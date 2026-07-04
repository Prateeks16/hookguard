// Package retention runs the nightly prune of old events (DESIGN.md §8.2):
// "a nightly job deletes events older than retention_days (default 30) and
// keeps rollups for 13 months." event_rollups is never touched here.
package retention

import (
	"log"
	"sync"
	"time"
)

// retentionStore is the slice of *store.Store the job needs, narrowed for
// testability the same way ingest.eventStore is.
type retentionStore interface {
	GetRetentionDays() (int, error)
	DeleteEventsOlderThan(cutoffMS int64) (int64, error)
}

// Job runs Sweep once at startup and then once per interval, so a
// freshly-deployed instance doesn't wait a full day for its first prune.
// Shape mirrors ingest.Batcher: one goroutine, a done channel for clean
// shutdown.
type Job struct {
	st       retentionStore
	interval time.Duration
	now      func() time.Time
	done     chan struct{}
	wg       sync.WaitGroup
}

// defaultInterval is once a day — cheap enough to run more often, but the
// events table only grows meaningfully slower than that.
const defaultInterval = 24 * time.Hour

// NewJob constructs a Job and starts its background goroutine.
func NewJob(st retentionStore) *Job {
	j := &Job{
		st:       st,
		interval: defaultInterval,
		now:      time.Now,
		done:     make(chan struct{}),
	}
	j.wg.Add(1)
	go j.run()
	return j
}

// Close stops the background goroutine.
func (j *Job) Close() {
	close(j.done)
	j.wg.Wait()
}

func (j *Job) run() {
	defer j.wg.Done()

	j.sweepAndLog()

	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			j.sweepAndLog()
		case <-j.done:
			return
		}
	}
}

func (j *Job) sweepAndLog() {
	deleted, err := Sweep(j.st, j.now())
	if err != nil {
		log.Printf("retention: sweep: %v", err)
		return
	}
	if deleted > 0 {
		log.Printf("retention: pruned %d events older than retention window", deleted)
	}
}

// Sweep reads the current retention_days setting, computes the cutoff
// against now, and deletes events older than it — the pure "what to do on a
// tick" logic, kept separate from the ticker/goroutine plumbing so it's
// callable directly from a test without waiting on time, matching how
// store.SummaryWindow takes now as a parameter rather than calling
// time.Now() internally. Reading retention_days on every call (rather than
// caching it at construction) is what makes an admin's change to the
// Settings control take effect on the next tick without a restart.
func Sweep(st retentionStore, now time.Time) (int64, error) {
	days, err := st.GetRetentionDays()
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	return st.DeleteEventsOlderThan(cutoff)
}
