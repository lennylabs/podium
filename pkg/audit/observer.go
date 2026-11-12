package audit

import (
	"log"
	"sync"
	"time"

	"github.com/lennylabs/podium/internal/clock"
)

// Observer receives the §8.3 delivery counts of a Stream. pkg/metrics
// implements it, so pkg/audit carries no Prometheus dependency. A Stream
// reports deltas only, so the observer must see every count from the
// stream's first Append; OpenOptions fixes it at Open for that reason.
//
// Spec: §8.3, §13.8
type Observer interface {
	// AddQueued adds delta to the queue depth of the destination kind.
	AddQueued(kind string, delta int)
	// Dropped counts n records dropped for the destination kind with reason.
	Dropped(kind, reason string, n int)
}

// The §8.3 Loss accounting drop reasons, which are also the reason label
// values of podium_audit_dropped_total.
//
// Spec: §8.3
const (
	ReasonQueueFull  = "queue_full"
	ReasonRejected   = "rejected"
	ReasonShutdown   = "shutdown"
	ReasonWriteError = "write_error"
)

// dropLogInterval is the §8.3 Observability bound: at most one drop log
// line per destination and reason per interval.
//
// Spec: §8.3
const dropLogInterval = time.Minute

// dropped is the only caller of Observer.Dropped and the only writer of the
// drop log line, so no destination can count a drop without logging it.
// name is the destination's file path, "stdout", or redactedURL, never a
// raw URL. It does not take s.mu, because Append calls it while holding
// s.mu and the sender goroutines call it without.
//
// Spec: §8.3
func (s *Stream) dropped(kind destKind, name, reason string, n int) {
	if n == 0 {
		return
	}
	if s.opts.Observer != nil {
		s.opts.Observer.Dropped(string(kind), reason, n)
	}
	s.drops.record(name, reason, n)
}

// queued is the only caller of Observer.AddQueued. Like dropped, it does
// not take s.mu.
//
// Spec: §8.3, §13.8
func (s *Stream) queued(kind destKind, delta int) {
	if delta == 0 || s.opts.Observer == nil {
		return
	}
	s.opts.Observer.AddQueued(string(kind), delta)
}

// dropLog rate-limits the drop log line per destination name and reason.
// Drops counted while a key's line is suppressed are carried into the next
// line for that key, so each line states every drop since the previous one.
type dropLog struct {
	clock clock.Clock

	// mu guards entries.
	mu      sync.Mutex
	entries map[dropKey]*dropEntry
}

type dropKey struct{ name, reason string }

type dropEntry struct {
	logged  time.Time
	pending int
}

func newDropLog(c clock.Clock) *dropLog {
	return &dropLog{clock: c, entries: map[dropKey]*dropEntry{}}
}

// record counts n drops for name and reason and writes a line when no line
// for that key was written in the last dropLogInterval. A suppressed count
// stays pending until the next line for the key, so the per-minute bound
// holds at every point in the process, its exit included.
func (d *dropLog) record(name, reason string, n int) {
	now := d.clock.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	key := dropKey{name: name, reason: reason}
	ent, seen := d.entries[key]
	if !seen {
		ent = &dropEntry{}
		d.entries[key] = ent
	}
	ent.pending += n
	if seen && now.Sub(ent.logged) < dropLogInterval {
		return
	}
	writeDropLine(key, ent, now)
}

// writeDropLine logs ent's pending count for key and resets it. The caller
// holds the dropLog mutex.
func writeDropLine(key dropKey, ent *dropEntry, now time.Time) {
	log.Printf("audit: dropped %d record(s) for destination %s: %s", ent.pending, key.name, key.reason)
	ent.logged = now
	ent.pending = 0
}
