package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
)

// Spec: §5 — dropHiddenDescriptors removes the mcp-server descriptors of a
// list, keeps every other element, and leaves a value without such a list
// unchanged; lowerTotalMatched subtracts the dropped count, never below zero.
func TestDropHiddenDescriptors(t *testing.T) {
	t.Parallel()
	v := map[string]any{
		"total_matched": float64(3),
		"results": []any{
			map[string]any{"id": "acme/rule", "type": "rule"},
			map[string]any{"id": "acme/db", "type": "mcp-server"},
			"not an object",
		},
	}
	n := dropHiddenDescriptors(v, "results")
	if n != 1 || len(v["results"].([]any)) != 2 {
		t.Errorf("dropped %d, results = %v; want 1 dropped and 2 kept", n, v["results"])
	}
	lowerTotalMatched(v, n)
	if v["total_matched"] != float64(2) {
		t.Errorf("total_matched = %v, want 2", v["total_matched"])
	}
	lowerTotalMatched(v, 5)
	if v["total_matched"] != float64(0) {
		t.Errorf("total_matched = %v, want 0 after an over-count", v["total_matched"])
	}
	for _, in := range []any{nil, "x", map[string]any{"results": "x"}} {
		if n := dropHiddenDescriptors(in, "results"); n != 0 {
			t.Errorf("dropHiddenDescriptors(%v) = %d, want 0", in, n)
		}
		lowerTotalMatched(in, 1)
	}
}

// Spec: §5 — bridgeRecords leaves out an overlay mcp-server record and keeps
// other records, including one whose manifest did not parse.
func TestBridgeRecords(t *testing.T) {
	t.Parallel()
	got := bridgeRecords([]filesystem.ArtifactRecord{
		{ID: "acme/rule", Artifact: &manifest.Artifact{Type: manifest.TypeRule}},
		{ID: "acme/db", Artifact: &manifest.Artifact{Type: manifest.TypeMCPServer}},
		{ID: "acme/broken"},
	})
	if len(got) != 2 || got[0].ID != "acme/rule" || got[1].ID != "acme/broken" {
		t.Errorf("bridgeRecords = %v, want acme/rule and acme/broken", got)
	}
}

// Spec: §5, §6.10 — deliverLoadArtifact refuses a registry-served mcp-server
// artifact with registry.not_found and writes nothing, and
// loadArtifactFromOverlay refuses an overlay one the same way.
func TestLoadArtifact_RefusesMCPServer(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	fm := "---\ntype: mcp-server\nname: db\nversion: 1.0.0\nserver_identifier: npx:@acme/db-mcp\n---\n\nbody\n"
	s := newTestServer(t, &config{harness: "codex", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	resp := fixtureResp("acme/db", fm)
	resp.Type = "mcp-server"
	resp = sealDelivery(resp)
	out, _ := s.deliverLoadArtifact(resp, deliverOpts{harness: "codex", destination: dest}).(map[string]any)
	if out["code"] != "registry.not_found" {
		t.Errorf("deliver = %v, want registry.not_found", out)
	}
	cache, _ := newContentCache(t.TempDir())
	ov := &mcpServer{
		cfg:      &config{harness: "codex", materializeRoot: dest, verifyPolicy: sign.PolicyNever},
		cache:    cache,
		adapters: adapter.DefaultRegistry(),
	}
	art, err := manifest.ParseArtifact([]byte(fm))
	if err != nil {
		t.Fatal(err)
	}
	rec := &filesystem.ArtifactRecord{ID: "acme/db", ArtifactBytes: []byte(fm), AuthoredBytes: []byte(fm), Artifact: art}
	if got, _ := ov.loadArtifactFromOverlay(rec, map[string]any{}).(map[string]any); got["code"] != "registry.not_found" {
		t.Errorf("overlay load = %v, want registry.not_found", got)
	}
	if _, err := os.Stat(filepath.Join(dest, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Errorf(".codex/config.toml exists after refused loads; stat err=%v", err)
	}
}

// Spec: §5, §6.10 — the refusal carries the registry's not-found message for
// an artifact ID.
func TestBridgeNotFound_MatchesRegistryMessage(t *testing.T) {
	t.Parallel()
	got := bridgeNotFound("acme/db")
	if got["code"] != "registry.not_found" || got["message"] != "artifact acme/db" {
		t.Errorf("bridgeNotFound = %v, want code registry.not_found and message %q", got, "artifact acme/db")
	}
}
