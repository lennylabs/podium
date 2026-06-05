package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// gitReingestRunner wires the §7.3.1 ingest pipeline against the built-in git
// source provider, mirroring what serverboot installs for a git-source layer.
func gitReingestRunner(st store.Store) server.ReingestRunner {
	return func(ctx context.Context, lc store.LayerConfig, bg *server.BreakGlass) (*ingest.Result, error) {
		return ingest.SourceIngestWithOptions(ctx, st, source.Git{}, lc, ingest.SourceIngestOptions{})
	}
}

// gitLayerRepo builds a tiny git repo containing one artifact and returns the
// file:// URL the Git provider clones.
func gitLayerRepo(t *testing.T, desc string) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, _ := repo.Worktree()
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "---\ntype: context\nversion: 1.0.0\ndescription: " + desc + "\nsensitivity: low\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "alpha", "ARTIFACT.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if _, err := wt.Add("alpha/ARTIFACT.md"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := wt.Commit("init", &git.CommitOptions{
		Author: &object.Signature{Name: "T", Email: "t@x", When: time.Now()},
	}); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	return "file://" + dir
}

// Spec: §7.3.1 / §14.10 — a manual reingest of a git-source layer
// clones the repo and ingests its artifacts. §14.10 registers a public Git
// repo as a layer on a developer machine without a public ingress and pulls it
// with `podium layer reingest`; the endpoint must resolve the git source
// provider and run the pipeline, not merely record intent.
func TestReingestPipeline_GitSource(t *testing.T) {
	t.Parallel()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	repoURL := gitLayerRepo(t, "community skill")
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "community-skills", SourceType: "git", Repo: repoURL, Ref: "master",
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}

	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithReingestRunner(gitReingestRunner(st))
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/v1/layers/reingest?id=community-skills", "application/json", nil)
	if err != nil {
		t.Fatalf("reingest: %v", err)
	}
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || m["accepted"] != float64(1) {
		t.Fatalf("git reingest status=%d body=%v (want accepted=1)", resp.StatusCode, m)
	}
	got, err := st.GetLayerConfig(ctx, "t", "community-skills")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if got.LastIngestedAt == nil {
		t.Errorf("last_ingested_at not stamped after git reingest")
	}
}

// localReingestRunner wires the §7.3.1 ingest pipeline against the built-in
// local source provider with the supplied freeze windows, mirroring what
// serverboot installs.
func localReingestRunner(st store.Store, windows []ingest.FreezeWindow) server.ReingestRunner {
	return func(ctx context.Context, lc store.LayerConfig, bg *server.BreakGlass) (*ingest.Result, error) {
		fw := windows
		if bg != nil && len(windows) > 0 {
			fw = make([]ingest.FreezeWindow, len(windows))
			for i, w := range windows {
				w.BreakGlass = true
				w.Justification = bg.Justification
				w.Approvers = bg.Approvers
				w.GrantedAt = time.Now().UTC()
				fw[i] = w
			}
		}
		return ingest.SourceIngestWithOptions(ctx, st, source.Local{}, lc, ingest.SourceIngestOptions{
			FreezeWindows: fw,
		})
	}
}

func writeArtifact(t *testing.T, dir, desc string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "---\ntype: context\nversion: 1.0.0\ndescription: " + desc + "\nsensitivity: low\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "ARTIFACT.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
}

// Spec: §7.3.1 — a manual reingest over HTTP runs the
// pipeline against a file-backed SQLite store: a post-registration artifact is
// ingested, the response carries the result summary, and last_ingested_at is
// stamped on the layer.
func TestReingestPipeline_SQLiteLocalSource(t *testing.T) {
	t.Parallel()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	layerDir := t.TempDir()
	writeArtifact(t, filepath.Join(layerDir, "alpha"), "alpha artifact")
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "L", SourceType: "local", LocalPath: layerDir,
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}

	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithReingestRunner(localReingestRunner(st, nil))
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/v1/layers/reingest?id=L", "application/json", nil)
	if err != nil {
		t.Fatalf("reingest: %v", err)
	}
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || m["accepted"] != float64(1) {
		t.Fatalf("reingest status=%d body=%v", resp.StatusCode, m)
	}
	got, err := st.GetLayerConfig(ctx, "t", "L")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if got.LastIngestedAt == nil {
		t.Errorf("last_ingested_at not stamped after reingest")
	}
}

// Spec: §4.7.2 — an active freeze window blocks reingest with
// ingest.frozen; a valid break-glass grant bypasses it.
func TestReingestPipeline_FreezeAndBreakGlass(t *testing.T) {
	t.Parallel()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	layerDir := t.TempDir()
	writeArtifact(t, filepath.Join(layerDir, "alpha"), "alpha artifact")
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "L", SourceType: "local", LocalPath: layerDir,
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	now := time.Now().UTC()
	windows := []ingest.FreezeWindow{{
		Name: "maint", Start: now.Add(-time.Hour), End: now.Add(time.Hour), Blocks: []string{"ingest"},
	}}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithReingestRunner(localReingestRunner(st, windows))
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	// Plain reingest → frozen.
	resp, err := http.Post(ts.URL+"/v1/layers/reingest?id=L", "application/json", nil)
	if err != nil {
		t.Fatalf("reingest: %v", err)
	}
	frozenStatus := resp.StatusCode
	resp.Body.Close()
	if frozenStatus != http.StatusConflict {
		t.Fatalf("frozen reingest status = %d, want 409", frozenStatus)
	}

	// Break-glass with two approvers → bypass.
	body, _ := json.Marshal(map[string]any{
		"break_glass": true, "justification": "incident",
		"approvers": []string{"alice@acme.com", "bob@acme.com"},
	})
	resp2, err := http.Post(ts.URL+"/v1/layers/reingest?id=L", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("break-glass reingest: %v", err)
	}
	okStatus := resp2.StatusCode
	resp2.Body.Close()
	if okStatus != http.StatusOK {
		t.Fatalf("break-glass reingest status = %d, want 200", okStatus)
	}
}
