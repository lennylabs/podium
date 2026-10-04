package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/webhook"
)

// Spec: §7.6 — /v1/events streams change events as NDJSON. The TS
// SDK's `subscribe()` parses this stream; the server keeps a per-
// connection subscription on the in-process bus.
func TestEvents_StreamsPublishedEvents(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	srv := server.New(core.New(st, "t", nil))
	srv.SetHeartbeatForTesting(50 * time.Millisecond)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Open the stream in a goroutine so we can publish concurrently.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/v1/events?type=artifact.published", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Errorf("Content-Type = %q, want application/x-ndjson", ct)
	}

	// Publish a matching event after the connection settles. The event of
	// another tenant goes first: §7.6 withholds it without a trace, so the
	// first line the stream carries is the matching event.
	go func() {
		time.Sleep(50 * time.Millisecond)
		srv.PublishEvent(context.Background(), core.EventScope{TenantID: "other", Layers: []string{"L"}}, "artifact.published", map[string]any{
			"id": "withheld", "version": "1.0.0",
		})
		srv.PublishEvent(context.Background(), core.EventScope{TenantID: "t", Layers: []string{"L"}}, "artifact.published", map[string]any{
			"id": "finance/run", "version": "1.0.0",
		})
	}()

	scan := bufio.NewScanner(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	for scan.Scan() {
		if time.Now().After(deadline) {
			break
		}
		var ev map[string]any
		if err := json.Unmarshal(scan.Bytes(), &ev); err != nil {
			t.Fatalf("unmarshal event: %v (body=%q)", err, scan.Text())
		}
		if ev["event"] == "_heartbeat" {
			continue
		}
		if ev["event"] != "artifact.published" {
			t.Errorf("event type = %v, want artifact.published", ev["event"])
		}
		data, _ := ev["data"].(map[string]any)
		if data["id"] != "finance/run" {
			t.Errorf("data.id = %v, want finance/run", data["id"])
		}
		return
	}
	t.Fatal("no event received within timeout")
}

// Spec: §7.6 — events whose type is not in the subscriber's filter
// are dropped. Subscribing to type=A while only type=B fires
// produces no output (other than heartbeats).
func TestEvents_FilterDropsUnmatchedTypes(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	srv := server.New(core.New(st, "t", nil))
	srv.SetHeartbeatForTesting(50 * time.Millisecond)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/v1/events?type=does.not.match", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	go func() {
		time.Sleep(50 * time.Millisecond)
		srv.PublishEvent(context.Background(), core.EventScope{TenantID: "t", Layers: []string{"L"}}, "artifact.published", map[string]any{"id": "x"})
	}()

	// Read for 300ms; should see no artifact.published event.
	deadline := time.Now().Add(300 * time.Millisecond)
	scan := bufio.NewScanner(resp.Body)
	for scan.Scan() {
		if time.Now().After(deadline) {
			return
		}
		var ev map[string]any
		_ = json.Unmarshal(scan.Bytes(), &ev)
		if ev["event"] == "artifact.published" {
			t.Errorf("filter leak: got %v", ev)
		}
	}
}

// countingStore wraps a store and counts ListLayerConfigs calls on entry.
// When armed, each call blocks until release closes or the caller's context
// ends, which holds the §7.6 shared layer read open while a test observes the
// publish path and the waiting subscribers.
type countingStore struct {
	store.Store
	reads   atomic.Int64
	armed   atomic.Bool
	release chan struct{}
}

// ListLayerConfigs counts the call, blocks when armed, and delegates.
func (c *countingStore) ListLayerConfigs(ctx context.Context, tenantID string) ([]store.LayerConfig, error) {
	c.reads.Add(1)
	if c.armed.Load() {
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.Store.ListLayerConfigs(ctx, tenantID)
}

// eventsUserHeader names the test header the identity resolver maps to a
// layer.Identity.
const eventsUserHeader = "X-Test-User"

// eventsIdentities are the subscribers the visibility suite opens streams as.
// alice and dave are in the eng group, bob is in none, carol holds a
// finance-only read scope, erin belongs to organization orgB, and admin holds
// the tenant admin grant.
var eventsIdentities = map[string]layer.Identity{
	"alice": {Sub: "alice", Groups: []string{"eng"}, IsAuthenticated: true},
	"bob":   {Sub: "bob", IsAuthenticated: true},
	"carol": {Sub: "carol", Scopes: []string{"podium:read:finance/*"}, IsAuthenticated: true},
	"dave":  {Sub: "dave", Groups: []string{"eng"}, IsAuthenticated: true},
	"erin":  {Sub: "erin", OrgID: "orgB", IsAuthenticated: true},
	"admin": {Sub: "admin", IsAuthenticated: true},
}

// withEventsIdentities resolves the test header to an eventsIdentities entry.
func withEventsIdentities() server.Option {
	return server.WithIdentityResolver(func(r *http.Request) layer.Identity {
		return eventsIdentities[r.Header.Get(eventsUserHeader)]
	})
}

// newEventsRegistry seeds tenant "t" with the groups:[eng] layer "eng" and the
// public layer "pub", seeds tenant "B" with the public layer "pubB", and serves
// a registry bound to "t" over the counting store.
func newEventsRegistry(t *testing.T, opts ...server.Option) (*server.Server, *httptest.Server, *countingStore) {
	t.Helper()
	ctx := context.Background()
	mem := store.NewMemory()
	for _, tenant := range []string{"t", "B"} {
		if err := mem.CreateTenant(ctx, store.Tenant{ID: tenant}); err != nil {
			t.Fatalf("CreateTenant %s: %v", tenant, err)
		}
	}
	for _, cfg := range []store.LayerConfig{
		{TenantID: "t", ID: "eng", SourceType: "local", Order: 1, Groups: []string{"eng"}},
		{TenantID: "t", ID: "pub", SourceType: "local", Order: 2, Public: true},
		{TenantID: "B", ID: "pubB", SourceType: "local", Order: 1, Public: true},
	} {
		if err := mem.PutLayerConfig(ctx, cfg); err != nil {
			t.Fatalf("PutLayerConfig %s: %v", cfg.ID, err)
		}
	}
	cs := &countingStore{Store: mem, release: make(chan struct{})}
	srv := server.New(core.New(cs, "t", nil), opts...)
	srv.SetHeartbeatForTesting(50 * time.Millisecond)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, cs
}

// openEventStream opens GET /v1/events as user (no header when user is
// empty) and returns a channel of the non-heartbeat events the stream
// carries. The handler subscribes before it flushes the 200, so an event
// published after this returns reaches the subscription.
func openEventStream(t *testing.T, base, user string) <-chan map[string]any {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/events", nil)
	if user != "" {
		req.Header.Set(eventsUserHeader, user)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/events as %q: %v", user, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/events as %q: status %d", user, resp.StatusCode)
	}
	out := make(chan map[string]any, 16)
	go func() {
		defer close(out)
		scan := bufio.NewScanner(resp.Body)
		for scan.Scan() {
			var ev map[string]any
			if json.Unmarshal(scan.Bytes(), &ev) != nil || ev["event"] == "_heartbeat" {
				continue
			}
			out <- ev
		}
	}()
	return out
}

// nextEventData returns the data object of the next event on ch, failing the
// test when none arrives within two seconds.
func nextEventData(t *testing.T, who string, ch <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatalf("%s: stream closed before an event arrived", who)
		}
		data, _ := ev["data"].(map[string]any)
		return data
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: no event within 2s", who)
		return nil
	}
}

// publishArtifact publishes artifact.published for id under scope.
func publishArtifact(srv *server.Server, scope core.EventScope, id string) {
	srv.PublishEvent(context.Background(), scope, "artifact.published", map[string]any{"id": id})
}

// engScope and pubScope name the groups:[eng] and public layers of tenant "t".
var (
	engScope = core.EventScope{TenantID: "t", Layers: []string{"eng"}}
	pubScope = core.EventScope{TenantID: "t", Layers: []string{"pub"}}
)

// Spec: §7.6 — the stream delivers an event only to a subscriber who can see
// the layer it names under §4.6. alice (group eng) receives the groups-layer
// event and the public-layer event; bob's stream withholds the groups-layer
// event without a trace, so his first event is the public one.
func TestEvents_VisibilityFiltersGroupsLayer(t *testing.T) {
	t.Parallel()
	srv, ts, _ := newEventsRegistry(t, withEventsIdentities())
	alice := openEventStream(t, ts.URL, "alice")
	bob := openEventStream(t, ts.URL, "bob")

	publishArtifact(srv, engScope, "eng-only")
	publishArtifact(srv, pubScope, "everyone")

	if got := nextEventData(t, "alice", alice)["id"]; got != "eng-only" {
		t.Errorf("alice first event id = %v, want eng-only", got)
	}
	if got := nextEventData(t, "alice", alice)["id"]; got != "everyone" {
		t.Errorf("alice second event id = %v, want everyone", got)
	}
	if got := nextEventData(t, "bob", bob)["id"]; got != "everyone" {
		t.Errorf("bob first event id = %v, want everyone (the eng event must be withheld)", got)
	}
}

// Spec: §7.6 — a reorder reaches a subscriber who can see at least one named
// layer, with `layer` rewritten to the visible subset in reorder order. The
// rewrite copies the payload, so the published map is unchanged.
func TestEvents_ReorderRewritesLayerPerSubscriber(t *testing.T) {
	t.Parallel()
	srv, ts, _ := newEventsRegistry(t, withEventsIdentities())
	alice := openEventStream(t, ts.URL, "alice")
	bob := openEventStream(t, ts.URL, "bob")

	data := map[string]any{"layer": "eng,pub", "action": "reorder"}
	srv.PublishEvent(context.Background(),
		core.EventScope{TenantID: "t", Layers: []string{"eng", "pub"}},
		"layer.config_changed", data)

	if got := nextEventData(t, "bob", bob)["layer"]; got != "pub" {
		t.Errorf("bob reorder layer = %v, want pub", got)
	}
	if got := nextEventData(t, "alice", alice)["layer"]; got != "eng,pub" {
		t.Errorf("alice reorder layer = %v, want eng,pub", got)
	}
	if data["layer"] != "eng,pub" {
		t.Errorf("published payload layer = %v after delivery, want eng,pub", data["layer"])
	}
}

// Spec: §7.6 — artifact events also gate on the §6.3.1 path scopes. carol
// holds podium:read:finance/*, so an event for eng/x is withheld and one for
// finance/x is delivered, although both name the public layer.
//
// Spec: §6.3.1
func TestEvents_ScopesNarrowArtifactEvents(t *testing.T) {
	t.Parallel()
	srv, ts, _ := newEventsRegistry(t, withEventsIdentities())
	carol := openEventStream(t, ts.URL, "carol")

	publishArtifact(srv, core.EventScope{TenantID: "t", Layers: []string{"pub"}, Path: "eng/x"}, "eng/x")
	publishArtifact(srv, core.EventScope{TenantID: "t", Layers: []string{"pub"}, Path: "finance/x"}, "finance/x")

	if got := nextEventData(t, "carol", carol)["id"]; got != "finance/x" {
		t.Errorf("carol first event id = %v, want finance/x (eng/x is outside her scopes)", got)
	}
}

// Spec: §7.6 — the event's recorded tenant must equal the subscriber's routed
// tenant. erin's organization routes her to tenant B, so an event recorded
// under "t" is withheld and one recorded under "B" is delivered.
func TestEvents_MultiTenantRoutesSubscriberTenant(t *testing.T) {
	t.Parallel()
	router := server.WithTenantRouter(func(_ context.Context, org string) (store.Tenant, bool) {
		if org == "orgB" {
			return store.Tenant{ID: "B"}, true
		}
		return store.Tenant{}, false
	}, false)
	srv, ts, _ := newEventsRegistry(t, withEventsIdentities(), router)
	erin := openEventStream(t, ts.URL, "erin")

	publishArtifact(srv, pubScope, "tenant-t")
	publishArtifact(srv, core.EventScope{TenantID: "B", Layers: []string{"pubB"}}, "tenant-b")

	if got := nextEventData(t, "erin", erin)["id"]; got != "tenant-b" {
		t.Errorf("erin first event id = %v, want tenant-b (tenant t's event must be withheld)", got)
	}
}

// Spec: §7.6 — the §4.6 no-identity and public-mode bypasses admit every layer
// before any store read. The default resolver yields an IsPublic identity,
// and WithPublicMode marks bob's resolved identity public, so both receive the
// groups-layer event and the layer list is never read.
//
// Spec: §4.6
func TestEvents_PublicBypassAdmitsWithoutRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		user string
		opts []server.Option
	}{
		{name: "default resolver", user: ""},
		{name: "public mode", user: "bob", opts: []server.Option{withEventsIdentities(), server.WithPublicMode()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, ts, cs := newEventsRegistry(t, tc.opts...)
			stream := openEventStream(t, ts.URL, tc.user)
			cs.reads.Store(0)

			publishArtifact(srv, engScope, "eng-only")

			if got := nextEventData(t, tc.name, stream)["id"]; got != "eng-only" {
				t.Errorf("event id = %v, want eng-only", got)
			}
			if n := cs.reads.Load(); n != 0 {
				t.Errorf("ListLayerConfigs calls = %d, want 0", n)
			}
		})
	}
}

// Spec: §7.6 — PublishEvent performs no store read, so it returns while the
// event's layer read is blocked. Every subscriber of one event shares one
// layer read: with alice and dave subscribed, the blocked read is entered
// once, and both receive the event after it is released.
func TestEvents_SharedLayerReadDoesNotBlockPublish(t *testing.T) {
	t.Parallel()
	srv, ts, cs := newEventsRegistry(t, withEventsIdentities())
	alice := openEventStream(t, ts.URL, "alice")
	dave := openEventStream(t, ts.URL, "dave")
	cs.reads.Store(0)
	cs.armed.Store(true)

	published := make(chan struct{})
	go func() {
		publishArtifact(srv, engScope, "eng-only")
		close(published)
	}()
	select {
	case <-published:
	case <-time.After(2 * time.Second):
		close(cs.release)
		t.Fatal("PublishEvent did not return while the layer read was blocked")
	}
	// Release only once a subscriber has entered the read, so the deliveries
	// below go through the blocked read rather than racing ahead of the arm.
	for deadline := time.Now().Add(2 * time.Second); cs.reads.Load() == 0; {
		if time.Now().After(deadline) {
			close(cs.release)
			t.Fatal("no subscriber entered the layer read")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(cs.release)

	if got := nextEventData(t, "alice", alice)["id"]; got != "eng-only" {
		t.Errorf("alice event id = %v, want eng-only", got)
	}
	if got := nextEventData(t, "dave", dave)["id"]; got != "eng-only" {
		t.Errorf("dave event id = %v, want eng-only", got)
	}
	if n := cs.reads.Load(); n != 1 {
		t.Errorf("ListLayerConfigs calls = %d, want 1 shared read", n)
	}
}

// Spec: §7.6 — the stream filter does not apply to webhook receivers. A
// receiver an admin registered receives the groups-layer event that bob's
// stream withholds.
//
// Spec: §7.3.2
func TestEvents_WebhookReceiverIsUnfiltered(t *testing.T) {
	t.Parallel()
	bodies := make(chan []byte, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies <- body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)
	worker := &webhook.Worker{
		Store:      webhook.NewMemoryStore(),
		HTTPClient: receiver.Client(),
		Backoff:    []time.Duration{},
	}
	srv, ts, cs := newEventsRegistry(t, withEventsIdentities(),
		server.WithWebhooks(worker))
	if err := cs.GrantAdmin(context.Background(), store.AdminGrant{UserID: "admin", OrgID: "t"}); err != nil {
		t.Fatalf("GrantAdmin: %v", err)
	}
	registerEventsReceiver(t, ts.URL, receiver.URL)
	bob := openEventStream(t, ts.URL, "bob")

	publishArtifact(srv, engScope, "eng-only")
	publishArtifact(srv, pubScope, "everyone")

	if got := nextEventData(t, "bob", bob)["id"]; got != "everyone" {
		t.Errorf("bob first event id = %v, want everyone", got)
	}
	if !receiverGotID(bodies, "eng-only") {
		t.Error("the webhook receiver never received the groups-layer event")
	}
}

// registerEventsReceiver registers url as an artifact.published receiver,
// authenticated as the admin identity.
func registerEventsReceiver(t *testing.T, base, url string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"url": url, "secret": "s", "event_filter": []string{"artifact.published"},
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/webhooks", bytes.NewReader(body))
	req.Header.Set(eventsUserHeader, "admin")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/webhooks: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /v1/webhooks status = %d: %s", resp.StatusCode, out)
	}
}

// receiverGotID reports whether a delivery naming artifact id arrives on
// bodies within two seconds.
func receiverGotID(bodies <-chan []byte, id string) bool {
	deadline := time.After(2 * time.Second)
	for {
		select {
		case b := <-bodies:
			if bytes.Contains(b, []byte(`"id":"`+id+`"`)) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}
