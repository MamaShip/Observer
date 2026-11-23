package watch

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/MamaShip/Observer/internal/wechat"
)

// Check schedule (docs/design.md §5.5).
const (
	deletionConfirmDelay = 10 * time.Minute // second look before declaring an article deleted
	failureBackoffBase   = 5 * time.Minute
	failureBackoffMax    = 2 * time.Hour
	alertAfterFailures   = 6
)

// checkInterval is the base delay between checks for an article first seen age ago.
func checkInterval(age time.Duration) time.Duration {
	switch {
	case age < 24*time.Hour:
		return 3 * time.Hour
	case age < 7*24*time.Hour:
		return 8 * time.Hour
	default:
		return 24 * time.Hour
	}
}

// failureBackoff is the delay after the n-th consecutive inconclusive check.
func failureBackoff(n int32) time.Duration {
	d := failureBackoffBase
	for i := int32(1); i < n && d < failureBackoffMax; i++ {
		d *= 2
	}
	return min(d, failureBackoffMax)
}

// jitter spreads d by ±20% so checks do not bunch up.
func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

// Limiter spaces out page requests to WeChat across all jobs: at most one
// request per interval (plus up to half an interval of random slack).
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

// NewLimiter returns a limiter allowing one request per interval.
func NewLimiter(interval time.Duration) *Limiter { return &Limiter{interval: interval} }

// Wait blocks until the caller may send a request.
func (l *Limiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	at := now
	if l.next.After(now) {
		at = l.next
	}
	gap := l.interval
	if gap > 0 {
		gap += time.Duration(rand.Int64N(int64(gap)/2 + 1))
	}
	l.next = at.Add(gap)
	l.mu.Unlock()

	d := time.Until(at)
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Breaker pauses all fetching after WeChat blocks several requests in a short
// window, rather than letting every article back off on its own.
type Breaker struct {
	mu        sync.Mutex
	window    time.Duration
	threshold int
	cooldown  time.Duration
	hits      []time.Time
	openUntil time.Time
	now       func() time.Time
}

// NewBreaker opens after threshold blocked results within window, for cooldown.
func NewBreaker(threshold int, window, cooldown time.Duration) *Breaker {
	return &Breaker{threshold: threshold, window: window, cooldown: cooldown, now: time.Now}
}

// Record notes one fetch result and reports whether this call opened the breaker.
func (b *Breaker) Record(v wechat.Verdict) (opened bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	switch v {
	case wechat.VerdictBlocked:
		keep := b.hits[:0]
		for _, t := range b.hits {
			if now.Sub(t) < b.window {
				keep = append(keep, t)
			}
		}
		b.hits = append(keep, now)
		if len(b.hits) >= b.threshold && !now.Before(b.openUntil) {
			b.openUntil = now.Add(b.cooldown)
			b.hits = nil
			return true
		}
	case wechat.VerdictAlive, wechat.VerdictDeleted:
		b.hits = nil // WeChat answered normally: not a systematic block
	}
	return false
}

// OpenUntil returns when the breaker closes, or the zero time if it is closed.
func (b *Breaker) OpenUntil() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.now().Before(b.openUntil) {
		return b.openUntil
	}
	return time.Time{}
}
