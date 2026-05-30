package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// scopePreview is the §3.5 wire response.
type scopePreview struct {
	Layers        []string       `json:"layers"`
	ArtifactCount int            `json:"artifact_count"`
	ByType        map[string]int `json:"by_type"`
	BySensitivity map[string]int `json:"by_sensitivity"`
}

// Spec: §3.5 (F-3.5.5, F-3.5.6, F-3.5.8) — the scope preview over the
// SQLite metadata store and the HTTP endpoint: counts are per distinct
// artifact (a multi-version artifact counts once, its sensitivity from the
// §4.7.6 latest version), `layers` is the precedence-ordered composition
// including a visible-but-empty layer, and an unset sensitivity floors to
// the documented `low` bucket. Mirrors the standalone deployment in process.
func TestScopePreview_SQLiteAggregation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// "multi" has two versions in layer mid; latest (v2, high) drives the
	// sensitivity tally. "solo" omits sensitivity (floors to low).
	records := []store.ManifestRecord{
		{TenantID: "default", ArtifactID: "finance/multi", Version: "1.0.0", ContentHash: "sha256:m1", Type: "skill", Sensitivity: "low", Layer: "mid", IngestedAt: base},
		{TenantID: "default", ArtifactID: "finance/multi", Version: "2.0.0", ContentHash: "sha256:m2", Type: "skill", Sensitivity: "high", Layer: "mid", IngestedAt: base.Add(time.Hour)},
		{TenantID: "default", ArtifactID: "finance/solo", Version: "1.0.0", ContentHash: "sha256:s1", Type: "agent", Layer: "high", IngestedAt: base},
	}
	for _, r := range records {
		if err := st.PutManifest(ctx, r); err != nil {
			t.Fatalf("PutManifest %s@%s: %v", r.ArtifactID, r.Version, err)
		}
	}

	// Layers supplied out of precedence order; "low" is visible but empty.
	reg := core.New(st, "default", []layer.Layer{
		{ID: "high", Precedence: 3, Visibility: layer.Visibility{Public: true}},
		{ID: "low", Precedence: 1, Visibility: layer.Visibility{Public: true}},
		{ID: "mid", Precedence: 2, Visibility: layer.Visibility{Public: true}},
	})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/v1/scope/preview")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}
	var p scopePreview
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if p.ArtifactCount != 2 {
		t.Errorf("artifact_count = %d, want 2 (distinct artifacts, not 3 versions)", p.ArtifactCount)
	}
	if p.ByType["skill"] != 1 || p.ByType["agent"] != 1 {
		t.Errorf("by_type = %v, want skill:1 agent:1", p.ByType)
	}
	if _, ok := p.BySensitivity[""]; ok {
		t.Errorf("by_sensitivity carries an empty-string bucket: %v", p.BySensitivity)
	}
	if p.BySensitivity["high"] != 1 {
		t.Errorf("by_sensitivity[high] = %d, want 1 (multi latest version)", p.BySensitivity["high"])
	}
	if p.BySensitivity["low"] != 1 {
		t.Errorf("by_sensitivity[low] = %d, want 1 (solo unset → low)", p.BySensitivity["low"])
	}
	if p.BySensitivity["medium"] != 0 {
		t.Errorf("by_sensitivity[medium] = %d, want 0 (intermediate version must not count)", p.BySensitivity["medium"])
	}
	if want := []string{"low", "mid", "high"}; !reflect.DeepEqual(p.Layers, want) {
		t.Errorf("layers = %v, want %v (precedence order, empty 'low' included)", p.Layers, want)
	}
}

// Spec: §3.5 (F-3.5.1) — the expose_scope_preview tenant gate survives the
// SQLite column round-trip: a tenant persisted with the flag false makes
// GET /v1/scope/preview answer 403 scope_preview_disabled.
func TestScopePreview_SQLiteTenantGateDisabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	off := false
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default", ExposeScopePreview: &off}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID: "default", ArtifactID: "finance/x", Version: "1.0.0",
		ContentHash: "sha256:x", Type: "skill", Layer: "L",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	reg := core.New(st, "default", []layer.Layer{{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}}})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/v1/scope/preview")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 403: %s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, body)
	}
	if env.Code != "scope_preview_disabled" {
		t.Errorf("error code = %q, want scope_preview_disabled", env.Code)
	}
}
