package ingest

import (
	"log"
	"strconv"
	"sync"
	"time"

	"hookguard/web/internal/store"
)

// eventStore is the slice of *store.Store the batcher needs — narrowed to
// keep this package testable against a fake without pulling in all of
// store's surface.
type eventStore interface {
	InsertEvents(events []store.Event) error
	UpsertRollups(deltas []store.RollupDelta) error
	SetSetting(key, value string) error
}

// Batcher enqueues ingested events onto a buffered channel drained by one
// background goroutine on a 100ms ticker (DESIGN.md §8.1), turning N
// per-request writes into one batched write per tick against the
// single-writer SQLite handle. Shape mirrors root events.go's eventEmitter:
// buffered channel, one consumer goroutine, non-blocking bounded enqueue.
type Batcher struct {
	st    eventStore
	ch    chan store.Event
	tick  time.Duration
	flush chan chan error
	done  chan struct{}
	wg    sync.WaitGroup
}

// NewBatcher constructs a Batcher and starts its single background
// goroutine. Callers own calling Close (or just leaking it for the process
// lifetime, as main() does — a long-lived server has one Batcher for its
// entire run).
func NewBatcher(st eventStore) *Batcher {
	b := &Batcher{
		st:    st,
		ch:    make(chan store.Event, 1024),
		tick:  100 * time.Millisecond,
		flush: make(chan chan error),
		done:  make(chan struct{}),
	}
	b.wg.Add(1)
	go b.run()
	return b
}

// Enqueue is a non-blocking send that drops the oldest queued event to make
// room for the newest on overflow, matching root events.go's emit(): ingest
// must never apply backpressure to the HTTP response path.
func (b *Batcher) Enqueue(ev store.Event) {
	select {
	case b.ch <- ev:
		return
	default:
	}
	select {
	case <-b.ch:
	default:
	}
	select {
	case b.ch <- ev:
	default:
	}
}

// Flush blocks until every event enqueued before this call has been
// persisted, so tests can wait deterministically instead of sleeping/
// polling on the ticker.
func (b *Batcher) Flush() error {
	reply := make(chan error, 1)
	b.flush <- reply
	return <-reply
}

// Close stops the background goroutine after draining what's queued.
func (b *Batcher) Close() {
	close(b.done)
	b.wg.Wait()
}

func (b *Batcher) run() {
	defer b.wg.Done()
	ticker := time.NewTicker(b.tick)
	defer ticker.Stop()

	var pending []store.Event
	for {
		select {
		case ev := <-b.ch:
			pending = append(pending, ev)
		case <-ticker.C:
			pending = b.drain(pending)
		case reply := <-b.flush:
			// drainChannel first: Flush must persist everything Enqueue'd
			// before it was called, and a select with both b.ch and b.flush
			// ready gives no ordering guarantee between them, so pending
			// could otherwise miss an event that's sitting in the channel
			// right now.
			pending = b.drain(b.drainChannel(pending))
			reply <- nil
		case <-b.done:
			b.drain(b.drainChannel(pending))
			return
		}
	}
}

// drainChannel non-blockingly moves everything currently sitting in b.ch
// onto pending, for the flush/done paths that need an up-to-date view
// before persisting.
func (b *Batcher) drainChannel(pending []store.Event) []store.Event {
	for {
		select {
		case ev := <-b.ch:
			pending = append(pending, ev)
		default:
			return pending
		}
	}
}

// drain persists pending (if any) and returns nil so the caller can reset
// its accumulator in one line.
func (b *Batcher) drain(pending []store.Event) []store.Event {
	if len(pending) == 0 {
		return nil
	}
	if err := b.st.InsertEvents(pending); err != nil {
		log.Printf("ingest: insert events: %v", err)
		return nil
	}
	if err := b.st.UpsertRollups(rollupDeltas(pending)); err != nil {
		log.Printf("ingest: upsert rollups: %v", err)
	}
	last := pending[len(pending)-1]
	if err := b.st.SetSetting("last_ingest_at", strconv.FormatInt(last.ReceivedAt, 10)); err != nil {
		log.Printf("ingest: set last_ingest_at: %v", err)
	}
	return nil
}

// rollupDeltas sums a batch's events into per-(hour, provider, verdict)
// deltas so a batch of same-bucket events becomes one upsert of n=len(...),
// not one racing upsert per event.
func rollupDeltas(events []store.Event) []store.RollupDelta {
	type key struct {
		hour     int64
		provider string
		verdict  string
	}
	counts := make(map[key]int, len(events))
	for _, e := range events {
		k := key{hour: hourBucket(e.ReceivedAt), provider: e.Provider, verdict: e.Verdict}
		counts[k]++
	}
	deltas := make([]store.RollupDelta, 0, len(counts))
	for k, n := range counts {
		deltas = append(deltas, store.RollupDelta{Hour: k.hour, Provider: k.provider, Verdict: k.verdict, N: n})
	}
	return deltas
}

// hourBucket converts a unix-ms timestamp to the unix-hour bucket
// event_rollups keys on (DESIGN.md §8.2: "hour INTEGER -- unix hour bucket").
func hourBucket(unixMS int64) int64 {
	return unixMS / 1000 / 3600
}
