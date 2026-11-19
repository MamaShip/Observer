package watch

import (
	"context"
	"fmt"
	"time"

	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/wechat"
)

// Periodic job intervals.
const (
	ScheduleInterval = time.Minute
	ExpireInterval   = 10 * time.Minute
	CanaryInterval   = 6 * time.Hour
	CleanupInterval  = 24 * time.Hour

	checkRunRetention = 180 * 24 * time.Hour
	scheduleBatch     = 200
)

// ScheduleDue queues a check for every article whose next check is due.
// Unique jobs keep an article that is already queued from being queued twice.
func (s *Service) ScheduleDue(ctx context.Context) (int, error) {
	if !s.breaker.OpenUntil().IsZero() {
		return 0, nil
	}
	ids, err := s.q.DueArticles(ctx, db.DueArticlesParams{Now: s.now(), MaxRows: scheduleBatch})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		res, err := s.jobs.Insert(ctx, CheckArgs{ArticleID: id}, nil)
		if err != nil {
			return n, err
		}
		if !res.UniqueSkippedAsDuplicate {
			n++
		}
	}
	return n, nil
}

// Expire ends watches whose period is over and stops checking articles
// nobody watches any more (plan.md §6.5).
func (s *Service) Expire(ctx context.Context) error {
	ended, err := s.q.ExpireWatches(ctx, s.now())
	if err != nil {
		return err
	}
	// Users who did not ask for expiry emails: mark them as notified now.
	if _, err := s.q.SkipExpiryNotifications(ctx, s.now()); err != nil {
		return err
	}
	dormant, err := s.q.MarkUnwatchedArticlesDormant(ctx)
	if err != nil {
		return err
	}
	if len(ended) > 0 || dormant > 0 {
		s.log.Info("watches expired", "watches", len(ended), "articles_dormant", dormant)
	}
	return nil
}

// CanaryResult is one canary check.
type CanaryResult struct {
	URL       string
	Want, Got wechat.Verdict
	Rule      string
}

// Canary checks known pages and reports any whose verdict is not the
// expected one: the first sign that WeChat changed its pages (plan.md §6.6).
func (s *Service) Canary(ctx context.Context) ([]CanaryResult, error) {
	var bad []CanaryResult
	for _, c := range []struct {
		url  string
		want wechat.Verdict
	}{{s.cfg.CanaryAliveURL, wechat.VerdictAlive}, {s.cfg.CanaryDeletedURL, wechat.VerdictDeleted}} {
		if c.url == "" {
			continue
		}
		link, err := wechat.ParseLink(c.url)
		if err != nil {
			return bad, err
		}
		f, err := s.fetchPage(ctx, link.ServiceFetchURL(), wechat.FetchCheck)
		if err != nil {
			return bad, err
		}
		if f.result.Verdict != c.want {
			r := CanaryResult{URL: c.url, Want: c.want, Got: f.result.Verdict, Rule: f.result.Rule}
			bad = append(bad, r)
			s.alert.Alert(ctx, "canary", "金丝雀检测失败：删除检测可能已失效",
				fmt.Sprintf("%s 预期 %s，实际 %s（规则 %s）。微信可能改了页面结构，请用 observer probe 检查并更新 internal/wechat 的规则。",
					r.URL, r.Want, r.Got, r.Rule))
		}
	}
	return bad, nil
}

// Cleanup deletes check runs past retention, keeping the ones that changed
// an article's state.
func (s *Service) Cleanup(ctx context.Context) error {
	n, err := s.q.DeleteOldCheckRuns(ctx, s.now().Add(-checkRunRetention))
	if err == nil && n > 0 {
		s.log.Info("old check runs deleted", "rows", n)
	}
	return err
}
