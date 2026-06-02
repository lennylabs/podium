package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

func lcaWriteArtifact(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ARTIFACT.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
}

func lcaReingest(t *testing.T, base, id string) map[string]any {
	t.Helper()
	resp, err := http.Post(base+"/v1/layers/reingest?id="+id, "application/json", nil)
	if err != nil {
		t.Fatalf("reingest %s: %v", id, err)
	}
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode reingest %s: %v", id, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reingest %s status = %d: %v", id, resp.StatusCode, m)
	}
	return m
}

// Spec: §4.6 field-semantics table (F-4.6.3) — license is "Scalar; child wins
// (lint warning if changed across layers)". A runtime reingest of an extends
// child whose license differs from the resolved parent's surfaces the warning
// advisory in the §7.3.1 reingest result so the publisher sees the change.
// End-to-end through the SQLite store and the HTTP reingest endpoint.
func TestLicenseChangeAdvisory_ReingestSurfacesIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	// Lower-precedence parent (Apache-2.0) and higher-precedence overlay child
	// (MIT) at the same canonical ID, in separate local-source layers.
	parentDir := t.TempDir()
	lcaWriteArtifact(t, filepath.Join(parentDir, "finance/pay"),
		"---\ntype: context\nversion: 1.0.0\ndescription: parent\nsensitivity: low\nlicense: Apache-2.0\n---\n\nbody\n")
	childDir := t.TempDir()
	lcaWriteArtifact(t, filepath.Join(childDir, "finance/pay"),
		"---\ntype: context\nversion: 2.0.0\ndescription: child\nsensitivity: low\nlicense: MIT\nextends: finance/pay@1.x\n---\n\nbody\n")

	if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "org-defaults", SourceType: "local", LocalPath: parentDir, Order: 1}); err != nil {
		t.Fatalf("PutLayerConfig org-defaults: %v", err)
	}
	if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "team-foo", SourceType: "local", LocalPath: childDir, Order: 2}); err != nil {
		t.Fatalf("PutLayerConfig team-foo: %v", err)
	}

	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithReingestRunner(localReingestRunner(st, nil))
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	// Parent first so the child's extends pin resolves against it.
	if m := lcaReingest(t, ts.URL, "org-defaults"); m["accepted"] != float64(1) {
		t.Fatalf("org-defaults reingest accepted = %v, want 1", m["accepted"])
	}
	child := lcaReingest(t, ts.URL, "team-foo")
	if child["accepted"] != float64(1) {
		t.Fatalf("team-foo reingest accepted = %v, want 1 (license change is advisory, not blocking)", child["accepted"])
	}

	advisories, _ := child["advisories"].([]any)
	var found bool
	for _, raw := range advisories {
		a, _ := raw.(map[string]any)
		if a["code"] == "lint.license_changed_across_layers" {
			found = true
			if a["severity"] != "warning" {
				t.Errorf("advisory severity = %v, want warning", a["severity"])
			}
			if a["artifact_id"] != "finance/pay" {
				t.Errorf("advisory artifact_id = %v, want finance/pay", a["artifact_id"])
			}
		}
	}
	if !found {
		t.Errorf("reingest response missing lint.license_changed_across_layers advisory: %v", advisories)
	}
}
