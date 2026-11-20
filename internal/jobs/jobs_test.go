package jobs

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/riverqueue/river/rivertype"

	"github.com/MamaShip/Observer/internal/notify"
	"github.com/MamaShip/Observer/internal/watch"
)

func TestResult(t *testing.T) {
	plain := errors.New("connection reset")
	if result(nil) != nil {
		t.Error("nil error changed")
	}
	if got := result(plain); got != plain { //nolint:errorlint // must be returned unchanged
		t.Errorf("plain error: %v", got)
	}

	var snooze *rivertype.JobSnoozeError
	if got := result(fmt.Errorf("check: %w", &watch.SnoozeError{After: 30 * time.Minute})); !errors.As(got, &snooze) || snooze.Duration != 30*time.Minute {
		t.Errorf("SnoozeError: %v", got)
	}
	if got := result(&notify.RetryAt{Time: time.Now().Add(time.Hour)}); !errors.As(got, &snooze) || snooze.Duration < 59*time.Minute || snooze.Duration > time.Hour {
		t.Errorf("RetryAt in an hour: %v", got)
	}
	// A retry time already past must not panic River.
	if got := result(&notify.RetryAt{Time: time.Now().Add(-time.Minute)}); !errors.As(got, &snooze) || snooze.Duration != 0 {
		t.Errorf("RetryAt in the past: %v", got)
	}

	var cancel *rivertype.JobCancelError
	if got := result(fmt.Errorf("resolve: %w", watch.ErrPermanent)); !errors.As(got, &cancel) {
		t.Errorf("ErrPermanent: %v", got)
	}
}
