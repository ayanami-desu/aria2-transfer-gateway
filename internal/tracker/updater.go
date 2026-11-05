package tracker

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
)

type GlobalOptions interface {
	ChangeGlobalOption(context.Context, map[string]string) error
}

type Updater struct {
	source   *Source
	client   GlobalOptions
	interval time.Duration
	logger   *log.Logger

	lastApplied string
	applied     bool
}

func NewUpdater(source *Source, client GlobalOptions, interval time.Duration, logger *log.Logger) *Updater {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Updater{source: source, client: client, interval: interval, logger: logger}
}

const (
	minRetryDelay = time.Minute
	maxRetryDelay = time.Hour
)

func (u *Updater) Run(ctx context.Context) {
	retryDelay := minRetryDelay
	for {
		cycleCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := u.refresh(cycleCtx)
		cancel()

		wait := u.interval
		if err != nil {
			u.logger.Printf("warning: tracker list refresh failed: %v (retrying in %s)", err, retryDelay)
			wait = retryDelay
			retryDelay = nextRetryDelay(retryDelay)
		} else {
			retryDelay = minRetryDelay
		}
		if !waitFor(ctx, wait) {
			return
		}
	}
}

func nextRetryDelay(current time.Duration) time.Duration {
	if current <= 0 {
		return minRetryDelay
	}
	next := current * 2
	if next > maxRetryDelay || next <= 0 {
		return maxRetryDelay
	}
	return next
}

func (u *Updater) refresh(ctx context.Context) error {
	if u.source == nil {
		return errors.New("tracker source is nil")
	}
	if u.client == nil {
		return errors.New("global options client is nil")
	}
	count, err := u.source.Refresh(ctx)
	if err != nil {
		return err
	}
	value := strings.Join(u.source.List(), ",")
	if u.applied && value == u.lastApplied {
		return nil
	}
	if err := u.client.ChangeGlobalOption(ctx, map[string]string{"bt-tracker": value}); err != nil {
		return err
	}
	u.lastApplied = value
	u.applied = true
	u.logger.Printf("tracker list updated: %d trackers from %s", count, u.source.url)
	return nil
}

func waitFor(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
