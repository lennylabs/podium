package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// eventBus is the in-process pub/sub the §7.6 subscribe path uses.
// Fan-out is N subscribers reading from per-subscriber buffered
// channels; slow subscribers drop events rather than blocking the
// publisher (drop-and-record so callers can detect lag).
//
// Multi-replica deployments swap this for a durable broker
// (Kafka, NATS). For single-replica deployments the in-process
// bus is sufficient and stays dependency-free.
type eventBus struct {
	mu     sync.RWMutex
	nextID uint64
	subs   map[uint64]*eventSubscription
	// heartbeat is the per-connection keepalive interval. Defaults
	// to 30s; tests override via SetHeartbeatForTesting.
	heartbeat time.Duration
}

// SetHeartbeatForTesting overrides the per-connection heartbeat
// interval. Test-only: production code uses the 30s default.
func (s *Server) SetHeartbeatForTesting(d time.Duration) {
	if s.events != nil {
		s.events.heartbeat = d
	}
}

// eventSubscription captures one /v1/events connection.
type eventSubscription struct {
	id      uint64
	filter  map[string]bool
	ch      chan registryEvent
	dropped atomic.Int64
}

// registryEvent is the on-the-wire shape clients see (§7.6 + the
// TS SDK's RegistryEvent type). The fields are intentionally
// permissive — callers add or remove keys as the registry-side
// surface evolves without breaking older subscribers.
type registryEvent struct {
	Event     string         `json:"event"`
	TraceID   string         `json:"trace_id,omitempty"`
	Timestamp string         `json:"timestamp,omitempty"`
	Actor     map[string]any `json:"actor,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

// newEventBus returns an empty bus with a 30-second heartbeat.
func newEventBus() *eventBus {
	return &eventBus{
		subs:      map[uint64]*eventSubscription{},
		heartbeat: 30 * time.Second,
	}
}

// publish fans the event out to every active subscriber whose
// filter accepts the event type. A blocked subscriber is recorded
// (its dropped counter increments) but does not slow the publisher.
func (b *eventBus) publish(e registryEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subs {
		if len(sub.filter) > 0 && !sub.filter[e.Event] {
			continue
		}
		select {
		case sub.ch <- e:
		default:
			sub.dropped.Add(1)
		}
	}
}

// subscribe registers a new subscription with the configured event
// filter (empty means "all events") and returns the subscription
// plus a cancel function.
func (b *eventBus) subscribe(eventTypes []string) (*eventSubscription, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := b.nextID
	sub := &eventSubscription{
		id:     id,
		filter: map[string]bool{},
		ch:     make(chan registryEvent, 256),
	}
	for _, t := range eventTypes {
		if t != "" {
			sub.filter[t] = true
		}
	}
	b.subs[id] = sub
	cancel := func() {
		b.mu.Lock()
		delete(b.subs, id)
		close(sub.ch)
		b.mu.Unlock()
	}
	return sub, cancel
}

// handleEvents serves §7.6 GET /v1/events?type=...&type=... as a
// NDJSON stream. The connection stays open until the client
// disconnects (browser close, ctx cancel) or the writer fails.
//
// Heartbeats: the handler emits a `{"event":"_heartbeat"}` JSON
// line every 30 seconds so HTTP/2 / proxy-buffered consumers know
// the connection is alive.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "registry.unavailable",
			"streaming not supported")
		return
	}
	types := r.URL.Query()["type"]
	sub, cancel := s.events.subscribe(types)
	defer cancel()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	w.WriteHeader(http.StatusOK)
	// Flush the response headers immediately so a subscriber's client returns
	// from its request as soon as the stream is established, rather than blocking
	// until the first event or the heartbeat interval. net/http buffers the
	// status line and headers until the first body write or flush; without this
	// a quiet stream withholds the 200 from the client for up to one heartbeat.
	// Subscribing before the flush guarantees that once the client observes the
	// 200, the subscription is active, so no event fired after the response is
	// missed by the new subscriber.
	flusher.Flush()

	enc := json.NewEncoder(w)
	hb := s.events.heartbeat
	if hb <= 0 {
		hb = 30 * time.Second
	}
	heartbeat := time.NewTicker(hb)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-sub.ch:
			if !ok {
				return
			}
			if err := enc.Encode(ev); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if err := enc.Encode(registryEvent{Event: "_heartbeat"}); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// PublishEvent surfaces the bus to callers (e.g., the audit
// emitter). Wraps publish so external code never sees the
// concurrency primitives. The signature matches ingest.EventEmitter
// so the orchestrator passes this method directly.
//
// The §7.3.2 trace id and actor are recovered from the per-request
// audit metadata on ctx (set by withAuditMetaMiddleware). Both are
// stamped on the streamed event and threaded into the outbound
// webhook delivery so receivers can correlate (trace_id) and
// attribute (actor). When ctx carries no metadata the trace id is
// empty and actor is an empty object, keeping the wire schema stable.
//
// When a §7.3.2 outbound webhook worker is wired (WithWebhooks),
// this also fans the event out to every matching receiver
// asynchronously.
func (s *Server) PublishEvent(ctx context.Context, eventType string, data map[string]any) {
	if s.events == nil {
		return
	}
	meta, ok := AuditMetaFromContext(ctx)
	traceID := meta.TraceID
	actor := actorFromMeta(meta, ok)
	if s.webhooks != nil {
		// Fire outbound deliveries asynchronously so a slow receiver
		// never blocks the publisher. Route per receiver by Debounce:
		// a windowless receiver (zero Debounce) takes the immediate
		// single-event path, and a debounced receiver enqueues into its
		// trailing window for one batch delivery (§7.3.2). Routing per
		// receiver keeps a debounced receiver out of the single-event
		// delivery so a burst yields exactly one batch.
		go s.fanOutWebhooks(eventType, traceID, actor, data)
	}
	s.events.publish(registryEvent{
		Event:     eventType,
		TraceID:   traceID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Actor:     actor,
		Data:      data,
	})
}

// fanOutWebhooks routes one published event to every matching, non-disabled
// receiver in the tenant by its debounce window (§7.3.2). It lists the
// receivers once, then for each one sends a windowless receiver (zero Debounce)
// through the worker's single-event path and a debounced receiver (non-zero
// Debounce) through its trailing window. The single-event path and the window
// each re-apply the receiver's event filter, so this routing does not
// pre-filter. On a list error it falls back to skipping outbound delivery for
// this event so a transient store failure never blocks the bus publish.
//
// Each windowless delivery runs in its own goroutine, bounded by the worker's
// MaxConcurrent semaphore inside DeliverOne, so a slow receiver does not delay
// another receiver's delivery for the same event. Enqueue holds the buffer lock
// only to extend the window and returns immediately, so a debounced receiver is
// routed inline.
//
// fanOutWebhooks runs on its own goroutine off PublishEvent and uses a
// background context so a slow receiver never blocks the publisher or rides the
// request's cancellation.
func (s *Server) fanOutWebhooks(eventType, traceID string, actor, data map[string]any) {
	ctx := context.Background()
	receivers, err := s.webhooks.Store.List(ctx, s.tenant)
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, r := range receivers {
		if r.Disabled || !r.Matches(eventType) {
			continue
		}
		if r.Debounce > 0 {
			s.webhooks.Enqueue(ctx, r, eventType, traceID, actor, data)
			continue
		}
		r := r
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.webhooks.DeliverOne(ctx, r, eventType, traceID, actor, data)
		}()
	}
	wg.Wait()
}

// actorFromMeta renders the §7.3.2 webhook `actor` object from the
// per-request audit metadata. It always returns a non-nil map so the
// outbound body carries a stable `actor` key even for events that
// resolve no caller (the map is then empty). Authenticated callers
// contribute their email and groups; public-mode callers contribute
// the source IP and any upstream forwarded user.
func actorFromMeta(m AuditMeta, ok bool) map[string]any {
	actor := map[string]any{}
	if !ok {
		return actor
	}
	if m.PublicMode {
		actor["type"] = "public"
		if m.SourceIP != "" {
			actor["source_ip"] = m.SourceIP
		}
		if m.ForwardedUser != "" {
			actor["forwarded_user"] = m.ForwardedUser
		}
		return actor
	}
	if m.Email != "" {
		actor["email"] = m.Email
	}
	if len(m.Groups) > 0 {
		actor["groups"] = m.Groups
	}
	return actor
}
