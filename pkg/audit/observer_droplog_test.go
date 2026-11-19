package audit

import (
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/clock"
)

// Spec: §8.3 — a drop held back by the per-minute bound is logged when the
// key's minute ends, through the trailing timer, and a line written in the
// meantime cancels the timer so the next line waits a full minute.
func TestDropLog_TrailingLineAfterTheInterval(t *testing.T) {
	logs := captureLog(t)
	frozen := clock.NewFrozen(time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC))
	d := newDropLog(frozen)
	d.record("dest", ReasonQueueFull, 1)
	d.record("dest", ReasonQueueFull, 3)
	key := dropKey{name: "dest", reason: ReasonQueueFull}
	d.mu.Lock()
	armed := d.entries[key].timer != nil
	d.mu.Unlock()
	if !armed {
		t.Fatal("a suppressed drop armed no trailing timer")
	}
	frozen.Advance(time.Minute)
	d.flushKey(key)
	if n := strings.Count(logs.String(), "audit: dropped 3 record(s) for destination dest: queue_full"); n != 1 {
		t.Fatalf("trailing line count = %d, want 1:\n%s", n, logs.String())
	}
	d.mu.Lock()
	cleared := d.entries[key].timer == nil
	d.mu.Unlock()
	if !cleared {
		t.Error("the trailing line left its timer armed")
	}
	// A key with nothing pending writes nothing when its timer fires.
	before := logs.String()
	d.flushKey(key)
	d.flushKey(dropKey{name: "absent", reason: ReasonQueueFull})
	if logs.String() != before {
		t.Errorf("a timer with nothing pending logged %q", strings.TrimPrefix(logs.String(), before))
	}
}
