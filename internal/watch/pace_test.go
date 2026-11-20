package watch

import (
	"context"
	"testing"
	"time"

	"github.com/MamaShip/Observer/internal/wechat"
)

func TestCheckInterval(t *testing.T) {
	for age, want := range map[time.Duration]time.Duration{
		0:                                3 * time.Hour,
		24*time.Hour - time.Nanosecond:   3 * time.Hour,
		24 * time.Hour:                   8 * time.Hour,
		7*24*time.Hour - time.Nanosecond: 8 * time.Hour,
		7 * 24 * time.Hour:               24 * time.Hour,
		59 * 24 * time.Hour:              24 * time.Hour,
	} {
		if got := checkInterval(age); got != want {
			t.Errorf("checkInterval(%s) = %s, want %s", age, got, want)
		}
	}
}

func TestFailureBackoff(t *testing.T) {
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, 80 * time.Minute, 2 * time.Hour, 2 * time.Hour}
	for i, w := range want {
		if got := failureBackoff(int32(i + 1)); got != w {
			t.Errorf("failureBackoff(%d) = %s, want %s", i+1, got, w)
		}
	}
}

func TestJitter(t *testing.T) {
	for range 1000 {
		if d := jitter(time.Hour); d < 48*time.Minute || d > 72*time.Minute {
			t.Fatalf("jitter(1h) = %s", d)
		}
	}
}

func TestBreaker(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := NewBreaker(3, 10*time.Minute, 30*time.Minute)
	b.now = func() time.Time { return now }

	b.Record(wechat.VerdictBlocked)
	b.Record(wechat.VerdictBlocked)
	b.Record(wechat.VerdictAlive) // a normal answer resets the count
	b.Record(wechat.VerdictBlocked)
	now = now.Add(11 * time.Minute) // outside the window
	b.Record(wechat.VerdictBlocked)
	if !b.OpenUntil().IsZero() {
		t.Fatal("opened without 3 blocks inside the window")
	}
	b.Record(wechat.VerdictBlocked)
	if !b.Record(wechat.VerdictBlocked) {
		t.Fatal("third block in the window did not open the breaker")
	}
	if until := b.OpenUntil(); !until.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("open until %s", until)
	}
	now = now.Add(31 * time.Minute)
	if !b.OpenUntil().IsZero() {
		t.Fatal("breaker did not close after the cooldown")
	}
}

func TestLimiterSpacesRequests(t *testing.T) {
	l := NewLimiter(40 * time.Millisecond)
	ctx := context.Background()
	start := time.Now()
	for range 3 {
		if err := l.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// Two gaps of 40–60 ms.
	if d := time.Since(start); d < 80*time.Millisecond || d > 200*time.Millisecond {
		t.Errorf("3 requests took %s", d)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_ = l.Wait(ctx) // reserve a slot in the future
	if err := l.Wait(cctx); err == nil {
		t.Error("Wait ignored a cancelled context")
	}
}
