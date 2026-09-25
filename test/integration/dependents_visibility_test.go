package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// dvUserHeader names the identity of each request in the dependents
// visibility fixture.
const dvUserHeader = "X-Test-User"

// Spec: §4.7.3 visibility — GET /v1/dependents against a target the caller
// cannot see answers 200 with an empty edge list, byte-identical to a
// target that does not exist, with no §6.10 envelope and no
// visibility.denied audit event, because the query is filtered rather than
// refused.
func TestDependents_InvisibleTargetReturnsEmpty200(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	rlvLayer(t, st, store.LayerConfig{TenantID: "default", ID: "hidden", Order: 1, Users: []string{"alice"}})
	rlvLayer(t, st, store.LayerConfig{TenantID: "default", ID: "team", Order: 2, Users: []string{"alice", "bob"}})
	rlvManifest(t, st, store.ManifestRecord{
		TenantID: "default", ArtifactID: "acme/hidden-parent", Version: "1.0.0", Layer: "hidden", Type: "context",
	})
	rlvManifest(t, st, store.ManifestRecord{
		TenantID: "default", ArtifactID: "acme/child", Version: "1.0.0", Layer: "team", Type: "context",
		ExtendsPin: "acme/hidden-parent@1.0.0",
	})
	if err := st.PutDependency(ctx, "default", store.DependencyEdge{
		From: "acme/child", To: "acme/hidden-parent", Kind: "extends",
	}); err != nil {
		t.Fatalf("PutDependency: %v", err)
	}

	var mu sync.Mutex
	var events []string
	reg := core.New(st, "default", nil).WithAudit(func(_ context.Context, e core.AuditEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e.Type)
	})
	srv := server.New(reg, server.WithIdentityResolver(func(r *http.Request) layer.Identity {
		sub := r.Header.Get(dvUserHeader)
		return layer.Identity{Sub: sub, IsAuthenticated: sub != ""}
	}))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// get returns the status, the raw body, and the body compacted so the
	// assertion is independent of the server's indentation.
	get := func(user, id string) (int, string, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/dependents?id="+id, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set(dvUserHeader, user)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET dependents %s: %v", id, err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read dependents %s: %v", id, err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			t.Fatalf("dependents %s: body is not JSON: %v (%s)", id, err, raw)
		}
		return resp.StatusCode, string(raw), compact.String()
	}

	status, invisible, compact := get("bob", "acme/hidden-parent")
	if status != http.StatusOK || compact != `{"edges":[]}` {
		t.Errorf("invisible target = %d %s, want 200 {\"edges\":[]}", status, invisible)
	}
	if strings.Contains(invisible, `"code"`) {
		t.Errorf("invisible target carries an error envelope: %s", invisible)
	}
	if _, missing, _ := get("bob", "acme/does-not-exist"); missing != invisible {
		t.Errorf("nonexistent target = %s, want byte-identical to invisible %s", missing, invisible)
	}
	mu.Lock()
	for _, e := range events {
		if e == "visibility.denied" {
			t.Errorf("filtered dependents query emitted visibility.denied: %v", events)
		}
	}
	mu.Unlock()

	// alice reads both layers and sees the edge, so the fixture is live.
	if status, _, body := get("alice", "acme/hidden-parent"); status != http.StatusOK || !strings.Contains(body, `"from":"acme/child"`) {
		t.Errorf("alice = %d %s, want the acme/child edge", status, body)
	}
}
