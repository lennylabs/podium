package webhook

import (
	"context"
	"sync"
	"time"
)

// debounceBuffer coalesces a burst of matched events for one receiver into a
// single batch delivery (§7.3.2). The buffer is per-receiver, keyed by
// receiver ID, and opens a trailing window on the first matched event. Events
// that arrive within the window are held and deduplicated by (event type,
// key); on window expiry the buffer hands the coalesced events to
// Worker.DeliverBatch.
//
// The buffer is in-process. A registry restart mid-window drops the buffered
// events, which is consistent with the at-least-once, best-effort delivery the
// subsystem provides through retries: a CI receiver re-renders the full
// catalog on its next trigger, so a dropped batch is recovered by the next
// event.
//
// Synchronization: the buffer carries its own mutex for the per-receiver
// window map and the timers. The Worker mutex (Worker.mu) backs the
// failure-counter read-modify-write and is held only inside recordResult, so
// the buffer's lock and the Worker's lock never nest. The buffer lock is
// released before DeliverBatch runs so a slow receiver never blocks an Enqueue
// for another receiver under MaxConcurrent.
type debounceBuffer struct {
	worker *Worker

	mu      sync.Mutex
	windows map[string]*debounceWindow // receiver ID → open window
}

// debounceWindow is the open trailing window for one receiver. It holds the
// coalesced events in arrival order with a dedup index by (event type, key)
// so a later event for the same target overwrites the earlier held copy.
type debounceWindow struct {
	receiver Receiver
	traceID  string // trace of the first event; identifies the batch delivery
	start    time.Time
	order    []dedupKey // arrival order of distinct (event type, key) pairs
	byKey    map[dedupKey]BatchEvent
	timer    stopper
}

// dedupKey identifies a coalesced event within a window. key is the
// per-event-type dedup key (§7.3.2): the artifact ID for artifact.published
// and artifact.deprecated, the layer ID for layer.ingested and
// layer.history_rewritten, and the domain path for domain.published. An event
// type with no recognized key field uses the empty key, so events of that type
// coalesce to one held copy per window.
type dedupKey struct {
	eventType string
	key       string
}

// stopper is the subset of *time.Timer the buffer needs. The seam lets a test
// inject a manual timer so the trailing window fires deterministically through
// the flush hook instead of through wall-clock expiry.
type stopper interface {
	Stop() bool
}

// debounce returns the worker's debounce buffer, building it on first use.
func (w *Worker) debounce() *debounceBuffer {
	w.debounceOnce.Do(func() {
		w.debounceBuf = &debounceBuffer{
			worker:  w,
			windows: map[string]*debounceWindow{},
		}
	})
	return w.debounceBuf
}

// Enqueue holds one matched event for a debounced receiver (§7.3.2). It is the
// publish-path entry point for a receiver with a non-zero Debounce: instead of
// delivering the event immediately, the worker enqueues it into the receiver's
// trailing window. The first event for a receiver opens the window and arms a
// timer for receiver.Debounce; subsequent events within the window are
// deduplicated by (event type, key) and held. On window expiry the buffer
// delivers the coalesced events through DeliverBatch.
//
// Enqueue serves only receivers with a window. A receiver with a zero Debounce
// has no window, and Enqueue is a no-op for it: the publish path routes a
// windowless receiver through Worker.Deliver, which preserves the unchanged
// single-event body. The caller selects Deliver (windowless receiver) or
// Enqueue (debounced receiver) by receiver.Debounce, so the batch body stays
// additive and scoped to receivers that opt in.
//
// data is the event-type-specific payload; the buffer reads the dedup key from
// it without copying, so the caller must not mutate data after the call.
func (w *Worker) Enqueue(ctx context.Context, receiver Receiver, eventType, traceID string, actor, data map[string]any) {
	w.debounce().enqueue(ctx, receiver, eventType, traceID, actor, data)
}

func (b *debounceBuffer) enqueue(_ context.Context, receiver Receiver, eventType, traceID string, actor, data map[string]any) {
	if receiver.Disabled || receiver.Debounce <= 0 {
		// A disabled receiver matches no event, and a windowless receiver
		// (zero Debounce) is delivered through Worker.Deliver by the publish
		// path, which preserves the unchanged single-event body. Enqueue opens
		// no window for either.
		return
	}
	ev := BatchEvent{
		EventType: eventType,
		TraceID:   traceID,
		Timestamp: b.now(),
		Actor:     actor,
		Data:      data,
	}
	dk := dedupKey{eventType: eventType, key: dedupValue(eventType, data)}

	b.mu.Lock()
	win, open := b.windows[receiver.ID]
	if !open {
		win = &debounceWindow{
			receiver: receiver,
			traceID:  traceID,
			start:    ev.Timestamp,
			byKey:    map[dedupKey]BatchEvent{},
		}
		b.windows[receiver.ID] = win
		win.timer = b.arm(receiver.ID, receiver.Debounce)
	}
	if _, seen := win.byKey[dk]; !seen {
		win.order = append(win.order, dk)
	}
	win.byKey[dk] = ev
	b.mu.Unlock()
}

// dedupValue returns the per-event-type dedup key from the event data (§7.3.2).
// An unrecognized event type or a missing field yields the empty key, so such
// events coalesce to one held copy per window rather than fanning out.
func dedupValue(eventType string, data map[string]any) string {
	var field string
	switch eventType {
	case "artifact.published", "artifact.deprecated":
		field = "id"
	case "layer.ingested", "layer.history_rewritten":
		field = "layer"
	case "domain.published":
		field = "domain"
	default:
		return ""
	}
	if v, ok := data[field].(string); ok {
		return v
	}
	return ""
}

// arm schedules the window expiry for receiverID after d. The default timer is
// a *time.Timer driven by wall-clock; a test overrides newTimer so the flush
// hook fires the window deterministically.
func (b *debounceBuffer) arm(receiverID string, d time.Duration) stopper {
	fire := func() { b.fire(receiverID) }
	if b.worker.newTimer != nil {
		return b.worker.newTimer(d, fire)
	}
	return time.AfterFunc(d, fire)
}

// fire closes the window for receiverID and delivers the coalesced events as
// one batch. It removes the window under the buffer lock, then runs
// DeliverBatch outside the lock so the delivery (and its retry budget) never
// blocks an Enqueue for another receiver.
func (b *debounceBuffer) fire(receiverID string) {
	b.mu.Lock()
	win, open := b.windows[receiverID]
	if !open {
		b.mu.Unlock()
		return
	}
	delete(b.windows, receiverID)
	events := make([]BatchEvent, 0, len(win.order))
	for _, dk := range win.order {
		events = append(events, win.byKey[dk])
	}
	b.mu.Unlock()

	_ = b.worker.DeliverBatch(context.Background(), win.receiver, win.traceID, win.start, events)
}

// Flush fires the open debounce window for receiverID immediately, bypassing
// the trailing timer, and reports whether a window was open. It is the test
// hook the step requires: a test injects a manual timer through newTimer so the
// wall-clock timer never fires, then calls Flush to deliver the coalesced batch
// deterministically. Flush is a no-op for a receiver with no open window.
func (w *Worker) Flush(receiverID string) bool {
	b := w.debounce()
	b.mu.Lock()
	win, open := b.windows[receiverID]
	if open && win.timer != nil {
		win.timer.Stop()
	}
	b.mu.Unlock()
	if !open {
		return false
	}
	b.fire(receiverID)
	return true
}

// now returns the buffer's clock, reusing Worker.Now so a test pins the window
// timestamps. It falls back to time.Now when no override is set.
func (b *debounceBuffer) now() time.Time {
	if b.worker.Now != nil {
		return b.worker.Now().UTC()
	}
	return time.Now().UTC()
}
