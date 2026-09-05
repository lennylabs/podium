package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// layerEmissionHarness builds a layer endpoint wired to both an audit sink and
// a publish recorder, so one request's §8.1 record and its §7.5.4 wake are
// counted together.
func layerEmissionHarness(t *testing.T, sink *audit.FileSink, rec *eventRecorder, id layer.Identity) string {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAudit(sink).
		WithEventPublisher(rec.publish).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return id, nil })
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

// auditLines returns the records the sink has appended so far. The emission
// tests count them, because a membership assertion passes on a handler that
// emits unconditionally and on one that emits on what changed alike.
func auditLines(t *testing.T, sink *audit.FileSink) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(readAuditLog(t, sink), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// emissionCounter holds the audit and publish counts a request is measured
// against, so each arm reports what that one request added.
type emissionCounter struct {
	t     *testing.T
	sink  *audit.FileSink
	rec   *eventRecorder
	lines int
	pubs  int
}

func newEmissionCounter(t *testing.T, sink *audit.FileSink, rec *eventRecorder) *emissionCounter {
	t.Helper()
	return &emissionCounter{t: t, sink: sink, rec: rec, lines: len(auditLines(t, sink)), pubs: len(rec.typesFor())}
}

// added returns the audit records and the published event types the last
// request produced, and re-anchors the counter on the current totals.
func (c *emissionCounter) added() ([]string, []string) {
	c.t.Helper()
	lines := auditLines(c.t, c.sink)
	pubs := c.rec.typesFor()
	newLines := lines[c.lines:]
	newPubs := pubs[c.pubs:]
	c.lines, c.pubs = len(lines), len(pubs)
	return newLines, newPubs
}

// expect asserts the exact number of audit records and published events the
// last request produced, and returns the records for a content assertion.
func (c *emissionCounter) expect(name string, wantAudit, wantPublish int) []string {
	c.t.Helper()
	lines, pubs := c.added()
	if len(lines) != wantAudit {
		c.t.Errorf("%s recorded %d audit events, want %d:\n%s", name, len(lines), wantAudit, strings.Join(lines, "\n"))
	}
	if len(pubs) != wantPublish {
		c.t.Errorf("%s published %v, want %d event(s)", name, pubs, wantPublish)
	}
	return lines
}

func assertContainsAll(t *testing.T, name, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s record missing %s:\n%s", name, w, got)
		}
	}
}

// spec: §8.1 — "layer.config_changed | An admin-defined layer was added,
// removed, restored, or patched, or the tenant's layer order was changed."
// spec: §7.3.1 — an update that changes the stored record records its event
// and one that stores nothing records none, and the §7.5.4 wake follows a
// change that can alter what a profile resolves.
func TestLayerEndpoint_UpdateEmitsOnWhatChanged(t *testing.T) {
	t.Parallel()
	sink := newAuditSink(t)
	rec := &eventRecorder{}
	base := layerEmissionHarness(t, sink, rec, layer.Identity{Sub: "admin", IsAuthenticated: true})

	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "company", "source_type": "git", "repo": "git@github.com:acme/company.git",
		"ref": "main", "public": true, "groups": []string{"eng"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status %d: %s", resp.StatusCode, body)
	}
	counter := newEmissionCounter(t, sink, rec)

	t.Run("a patch restating the stored values records and publishes nothing", func(t *testing.T) {
		resp, body := putJSON(t, base, "/v1/layers/update?id=company", map[string]any{
			"public": true, "organization": false, "groups": []string{"eng"}, "ref": "main",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
		}
		counter.expect("a patch that stored nothing", 0, 0)
	})

	t.Run("a visibility withdrawal records the event and wakes the watchers", func(t *testing.T) {
		resp, body := putJSON(t, base, "/v1/layers/update?id=company", map[string]any{"public": false})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
		}
		lines := counter.expect("a visibility withdrawal", 1, 1)
		if len(lines) == 1 {
			assertContainsAll(t, "the withdrawal", lines[0],
				`"type":"layer.config_changed"`, `"target":"company"`, `"action":"update"`)
		}
	})

	t.Run("a rotation records the event and wakes no watcher", func(t *testing.T) {
		resp, body := putJSON(t, base, "/v1/layers/update?id=company", map[string]any{
			"rotate_webhook_secret": true,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
		}
		lines := counter.expect("a rotation-only patch", 1, 0)
		if len(lines) == 1 {
			assertContainsAll(t, "the rotation", lines[0],
				`"type":"layer.config_changed"`, `"action":"update"`)
		}
	})

	t.Run("a force-push policy change records the event and wakes no watcher", func(t *testing.T) {
		resp, body := putJSON(t, base, "/v1/layers/update?id=company", map[string]any{
			"force_push_policy": "strict",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
		}
		counter.expect("a force_push_policy change", 1, 0)
	})
}

// spec: §8.1 — "layer.user_registered | A personal layer was registered,
// unregistered, patched, restored, or erased." A patch that changes a member
// the class admits records one such event and publishes nothing; a
// visibility body restating what the class stores records none.
func TestLayerEndpoint_UpdateEmitsOnWhatChangedUserDefined(t *testing.T) {
	t.Parallel()
	sink := newAuditSink(t)
	rec := &eventRecorder{}
	base := layerEmissionHarness(t, sink, rec, layer.Identity{Sub: "alice", IsAuthenticated: true})

	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "alice-personal", "source_type": "git", "repo": "git@github.com:alice/notes.git",
		"ref": "main", "user_defined": true, "owner": "alice",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status %d: %s", resp.StatusCode, body)
	}
	counter := newEmissionCounter(t, sink, rec)

	t.Run("a ref change records one personal-layer event and publishes nothing", func(t *testing.T) {
		resp, body := putJSON(t, base, "/v1/layers/update?id=alice-personal", map[string]any{"ref": "release"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
		}
		lines := counter.expect("a ref change on a personal layer", 1, 0)
		if len(lines) == 1 {
			assertContainsAll(t, "the ref change", lines[0],
				`"type":"layer.user_registered"`, `"target":"alice-personal"`,
				`"owner":"alice"`, `"action":"update"`)
			if strings.Contains(lines[0], "layer.config_changed") {
				t.Errorf("a personal layer recorded layer.config_changed:\n%s", lines[0])
			}
		}
	})

	t.Run("a visibility body the class stores at its zero value records nothing", func(t *testing.T) {
		resp, body := putJSON(t, base, "/v1/layers/update?id=alice-personal", map[string]any{"public": false})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
		}
		counter.expect("a public:false patch on a personal layer", 0, 0)
	})
}

// spec: §8.1, §7.3.1 — a reorder records one layer.config_changed naming the
// reordered identifiers where the resulting precedence sequence differs from
// the one the tenant held, and records none where the two are the same. The
// fixture's stored order values are 20 and 30 rather than the 10 and 20 the
// handler renumbers to, so a comparison over the stored integers would report
// a change on the identity reorder.
func TestLayerEndpoint_ReorderEmitsOnPrecedenceChange(t *testing.T) {
	t.Parallel()
	sink := newAuditSink(t)
	rec := &eventRecorder{}
	base := layerEmissionHarness(t, sink, rec, layer.Identity{Sub: "admin", IsAuthenticated: true})

	for _, id := range []string{"a", "b", "c"} {
		resp, body := mustPost(t, base, "/v1/layers", map[string]any{
			"id": id, "source_type": "local", "local_path": "/tmp/" + id,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("register %s status %d: %s", id, resp.StatusCode, body)
		}
	}
	// Unregistering the head leaves b at 20 and c at 30, which the reorder
	// renumbers to 10 and 20 without changing the sequence.
	mustDelete(t, base, "/v1/layers?id=a")
	counter := newEmissionCounter(t, sink, rec)

	t.Run("a reorder producing the sequence the tenant held records nothing", func(t *testing.T) {
		resp, body := mustPost(t, base, "/v1/layers/reorder", map[string]any{"order": []string{"b", "c"}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("reorder status %d: %s", resp.StatusCode, body)
		}
		counter.expect("an identity reorder", 0, 0)
	})

	t.Run("a reorder producing a different sequence records one event", func(t *testing.T) {
		resp, body := mustPost(t, base, "/v1/layers/reorder", map[string]any{"order": []string{"c", "b"}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("reorder status %d: %s", resp.StatusCode, body)
		}
		lines := counter.expect("a reordering reorder", 1, 1)
		if len(lines) == 1 {
			assertContainsAll(t, "the reorder", lines[0],
				`"type":"layer.config_changed"`, `"target":"c,b"`, `"action":"reorder"`)
		}
	})

	t.Run("an empty order records nothing", func(t *testing.T) {
		resp, body := mustPost(t, base, "/v1/layers/reorder", map[string]any{"order": []string{}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("reorder status %d: %s", resp.StatusCode, body)
		}
		counter.expect("an empty reorder", 0, 0)
	})
}

// spec: §8.1, §7.3.1 — a reorder records one layer.config_changed on the
// comma-joined identifiers whatever the class of the layers it names, because
// the precedence sequence is a property of the tenant's layer list. The
// §7.3.1 layer-write authorization rule admits a user-defined layer's stored
// owner, so a reorder naming one is reachable.
func TestLayerEndpoint_ReorderNamingAUserDefinedLayerRecordsConfigChanged(t *testing.T) {
	t.Parallel()
	sink := newAuditSink(t)
	rec := &eventRecorder{}
	base := layerEmissionHarness(t, sink, rec, layer.Identity{Sub: "alice", IsAuthenticated: true})

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team", "source_type": "local", "local_path": "/tmp/team",
	})
	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "alice-personal", "source_type": "local", "local_path": "/tmp/alice",
		"user_defined": true, "owner": "alice",
	})
	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "later", "source_type": "local", "local_path": "/tmp/later",
	})
	// Removing the head leaves the remaining layers at 20 and 30.
	mustDelete(t, base, "/v1/layers?id=team")
	counter := newEmissionCounter(t, sink, rec)

	resp, body := mustPost(t, base, "/v1/layers/reorder", map[string]any{
		"order": []string{"later", "alice-personal"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reorder status %d: %s", resp.StatusCode, body)
	}
	lines := counter.expect("a reorder naming a user-defined layer", 1, 1)
	if len(lines) == 1 {
		assertContainsAll(t, "the reorder", lines[0],
			`"type":"layer.config_changed"`, `"target":"later,alice-personal"`, `"action":"reorder"`)
		if strings.Contains(lines[0], "layer.user_registered") {
			t.Errorf("a reorder resolved the event type from a layer's class:\n%s", lines[0])
		}
	}
}
