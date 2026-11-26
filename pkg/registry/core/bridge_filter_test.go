package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// newBridgeFixture is the usage fixture with two mcp-server artifacts in the
// team layer, ops/db and ops/cache, whose descriptions match the query
// "session" like the fixture's skills.
func newBridgeFixture(t *testing.T) *usageFixture {
	t.Helper()
	f := newUsageFixture(t)
	for _, m := range []store.ManifestRecord{
		{TenantID: "t", ArtifactID: "ops/db", Version: "1.0.0", ContentHash: "sha256:m1", Type: "mcp-server", Name: "db", Description: "session database server", Layer: "team"},
		{TenantID: "t", ArtifactID: "ops/cache", Version: "1.0.0", ContentHash: "sha256:m2", Type: "mcp-server", Name: "cache", Description: "session cache server", Layer: "team"},
	} {
		if err := f.st.PutManifest(context.Background(), storetest.Seal(t, m, nil, nil)); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// eventsOf returns the fixture's audit events of type typ, in order.
func (f *usageFixture) eventsOf(typ string) []core.AuditEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []core.AuditEvent
	for _, e := range f.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// descriptorIDs returns the IDs of ds.
func descriptorIDs(ds []core.ArtifactDescriptor) map[string]bool {
	ids := map[string]bool{}
	for _, d := range ds {
		ids[d.ID] = true
	}
	return ids
}

// Spec: §5, §7.2.2, §8.1, §8.7 — a search whose client is the MCP bridge
// matches no mcp-server artifact: the page fills from the other matches,
// total_matched counts them alone, and the artifacts.searched results context
// and the search hits name only them. An SDK search with the same options
// returns the mcp-server artifacts.
func TestBridgeFilter_Search(t *testing.T) {
	t.Parallel()
	f := newBridgeFixture(t)
	opts := core.SearchArtifactsOptions{Query: "session", TopK: 2}
	bridge, err := f.reg.SearchArtifacts(clientCtx("mcp", ""), usageAlice, opts)
	if err != nil {
		t.Fatal(err)
	}
	ids := descriptorIDs(bridge.Results)
	if len(bridge.Results) != 2 || !ids["ops/greet"] || !ids["ops/farewell"] || bridge.TotalMatched != 2 {
		t.Errorf("bridge search = %+v (total %d), want ops/greet and ops/farewell, total 2", bridge.Results, bridge.TotalMatched)
	}
	byType, err := f.reg.SearchArtifacts(clientCtx("mcp", ""), usageAlice, core.SearchArtifactsOptions{Type: "mcp-server"})
	if err != nil || len(byType.Results) != 0 || byType.TotalMatched != 0 {
		t.Errorf("bridge search for type mcp-server = %+v, %v; want no results and total 0", byType, err)
	}
	sdk, err := f.reg.SearchArtifacts(clientCtx("sdk", ""), usageAlice, core.SearchArtifactsOptions{Query: "session", TopK: 10})
	if err != nil {
		t.Fatal(err)
	}
	if ids := descriptorIDs(sdk.Results); !ids["ops/db"] || !ids["ops/cache"] || sdk.TotalMatched != 4 {
		t.Errorf("sdk search = %+v (total %d), want the mcp-server artifacts and total 4", sdk.Results, sdk.TotalMatched)
	}

	searched := f.eventsOf("artifacts.searched")
	var refs []struct {
		ArtifactID string `json:"artifact_id"`
	}
	if err := json.Unmarshal([]byte(searched[0].Context["results"]), &refs); err != nil || len(refs) != 2 {
		t.Fatalf("bridge results context = %q (%v), want 2 entries", searched[0].Context["results"], err)
	}
	for _, r := range refs {
		if r.ArtifactID == "ops/db" || r.ArtifactID == "ops/cache" {
			t.Errorf("bridge results context names %s", r.ArtifactID)
		}
	}
	for _, row := range f.rows(t) {
		if row.Client == "mcp" && (row.ArtifactID == "ops/db" || row.ArtifactID == "ops/cache") {
			t.Errorf("bridge search counted a hit for %s: %+v", row.ArtifactID, row)
		}
	}
}

// Spec: §5, §7.2.2 — the MCP bridge's load_domain notable list names no
// mcp-server artifact; an SDK caller's names them.
func TestBridgeFilter_LoadDomain(t *testing.T) {
	t.Parallel()
	f := newBridgeFixture(t)
	bridge, err := f.reg.LoadDomain(clientCtx("mcp", ""), usageAlice, "ops", core.LoadDomainOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ids := descriptorIDs(bridge.Notable); ids["ops/db"] || ids["ops/cache"] || !ids["ops/greet"] {
		t.Errorf("bridge notable = %+v, want ops/greet and no mcp-server", bridge.Notable)
	}
	sdk, err := f.reg.LoadDomain(clientCtx("sdk", ""), usageAlice, "ops", core.LoadDomainOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ids := descriptorIDs(sdk.Notable); !ids["ops/db"] {
		t.Errorf("sdk notable = %+v, want ops/db", sdk.Notable)
	}
}

// Spec: §5, §7.2.2, §8.1, §8.7 — the MCP bridge's load of an mcp-server
// artifact fails as a load of an ID outside the caller's view: the same
// registry.not_found error and message, a read event of result size 0, no
// visibility.denied event, and no load count. An SDK caller loads it.
func TestBridgeFilter_LoadArtifact(t *testing.T) {
	t.Parallel()
	f := newBridgeFixture(t)
	_, hiddenErr := f.reg.LoadArtifact(clientCtx("mcp", ""), usageAlice, "ops/db", core.LoadArtifactOptions{})
	_, missingErr := f.reg.LoadArtifact(clientCtx("mcp", ""), usageAlice, "ops/nope", core.LoadArtifactOptions{})
	if !errors.Is(hiddenErr, core.ErrNotFound) || hiddenErr.Error() != "registry.not_found: artifact ops/db" {
		t.Errorf("bridge load of ops/db = %v, want registry.not_found: artifact ops/db", hiddenErr)
	}
	if missingErr == nil || missingErr.Error() != "registry.not_found: artifact ops/nope" {
		t.Errorf("bridge load of a missing ID = %v, want the same message format", missingErr)
	}
	if res, err := f.reg.LoadArtifact(clientCtx("sdk", ""), usageAlice, "ops/db", core.LoadArtifactOptions{}); err != nil || res.ID != "ops/db" {
		t.Errorf("sdk load of ops/db = %+v, %v; want the artifact", res, err)
	}

	loaded := f.eventsOf("artifact.loaded")
	if len(loaded) != 3 || loaded[0].Target != "ops/db" || loaded[0].ResultSize != 0 || loaded[2].ResultSize != 1 {
		t.Errorf("artifact.loaded events = %+v, want ops/db at size 0, the missing ID at 0, and the sdk load at 1", loaded)
	}
	if denied := f.eventsOf("visibility.denied"); len(denied) != 0 {
		t.Errorf("visibility.denied events = %+v, want none", denied)
	}
	for _, row := range f.rows(t) {
		if row.Client == "mcp" && row.Loads != 0 {
			t.Errorf("bridge load counted: %+v", row)
		}
	}
}
