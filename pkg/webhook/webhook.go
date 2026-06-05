// Package webhook implements §7.3.2 outbound webhook delivery.
// The registry publishes change events through the in-process
// event bus (pkg/registry/server.eventBus); this package consumes
// those events and POSTs them to per-tenant receiver URLs with
// HMAC-signed bodies.
//
// Delivery semantics:
//   - One POST per (event, receiver) pair.
//   - X-Podium-Signature: sha256=<hex> over the request body using
//     the receiver's per-receiver secret.
//   - Retries with exponential backoff: 1s, 2s, 4s, 8s, 16s, 30s, 60s.
//   - Receiver auto-disables after 32 consecutive failures; an
//     operator re-enables via PUT /v1/webhooks/{id} with disabled=false.
//
// Receivers are stored as opaque rows in pkg/store. The package
// reads them once at worker startup and refreshes on every event
// (cheap, since the receiver list is bounded). Hot-reload is left
// to the operator: changing a receiver while events are firing may
// take effect on the next event.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Errors returned by Receiver and Worker functions.
var (
	// ErrInvalidConfig signals a malformed Receiver (missing URL,
	// invalid event filter, etc.).
	ErrInvalidConfig = errors.New("webhook: invalid_config")
	// ErrUnreachable wraps a transient delivery failure (5xx,
	// network error, timeout).
	ErrUnreachable = errors.New("webhook: receiver unreachable")
)

// Receiver is one configured outbound destination.
type Receiver struct {
	ID           string
	TenantID     string
	URL          string
	Secret       string
	EventFilter  []string
	Disabled     bool
	FailureCount int
	LastDelivery time.Time
	LastFailure  time.Time
	CreatedAt    time.Time
}

// Matches reports whether the receiver's filter accepts an event of
// the given type. An empty filter matches every event.
func (r Receiver) Matches(eventType string) bool {
	if len(r.EventFilter) == 0 {
		return true
	}
	for _, t := range r.EventFilter {
		if t == eventType {
			return true
		}
	}
	return false
}

// Store is the persistence SPI. Implementations live in pkg/store
// (a thin shim over the existing RegistryStore) for production and
// in pkg/webhook/webhooktest for tests.
type Store interface {
	List(ctx context.Context, tenantID string) ([]Receiver, error)
	Get(ctx context.Context, tenantID, id string) (Receiver, error)
	Put(ctx context.Context, r Receiver) error
	Delete(ctx context.Context, tenantID, id string) error
}

// Worker delivers events to every matching receiver. Construct one
// per server process (always by pointer; Worker carries a mutex and
// must not be copied); call Deliver for each event.
type Worker struct {
	Store      Store
	HTTPClient *http.Client
	// MaxFailures is the consecutive-failure threshold before a
	// receiver auto-disables. Zero defaults to 32.
	MaxFailures int
	// Backoff is the per-attempt backoff schedule. Zero defaults to
	// the spec-recommended sequence: 1s, 2s, 4s, 8s, 16s, 30s, 60s.
	Backoff []time.Duration
	// Now overrides the clock for tests.
	Now func() time.Time
	// MaxConcurrent bounds the number of in-flight HTTP deliveries
	// across every concurrent Deliver call so a burst of events (each
	// fired from its own goroutine in Server.PublishEvent) cannot
	// leave an unbounded number of outstanding requests. Zero
	// defaults to 8. spec: §7.3.2.
	MaxConcurrent int

	// mu serializes the per-receiver failure-counter read-modify-write
	// so two concurrent events never lose an increment.
	mu sync.Mutex
	// sem bounds concurrent deliveries; lazily sized from MaxConcurrent
	// on first use via semOnce.
	sem     chan struct{}
	semOnce sync.Once
}

// semaphore lazily builds and returns the delivery concurrency limiter,
// sized from MaxConcurrent (default 8).
func (w *Worker) semaphore() chan struct{} {
	w.semOnce.Do(func() {
		n := w.MaxConcurrent
		if n <= 0 {
			n = 8
		}
		w.sem = make(chan struct{}, n)
	})
	return w.sem
}

// Deliver fans the event out to every matching receiver in tenantID.
// Returns once every receiver has either acknowledged (2xx) or
// exhausted its retry budget; failures don't abort the fan-out.
//
// The marshaled body carries the full §7.3.2 schema
// {event, trace_id, timestamp, actor, data}. actor is always emitted
// as an object (an empty object when no caller is resolved) so the
// wire schema stays stable for receivers that key on it.
//
// Receivers that exceed MaxFailures consecutive failures are
// auto-disabled and recorded back to the store with Disabled=true.
// Concurrent deliveries are bounded by MaxConcurrent, and the
// per-receiver failure-counter update is serialized so two events
// firing close together never lose an increment.
func (w *Worker) Deliver(ctx context.Context, tenantID, eventType, traceID string, actor, body map[string]any) error {
	receivers, err := w.Store.List(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("webhook.Deliver: list: %w", err)
	}
	maxFailures := w.MaxFailures
	if maxFailures == 0 {
		maxFailures = 32
	}
	now := w.Now
	if now == nil {
		now = time.Now
	}
	if actor == nil {
		actor = map[string]any{}
	}
	payload, err := json.Marshal(map[string]any{
		"event":     eventType,
		"trace_id":  traceID,
		"timestamp": now().UTC().Format(time.RFC3339Nano),
		"actor":     actor,
		"data":      body,
	})
	if err != nil {
		return fmt.Errorf("webhook.Deliver: marshal: %w", err)
	}
	sem := w.semaphore()
	wg := sync.WaitGroup{}
	for _, r := range receivers {
		if r.Disabled || !r.Matches(eventType) {
			continue
		}
		r := r
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Bound concurrent in-flight HTTP across all Deliver calls.
			sem <- struct{}{}
			deliverErr := w.deliverWithRetry(ctx, r, payload)
			<-sem
			w.recordResult(ctx, tenantID, r, deliverErr, now, maxFailures)
		}()
	}
	wg.Wait()
	return nil
}

// recordResult applies one delivery outcome to the receiver's
// persisted failure state. It re-reads the receiver from the store
// under w.mu so concurrent deliveries serialize their read-modify-write
// and no increment is lost. The HTTP delivery itself runs
// outside the lock so a slow receiver never blocks another's update.
func (w *Worker) recordResult(ctx context.Context, tenantID string, r Receiver, deliverErr error, now func() time.Time, maxFailures int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	cur, err := w.Store.Get(ctx, tenantID, r.ID)
	if err != nil {
		// Receiver vanished or store has no fresher copy; fall back to
		// the snapshot we delivered against.
		cur = r
	}
	if deliverErr != nil {
		cur.FailureCount++
		cur.LastFailure = now()
		if cur.FailureCount >= maxFailures {
			cur.Disabled = true
		}
	} else {
		cur.FailureCount = 0
		cur.LastDelivery = now()
	}
	_ = w.Store.Put(ctx, cur)
}

// deliverWithRetry POSTs payload to r.URL and retries on transient
// failure per the configured Backoff schedule.
func (w *Worker) deliverWithRetry(ctx context.Context, r Receiver, payload []byte) error {
	backoff := w.Backoff
	if len(backoff) == 0 {
		backoff = []time.Duration{
			time.Second, 2 * time.Second, 4 * time.Second,
			8 * time.Second, 16 * time.Second, 30 * time.Second,
			60 * time.Second,
		}
	}
	for attempt := 0; attempt <= len(backoff); attempt++ {
		err := w.postOnce(ctx, r, payload)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrUnreachable) || attempt == len(backoff) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff[attempt]):
		}
	}
	return ErrUnreachable
}

// postOnce sends a single delivery attempt. 2xx → nil; 4xx →
// non-retryable error (caller stops); 5xx / network → ErrUnreachable.
func (w *Worker) postOnce(ctx context.Context, r Receiver, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "podium/webhook")
	req.Header.Set("X-Podium-Signature", "sha256="+SignBody(payload, r.Secret))

	client := w.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: HTTP %d", ErrUnreachable, resp.StatusCode)
	default:
		buf, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("webhook: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(buf)))
	}
}

// SignBody computes the §7.3.2 HMAC-SHA256 of body under secret. The
// returned string is the lowercase hex digest. Receivers verify
// with the same secret.
func SignBody(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyBody is the receiver-side counterpart to SignBody. Returns
// nil when the signature matches body under secret. Receivers and
// tests call it from their request handler.
func VerifyBody(body []byte, signature, secret string) error {
	want := SignBody(body, secret)
	if !hmac.Equal([]byte(want), []byte(strings.TrimPrefix(signature, "sha256="))) {
		return errors.New("webhook: signature mismatch")
	}
	return nil
}

// MemoryStore is an in-memory Store used by tests and small
// standalone deployments that don't persist webhook config across
// restarts.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]map[string]Receiver // tenant → id → receiver
}

// NewMemoryStore returns an empty in-memory webhook store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: map[string]map[string]Receiver{}}
}

// List returns all receivers for tenantID.
func (m *MemoryStore) List(_ context.Context, tenantID string) ([]Receiver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.rows[tenantID]
	out := make([]Receiver, 0, len(bucket))
	for _, r := range bucket {
		out = append(out, r)
	}
	return out, nil
}

// Get returns one receiver or ErrNotFound.
func (m *MemoryStore) Get(_ context.Context, tenantID, id string) (Receiver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if bucket, ok := m.rows[tenantID]; ok {
		if r, ok := bucket[id]; ok {
			return r, nil
		}
	}
	return Receiver{}, fmt.Errorf("webhook: receiver %s/%s not found", tenantID, id)
}

// Put upserts the receiver.
func (m *MemoryStore) Put(_ context.Context, r Receiver) error {
	if r.URL == "" || r.TenantID == "" || r.ID == "" {
		return ErrInvalidConfig
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.rows[r.TenantID]
	if !ok {
		bucket = map[string]Receiver{}
		m.rows[r.TenantID] = bucket
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	bucket[r.ID] = r
	return nil
}

// Delete removes the receiver. Missing keys are a no-op.
func (m *MemoryStore) Delete(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if bucket, ok := m.rows[tenantID]; ok {
		delete(bucket, id)
	}
	return nil
}

// quiet unused-import warning when callers don't need the helpers.
var _ = json.NewEncoder
