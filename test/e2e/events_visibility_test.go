package e2e

// End-to-end coverage of the §7.6 change-event stream visibility rule on the
// compiled binary. Each stream is opened through the trusted-headers identity
// provider, so the identity and the tenant the registry resolves when the
// stream opens are the ones a gateway asserted, and each event reaches only
// the subscribers whose §4.6 view admits the layer it names.
//
// The anonymous streams on a no-identity registry are the §4.6 no-identity
// bypass regression guards: TestLayerUpdate_RotationWakesNoWatcher
// (layer_visibility_narrowing_test.go) and the streams in
// notification_sink_primitive_test.go open a stream with no identity and
// expect every event of the tenant. If they start failing, the bypass is
// broken, and they are not to be adjusted to pass.
//
// The reorder rewrite and the unfiltered webhook receiver are pinned
// in-process by pkg/registry/server/events_test.go.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// evBound is how long a stream waits for an event the registry must deliver.
// evWindow is how long a stream watches for an event the registry must
// withhold. The window is at least as long as the bound, so a withheld event
// that would have arrived as late as an admitted one is still observed.
const (
	evBound  = 15 * time.Second
	evWindow = evBound
)

// evHeaders builds a trusted-headers identity for a request or a stream.
// An empty sub yields no identity headers, which is the anonymous caller.
func evHeaders(sub, groups string) http.Header {
	h := http.Header{}
	if sub != "" {
		h.Set("X-Podium-User-Sub", sub)
	}
	if groups != "" {
		h.Set("X-Podium-User-Groups", groups)
	}
	return h
}

// evNamesLayer reports whether the event's data names layerID. The reorder
// event's `layer` value joins the reordered layers with commas.
func evNamesLayer(ev registryEventLine, layerID string) bool {
	v, _ := ev.Data["layer"].(string)
	for _, id := range strings.Split(v, ",") {
		if id == layerID {
			return true
		}
	}
	return false
}

// evWaitForLayer reads events until one of eventType (any type when empty)
// names one of layerIDs, or the deadline elapses. It reports the match and
// whether one was seen, and never fails the test, so a caller can assert
// either outcome.
func evWaitForLayer(c *eventStreamClient, eventType string, within time.Duration, layerIDs ...string) (registryEventLine, bool) {
	deadline := time.After(within)
	for {
		select {
		case ev, ok := <-c.events:
			if !ok {
				return registryEventLine{}, false
			}
			if eventType != "" && ev.Event != eventType {
				continue
			}
			for _, id := range layerIDs {
				if evNamesLayer(ev, id) {
					return ev, true
				}
			}
		case <-deadline:
			return registryEventLine{}, false
		}
	}
}

// evWantNext reads the next event on c and fails the test unless it has the
// given type and names layerID. Reading the next event, rather than waiting
// for a match, is what proves a withheld event left no trace ahead of it.
func evWantNext(t *testing.T, who string, c *eventStreamClient, eventType, layerID string) {
	t.Helper()
	ev := c.next(t, evBound)
	if ev.Event != eventType || !evNamesLayer(ev, layerID) {
		t.Fatalf("%s: next event is %q naming %v, want %q naming %s", who, ev.Event, ev.Data["layer"], eventType, layerID)
	}
}

// evReingest triggers a reingest of layerID as the caller headers name and
// fails the test unless the registry accepts it.
func evReingest(t *testing.T, srv *serverProc, as http.Header, layerID string) {
	t.Helper()
	st, body := apiDoAs(t, http.MethodPost, srv.BaseURL+"/v1/layers/reingest?id="+layerID, as, nil)
	apiWantStatus(t, st, 200, "reingest "+layerID, body)
}

// evUpdateUsers patches the users list of layerID as the caller headers name
// and fails the test unless the registry accepts it.
func evUpdateUsers(t *testing.T, srv *serverProc, as http.Header, layerID string, users []string) {
	t.Helper()
	st, body := apiDoAs(t, http.MethodPut, srv.BaseURL+"/v1/layers/update?id="+layerID, as,
		map[string]any{"users": users})
	apiWantStatus(t, st, 200, "update users on "+layerID, body)
}

// evStandaloneServer boots the trusted-headers standalone fixture with carol
// as the bootstrap admin, which the layer writes below require, and returns
// the eng-layer source root.
func evStandaloneServer(t *testing.T) (*serverProc, string) {
	t.Helper()
	return gwTrustedHeadersRegistry(t, "PODIUM_BOOTSTRAP_ADMINS=carol@acme.com")
}

// Spec: §7.6 — the stream delivers an event only to a subscriber whose §4.6
// view admits the layer the event names. alice, in the engineering group,
// receives the eng-layer artifact.published and layer.ingested. bob and the
// anonymous caller receive neither, so the first event on their streams is the
// public-layer layer.ingested published after them.
func TestEventStream_LayerVisibilityPerCaller(t *testing.T) {
	t.Parallel()
	srv, engRoot := evStandaloneServer(t)
	carol := evHeaders("carol@acme.com", "")

	alice := openEventStreamAs(t, srv, evHeaders("alice@acme.com", "engineering"))
	bob := openEventStreamAs(t, srv, evHeaders("bob@acme.com", ""))
	anon := openEventStreamAs(t, srv, nil)

	if err := os.MkdirAll(filepath.Join(engRoot, "roadmap"), 0o755); err != nil {
		t.Fatalf("mkdir roadmap: %v", err)
	}
	if err := os.WriteFile(filepath.Join(engRoot, "roadmap", "ARTIFACT.md"),
		[]byte(contextArtifact("engineering roadmap")), 0o644); err != nil {
		t.Fatalf("write roadmap artifact: %v", err)
	}
	evReingest(t, srv, carol, "eng-layer")

	pub, ok := evWaitForLayer(alice, "artifact.published", evBound, "eng-layer")
	if !ok {
		t.Fatalf("alice received no eng-layer artifact.published within %s\nlog:\n%s", evBound, srv.log())
	}
	if pub.Data["id"] != "roadmap" {
		t.Errorf("alice's artifact.published names %v, want roadmap", pub.Data["id"])
	}
	if _, ok := evWaitForLayer(alice, "layer.ingested", evBound, "eng-layer"); !ok {
		t.Fatalf("alice received no eng-layer layer.ingested within %s", evBound)
	}

	evReingest(t, srv, carol, "public-layer")
	evWantNext(t, "bob", bob, "layer.ingested", "public-layer")
	evWantNext(t, "anonymous", anon, "layer.ingested", "public-layer")
}

// Spec: §7.6 — the stream evaluates each event at delivery against the stored
// layer visibility, so a grant made while bob's stream is open admits the
// events that follow it.
// Spec: §7.5.4 — the withdrawing layer.config_changed still reaches the
// subscriber who could see the layer before the change, through the
// prior-visibility arm, and the layer's later events do not.
func TestEventStream_VisibilityChangeMidStream(t *testing.T) {
	t.Parallel()
	srv, _ := evStandaloneServer(t)
	carol := evHeaders("carol@acme.com", "")
	bob := openEventStreamAs(t, srv, evHeaders("bob@acme.com", ""))

	evUpdateUsers(t, srv, carol, "eng-layer", []string{"bob@acme.com"})
	evWantNext(t, "bob after the grant", bob, "layer.config_changed", "eng-layer")
	evReingest(t, srv, carol, "eng-layer")
	evWantNext(t, "bob after the granted reingest", bob, "layer.ingested", "eng-layer")

	evUpdateUsers(t, srv, carol, "eng-layer", []string{})
	evWantNext(t, "bob after the withdrawal", bob, "layer.config_changed", "eng-layer")
	evReingest(t, srv, carol, "eng-layer")
	evReingest(t, srv, carol, "public-layer")
	evWantNext(t, "bob after the withdrawn reingest", bob, "layer.ingested", "public-layer")
}

// Spec: §7.6 — the stream delivers an event only to a subscriber routed to the
// event's tenant.
// Spec: §6.3.1 — a multi-tenant trusted-headers registry routes the stream by
// the X-Podium-User-Org the gateway asserts, and leaves an unprovisioned org
// and an absent org on the unrouted tenant.
// Spec: §4.7.1 — the org name `default` resolves to the bootstrap tenant's ID.
//
// IMPLEMENTOR'S CHOICE (proposal 0042, TEST-3 case 3): the bootstrap-tenant
// event is the layer.config_changed of a reorder of two user-defined layers
// the per-run subject registers and owns. Registering a user-defined layer and
// reordering it pass on the §7.3.1 owner arm, so the case needs no admin layer
// write, which a multi-tenant registry refuses. The layers name a network git
// repository under the reserved .invalid domain, so no operation resolves to
// the file transport and no fetch is attempted. Each request's status is
// asserted before any stream is read. The subject and the layer IDs carry a
// per-run suffix, which keeps the per-owner layer cap and the shared database
// from colliding across runs. The case needs no external tool. All three
// streams use that one subject, whose users:[<registrant>] visibility admits
// both layers, so only the tenant condition separates them.
func TestEventStream_MultiTenantRouting(t *testing.T) {
	t.Parallel()
	dsn, bucket, region := msSkipIfNoStack(t)
	_, pemPath := injKeyPair(t)
	secret := "ev-proxy-" + randHex(8)
	srv := msStartStandardServerEnv(t, dsn, bucket, region, pemPath,
		"PODIUM_IDENTITY_PROVIDER=trusted-headers",
		"PODIUM_MULTI_TENANT=true",
		"PODIUM_TRUSTED_PROXY_SECRET="+secret,
		"PODIUM_BOOTSTRAP_ADMINS=",
	)

	suffix := randHex(6)
	sub := "carol-" + suffix + "@acme.com"
	as := func(org string) http.Header {
		h := evHeaders(sub, "")
		h.Set("X-Podium-Proxy-Secret", secret)
		if org != "" {
			h.Set("X-Podium-User-Org", org)
		}
		return h
	}
	first, second := "ev-mt-a-"+suffix, "ev-mt-b-"+suffix
	for _, id := range []string{first, second} {
		evRegisterUserLayer(t, srv, as("default"), id)
	}

	routed := openEventStreamAs(t, srv, as("default"))
	unprovisioned := openEventStreamAs(t, srv, as("globex"))
	noOrg := openEventStreamAs(t, srv, as(""))

	st, body := apiDoAs(t, http.MethodPost, srv.BaseURL+"/v1/layers/reorder", as("default"),
		map[string]any{"order": []string{second, first}})
	apiWantStatus(t, st, 200, "reorder the per-run layers", body)

	if _, ok := evWaitForLayer(routed, "layer.config_changed", evBound, first, second); !ok {
		t.Fatalf("the stream routed to the bootstrap tenant received no reorder event within %s\nlog:\n%s", evBound, srv.log())
	}

	// The unrouted streams watch concurrently over one window, which starts
	// after the routed stream has received the event.
	var wg sync.WaitGroup
	for name, c := range map[string]*eventStreamClient{"globex": unprovisioned, "no org": noOrg} {
		wg.Add(1)
		go func(name string, c *eventStreamClient) {
			defer wg.Done()
			if ev, ok := evWaitForLayer(c, "", evWindow, first, second); ok {
				t.Errorf("the %s stream received %q naming %v; an unrouted subscriber must receive no event of the bootstrap tenant",
					name, ev.Event, ev.Data["layer"])
			}
		}(name, c)
	}
	wg.Wait()
}

// evRegisterUserLayer registers a user-defined git layer as the caller the
// headers name, fails the test unless the registry stores it as that caller's
// user-defined layer, and unregisters it when the test ends so the shared
// database keeps no per-run layer. The body asserts user_defined so a caller
// who holds the tenant admin grant also registers a personal layer, whose
// registration publishes no layer.config_changed (§7.5.4).
func evRegisterUserLayer(t *testing.T, srv *serverProc, as http.Header, id string) {
	t.Helper()
	st, body := apiDoAs(t, http.MethodPost, srv.BaseURL+"/v1/layers", as, map[string]any{
		"id":           id,
		"source_type":  "git",
		"repo":         "https://git.invalid/acme/" + id + ".git",
		"ref":          "main",
		"user_defined": true,
	})
	apiWantStatus(t, st, 201, "register user-defined layer "+id, body)
	var resp struct {
		Layer struct {
			UserDefined bool `json:"user_defined"`
		} `json:"layer"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || !resp.Layer.UserDefined {
		t.Fatalf("register %s did not store a user-defined layer (decode error %v)\nbody: %s", id, err, body)
	}
	t.Cleanup(func() {
		if st, body := apiDoAs(t, http.MethodDelete, srv.BaseURL+"/v1/layers?id="+id, as, nil); st != 200 && st != 204 {
			t.Logf("unregister %s: HTTP %d: %s", id, st, body)
		}
	})
}
