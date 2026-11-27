package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
)

// accRuleFM is an always-loaded rule manifest whose body is body. The gemini
// adapter injects it into GEMINI.md under a block keyed by the artifact ID.
func accRuleFM(ver, body string) string {
	return "---\ntype: rule\nversion: " + ver + "\nrule_mode: always\n---\n\n" + body + "\n"
}

// readStr returns the contents of path, failing the test when it cannot be
// read.
func readStr(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// accDeliver loads a registry-served rule through deliverLoadArtifact and
// fails on an error result.
func accDeliver(t *testing.T, s *mcpServer, dest, id, fm string) {
	t.Helper()
	out := s.deliverLoadArtifact(fixtureResp(id, fm), deliverOpts{harness: "gemini", destination: dest})
	if m, ok := out.(map[string]any); !ok || m["error"] != nil {
		t.Fatalf("deliver %s = %v", id, out)
	}
}

// Spec: §6.6 step 5, §6.7 — a registry-served load reconciles only the loaded
// artifact's inject block: a second rule's load keeps the first rule's block,
// and a re-load at a new version replaces the first rule's block in place of
// adding a second one.
func TestDeliver_LoadKeepsOtherArtifactsBlocks(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "gemini", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	accDeliver(t, s, dest, "acme/alpha", accRuleFM("1.0.0", "Alpha v1."))
	accDeliver(t, s, dest, "acme/beta", accRuleFM("1.0.0", "Beta v1."))
	accDeliver(t, s, dest, "acme/alpha", accRuleFM("2.0.0", "Alpha v2."))

	got := readStr(t, filepath.Join(dest, "GEMINI.md"))
	if n := strings.Count(got, "<!-- podium:begin:acme/alpha -->"); n != 1 {
		t.Errorf("GEMINI.md holds %d alpha blocks, want 1:\n%s", n, got)
	}
	for _, want := range []string{"Alpha v2.", "<!-- podium:begin:acme/beta -->", "Beta v1."} {
		if !strings.Contains(got, want) {
			t.Errorf("GEMINI.md lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Alpha v1.") {
		t.Errorf("the stale alpha block survived the re-load:\n%s", got)
	}
}

// Spec: §6.4, §6.6 step 5, §6.7 — a workspace-overlay load reconciles only the
// overlay artifact's block, as a registry-served load does.
func TestLoadArtifactFromOverlay_KeepsOtherArtifactsBlocks(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	cache, _ := newContentCache(t.TempDir())
	s := &mcpServer{
		cfg:      &config{harness: "gemini", materializeRoot: dest, verifyPolicy: sign.PolicyNever},
		cache:    cache,
		adapters: adapter.DefaultRegistry(),
	}
	rec := func(id, fm string) *filesystem.ArtifactRecord {
		art, err := manifest.ParseArtifact([]byte(fm))
		if err != nil {
			t.Fatalf("parse %s: %v", id, err)
		}
		return &filesystem.ArtifactRecord{ID: id, ArtifactBytes: []byte(fm), AuthoredBytes: []byte(fm), Artifact: art}
	}
	for _, r := range []*filesystem.ArtifactRecord{
		rec("acme/alpha", accRuleFM("1.0.0", "Alpha v1.")),
		rec("acme/beta", accRuleFM("1.0.0", "Beta v1.")),
		rec("acme/alpha", accRuleFM("2.0.0", "Alpha v2.")),
	} {
		if out, ok := s.loadArtifactFromOverlay(r, map[string]any{}).(map[string]any); !ok || out["error"] != nil {
			t.Fatalf("overlay load %s = %v", r.ID, out)
		}
	}
	got := readStr(t, filepath.Join(dest, "GEMINI.md"))
	if strings.Count(got, "<!-- podium:begin:acme/alpha -->") != 1 || !strings.Contains(got, "Alpha v2.") || strings.Contains(got, "Alpha v1.") {
		t.Errorf("the alpha block was not replaced once:\n%s", got)
	}
	if !strings.Contains(got, "Beta v1.") {
		t.Errorf("the alpha re-load removed the beta block:\n%s", got)
	}
}
