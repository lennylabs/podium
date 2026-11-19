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

// Spec: §8.3 — further drops within the minute join the pending count under
// the one armed timer, and a drop recorded after the minute writes the line
// itself and cancels that timer, so the held-back drops are logged once.
func TestDropLog_LineAfterTheIntervalCancelsTheTimer(t *testing.T) {
	logs := captureLog(t)
	frozen := clock.NewFrozen(time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC))
	d := newDropLog(frozen)
	key := dropKey{name: "dest", reason: ReasonQueueFull}
	timer := func() *time.Timer {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.entries[key].timer
	}
	d.record("dest", ReasonQueueFull, 1)
	d.record("dest", ReasonQueueFull, 2)
	first := timer()
	d.record("dest", ReasonQueueFull, 3)
	same := timer() == first
	frozen.Advance(time.Minute)
	d.record("dest", ReasonQueueFull, 4)
	out := logs.String()
	if first == nil || !same || timer() != nil ||
		strings.Count(out, "audit: dropped 1 record(s)") != 1 || strings.Count(out, "audit: dropped 9 record(s)") != 1 || strings.Count(out, "audit: dropped") != 2 {
		t.Errorf("armed %t, one timer %t, cleared %t; log:\n%s", first != nil, same, timer() == nil, out)
	}
}

// Spec: §8.3 — the trailing timer fires when the key's minute ends and logs
// the held-back count without another drop arriving.
func TestDropLog_TimerLogsTheHeldBackCount(t *testing.T) {
	logs := captureLog(t)
	frozen := clock.NewFrozen(time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC))
	d := newDropLog(frozen)
	d.record("dest", ReasonQueueFull, 1)
	// The minute ends a few milliseconds of real time after the held-back
	// drop, so the timer fires within the test.
	frozen.Advance(dropLogInterval - 5*time.Millisecond)
	d.record("dest", ReasonQueueFull, 2)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "audit: dropped 2 record(s)") {
		if time.Now().After(deadline) {
			t.Fatalf("the trailing timer logged nothing:\n%s", logs.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Spec: §8.3 — at shutdown, flushAll logs a held-back count and stops the
// armed timer, so the count is logged once.
func TestDropLog_FlushAllStopsTheArmedTimer(t *testing.T) {
	logs := captureLog(t)
	frozen := clock.NewFrozen(time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC))
	d := newDropLog(frozen)
	d.record("dest", ReasonQueueFull, 1)
	d.record("dest", ReasonQueueFull, 2)
	d.flushAll()
	key := dropKey{name: "dest", reason: ReasonQueueFull}
	d.mu.Lock()
	stopped := d.entries[key].timer == nil
	d.mu.Unlock()
	if out := logs.String(); !stopped || strings.Count(out, "audit: dropped 2 record(s)") != 1 {
		t.Errorf("timer stopped %t; log:\n%s", stopped, out)
	}
}
