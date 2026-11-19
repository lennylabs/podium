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
// A suppressed count is written when the key's minute ends, by a timer, and
// at the latest when the stream shuts down, so no counted drop goes unlogged.
type dropLog struct {
	clock clock.Clock

	// mu guards entries and each entry's timer.
	mu      sync.Mutex
	entries map[dropKey]*dropEntry
}

type dropKey struct{ name, reason string }

type dropEntry struct {
	logged  time.Time
	pending int
	// timer writes the pending count when the key's minute ends; nil while
	// nothing is pending.
	timer *time.Timer
}

func newDropLog(c clock.Clock) *dropLog {
	return &dropLog{clock: c, entries: map[dropKey]*dropEntry{}}
}

// record counts n drops for name and reason and writes a line when no line
// for that key was written in the last dropLogInterval. A suppressed count
// stays pending, and a timer writes it when the interval ends, so the line
// rate stays within the per-minute bound while the process runs.
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
		if ent.timer == nil {
			ent.timer = time.AfterFunc(dropLogInterval-now.Sub(ent.logged), func() { d.flushKey(key) })
		}
		return
	}
	writeDropLine(key, ent, now)
}

// flushKey writes key's pending count, if any, when its suppression ends.
func (d *dropLog) flushKey(key dropKey) {
	d.mu.Lock()
	defer d.mu.Unlock()
	ent := d.entries[key]
	if ent == nil {
		return
	}
	if ent.pending > 0 {
		writeDropLine(key, ent, d.clock.Now())
	}
	ent.timer = nil
}

// flushAll writes every pending count, which stops each armed timer: a timer
// is armed only while its key has a count pending, and writeDropLine cancels
// it. The stream calls flushAll once, at shutdown, so the drops of a
// process's last minute, such as the records a shutdown could not deliver,
// are logged before it exits.
//
// Spec: §8.3
func (d *dropLog) flushAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.clock.Now()
	for key, ent := range d.entries {
		if ent.pending > 0 {
			writeDropLine(key, ent, now)
		}
	}
}

// writeDropLine logs ent's pending count for key, resets it, and cancels the
// key's trailing timer, so the next line waits a full interval from this one.
// The caller holds the dropLog mutex.
func writeDropLine(key dropKey, ent *dropEntry, now time.Time) {
	log.Printf("audit: dropped %d record(s) for destination %s: %s", ent.pending, key.name, key.reason)
	ent.logged = now
	ent.pending = 0
	if ent.timer != nil {
		ent.timer.Stop()
		ent.timer = nil
	}
}
