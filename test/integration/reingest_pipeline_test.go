package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	body := "---\ntype: agent\nversion: 1.0.0\ndescription: " + desc + "\nsensitivity: low\n---\n\nbody\n"
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
	_ = resp.Body.Close()
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
	body := "---\ntype: agent\nversion: 1.0.0\ndescription: " + desc + "\nsensitivity: low\n---\n\nbody\n"
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
	_ = resp.Body.Close()
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
	_ = resp.Body.Close()
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
	_ = resp2.Body.Close()
	if okStatus != http.StatusOK {
		t.Fatalf("break-glass reingest status = %d, want 200", okStatus)
	}
}

// Spec: §4.1, §7.3.1 — a manual reingest whose source holds an artifact with
// undecodable YAML frontmatter refuses that artifact alone. The cycle
// completes without a §6.10 error envelope, because the refusal is neither a
// lint failure nor a same-version change, and the response's rejected list
// names the artifact with ingest.invalid_artifact, a reason naming its
// ARTIFACT.md, and details that are exactly the manifest_unparseable
// constraint. The failure never falls through to registry.unavailable, which
// would report the healthy registry as the problem.
func TestReingestPipeline_MalformedFrontmatterIsRefusedPerArtifact(t *testing.T) {
	t.Parallel()
	layerDir := t.TempDir()
	malformed := "---\ntype: agent\nversion: 1.0.0\ndescription: d\nweird: [unclosed\n---\n\nbody\n"
	writeFile(t, filepath.Join(layerDir, "x", "y", "ARTIFACT.md"), malformed)
	_, reingest := localReingest(t, layerDir)
	status, m := reingest()
	if status != http.StatusOK || m["accepted"] != float64(0) {
		t.Fatalf("reingest = %d %v, want 200 with nothing accepted", status, m)
	}
	rejected, _ := m["rejected"].([]any)
	if len(rejected) != 1 {
		t.Fatalf("rejected = %v, want the one malformed artifact", m["rejected"])
	}
	r, _ := rejected[0].(map[string]any)
	details, _ := r["details"].(map[string]any)
	reason, _ := r["reason"].(string)
	if r["artifact_id"] != "x/y" || r["code"] != "ingest.invalid_artifact" ||
		len(details) != 1 || details["constraint"] != "manifest_unparseable" ||
		!strings.HasPrefix(reason, "x/y/ARTIFACT.md: manifest: invalid YAML frontmatter") {
		t.Errorf("rejected[0] = %v, want x/y refused with manifest_unparseable naming x/y/ARTIFACT.md", r)
	}
}

// localReingest registers layerDir as the local layer L of a file-backed
// SQLite store and returns the store and a function that reingests L through
// the layer endpoint, returning the status and the decoded body.
func localReingest(t *testing.T, layerDir string) (store.Store, func() (int, map[string]any)) {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "L", SourceType: "local", LocalPath: layerDir,
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithReingestRunner(localReingestRunner(st, nil))
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)
	return st, func() (int, map[string]any) {
		t.Helper()
		resp, err := http.Post(ts.URL+"/v1/layers/reingest?id=L", "application/json", nil)
		if err != nil {
			t.Fatalf("reingest: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}
}

// writeFile writes content to path, creating its directory.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// Spec: §4.2, §7.3.1, §6.10 — a SKILL.md directly under the layer root makes
// no package, because a package is a directory below the root, and it still
// fails the walk of the whole layer beside an unparseable manifest the walk
// would otherwise refuse on its own. The reingest is refused as a content
// fault: 422 ingest.lint_failed with retryable false and a remediation that
// does not send the operator to the registry's health endpoint, because the
// registry is healthy and every retry of the same source fails identically.
func TestReingestPipeline_RootLevelSkillIsLintFailure(t *testing.T) {
	t.Parallel()
	layerDir := t.TempDir()
	writeFile(t, filepath.Join(layerDir, "SKILL.md"), "---\nname: root\ndescription: A skill at the layer root.\n---\n\nbody\n")
	writeFile(t, filepath.Join(layerDir, "bad", "SKILL.md"), psMappingDescriptionSkill)
	_, reingest := localReingest(t, layerDir)
	status, m := reingest()
	if status != http.StatusUnprocessableEntity || m["code"] != "ingest.lint_failed" {
		t.Fatalf("reingest = %d %v, want 422 ingest.lint_failed", status, m)
	}
	if m["retryable"] != false {
		t.Errorf("retryable = %v, want false: the same source fails identically", m["retryable"])
	}
	action, _ := m["suggested_action"].(string)
	if action == "" || strings.Contains(action, "health endpoint") {
		t.Errorf("suggested_action = %q, want a remediation that is not the registry's health endpoint", action)
	}
	if msg, _ := m["message"].(string); !strings.Contains(msg, "SKILL.md") || !strings.Contains(msg, "subdirectory") {
		t.Errorf("message = %q, want it to name the root-level SKILL.md", msg)
	}
}

// Spec: §4.1, §4.3.4, §7.3.1 — a layer holding a plain skill whose metadata
// nests a mapping and a sequence beside a valid skill answers the reingest
// with 200, rejects nothing, and stores both skills, the nested one with its
// SKILL.md bytes unchanged.
func TestReingest_NestedMetadataAccepted(t *testing.T) {
	t.Parallel()
	layerDir := t.TempDir()
	writeFile(t, filepath.Join(layerDir, "good", "SKILL.md"), psNamedSkill("good", "valid", "1.0.0"))
	writeFile(t, filepath.Join(layerDir, "bad", "SKILL.md"), psNestedMetadataSkill)
	st, reingest := localReingest(t, layerDir)
	status, m := reingest()
	if status != http.StatusOK || m["accepted"] != float64(2) {
		t.Fatalf("reingest = %d %v, want 200 with both skills accepted", status, m)
	}
	if rejected, _ := m["rejected"].([]any); len(rejected) != 0 {
		t.Errorf("rejected = %v, want none", m["rejected"])
	}
	all, err := st.ListManifestsIncludingDeleted(context.Background(), "t")
	if err != nil {
		t.Fatalf("ListManifestsIncludingDeleted: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("stored rows = %+v, want good and bad", all)
	}
	for _, rec := range all {
		if rec.ArtifactID == "bad" && string(rec.SkillRaw) != psNestedMetadataSkill {
			t.Errorf("bad SkillRaw = %q, want the authored bytes", rec.SkillRaw)
		}
	}
}

// Spec: §7.3.1, §6.10, §4.2 — a local layer whose only package is a SKILL.md
// symlink to a file outside the layer directory fails the reingest with 502
// ingest.source_unreachable under local-source confinement, and stores no
// row, rather than silently dropping the package.
func TestReingestPipeline_EscapingSoleSkillIsSourceUnreachable(t *testing.T) {
	t.Parallel()
	outside := filepath.Join(t.TempDir(), "SKILL.md")
	writeFile(t, outside, "---\nname: x\ndescription: A skill outside the layer.\n---\n\nbody\n")
	layerDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(layerDir, "x"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(layerDir, "x", "SKILL.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	st, reingest := localReingest(t, layerDir)
	status, m := reingest()
	if status != http.StatusBadGateway || m["code"] != "ingest.source_unreachable" {
		t.Errorf("reingest = %d %v, want 502 ingest.source_unreachable", status, m)
	}
	all, err := st.ListManifestsIncludingDeleted(context.Background(), "t")
	if err != nil {
		t.Fatalf("ListManifestsIncludingDeleted: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("stored rows = %+v, want none", all)
	}
}

// Spec: §7.3.1 — a version-less skill reverted to bytes a stored row already
// holds is reported in the reingest response's rejected array with
// ingest.invalid_artifact and its constraint, and nothing is accepted.
func TestReingestPipeline_RevertedVersionlessSkillIsRejected(t *testing.T) {
	t.Parallel()
	layerDir := t.TempDir()
	path := filepath.Join(layerDir, "y", "SKILL.md")
	first := "---\nname: y\ndescription: A version-less skill.\n---\n\nfirst\n"
	writeFile(t, path, first)
	_, reingest := localReingest(t, layerDir)
	if status, m := reingest(); status != http.StatusOK || m["accepted"] != float64(1) {
		t.Fatalf("first reingest = %d %v, want one accepted", status, m)
	}
	writeFile(t, path, "---\nname: y\ndescription: A version-less skill.\n---\n\nchanged\n")
	if status, m := reingest(); status != http.StatusOK || m["accepted"] != float64(1) {
		t.Fatalf("second reingest = %d %v, want one accepted", status, m)
	}
	writeFile(t, path, first)
	status, m := reingest()
	if status != http.StatusOK || m["accepted"] != float64(0) || m["idempotent"] != float64(0) {
		t.Fatalf("revert reingest = %d %v, want 200 with nothing accepted or idempotent", status, m)
	}
	rejected, _ := m["rejected"].([]any)
	if len(rejected) != 1 {
		t.Fatalf("rejected = %v, want one entry", m["rejected"])
	}
	entry, _ := rejected[0].(map[string]any)
	details, _ := entry["details"].(map[string]any)
	if entry["artifact_id"] != "y" || entry["code"] != "ingest.invalid_artifact" ||
		len(details) != 1 || details["constraint"] != "auto_version_duplicate_content" {
		t.Errorf("rejected entry = %v, want y, ingest.invalid_artifact, and the duplicate-content constraint", entry)
	}
}
