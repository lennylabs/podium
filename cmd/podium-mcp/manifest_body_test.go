package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mbHash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func mbServe(t *testing.T, body []byte) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

// Spec: §6.6 step 1 — a non-skill manifest body delivered above the cutoff is
// fetched from its presigned URL, the frontmatter is restored, and the inline
// manifest_body is re-derived from it (byte-identical to ingest's split).
func TestFetchManifestBody_ReconstitutesContext(t *testing.T) {
	t.Parallel()
	doc := []byte("---\ntype: context\nversion: 1.0.0\ndescription: Glossary.\n---\n\nThe full glossary body.\n")
	srv := &mcpServer{cfg: &config{}, http: &http.Client{}}
	resp := loadArtifactResponse{
		ID: "finance/glossary", Type: "context",
		ManifestBodyURL: &largeResourceLink{URL: mbServe(t, doc), ContentHash: mbHash(doc), Size: int64(len(doc))},
	}
	if err := srv.fetchManifestBody(&resp, nil); err != nil {
		t.Fatalf("fetchManifestBody: %v", err)
	}
	if resp.ManifestBodyURL != nil {
		t.Errorf("manifest_body_url should be cleared after fetch")
	}
	if resp.Frontmatter != string(doc) {
		t.Errorf("frontmatter not reconstituted:\n got %q\nwant %q", resp.Frontmatter, doc)
	}
	if resp.ManifestBody != "The full glossary body.\n" {
		t.Errorf("manifest_body re-derived = %q, want %q", resp.ManifestBody, "The full glossary body.\n")
	}
}

// Spec: §6.6/§4.3.4 — for a skill the canonical document is SKILL.md, so
// skill_raw is restored and manifest_body is re-derived from the SKILL.md body.
func TestFetchManifestBody_ReconstitutesSkill(t *testing.T) {
	t.Parallel()
	skill := []byte("---\nname: run\ndescription: Run it.\n---\n\nThe full skill body.\n")
	srv := &mcpServer{cfg: &config{}, http: &http.Client{}}
	resp := loadArtifactResponse{
		ID: "finance/run", Type: "skill",
		Frontmatter:     "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body in SKILL.md -->\n",
		ManifestBodyURL: &largeResourceLink{URL: mbServe(t, skill), ContentHash: mbHash(skill), Size: int64(len(skill))},
	}
	if err := srv.fetchManifestBody(&resp, nil); err != nil {
		t.Fatalf("fetchManifestBody: %v", err)
	}
	if resp.SkillRaw != string(skill) {
		t.Errorf("skill_raw not reconstituted:\n got %q\nwant %q", resp.SkillRaw, skill)
	}
	if resp.ManifestBody != "The full skill body.\n" {
		t.Errorf("manifest_body re-derived = %q, want %q", resp.ManifestBody, "The full skill body.\n")
	}
	if !strings.Contains(resp.Frontmatter, "type: skill") {
		t.Errorf("ARTIFACT.md frontmatter should be untouched: %q", resp.Frontmatter)
	}
}

// Spec: §6.6 step 1/2 — a tampered or stale body URL whose bytes do not match
// the advertised content hash aborts before reconstitution.
func TestFetchManifestBody_HashMismatchAborts(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}, http: &http.Client{}}
	resp := loadArtifactResponse{
		ID: "x", Type: "context",
		ManifestBodyURL: &largeResourceLink{URL: mbServe(t, []byte("tampered")), ContentHash: "sha256:" + strings.Repeat("a", 64)},
	}
	err := srv.fetchManifestBody(&resp, nil)
	if err == nil || !strings.Contains(err.Error(), "content hash mismatch") {
		t.Errorf("err = %v, want hash-mismatch refusal", err)
	}
}

// A response delivered inline (no manifest_body_url) leaves the fields
// untouched.
func TestFetchManifestBody_NoURLNoop(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}, http: &http.Client{}}
	resp := loadArtifactResponse{ID: "x", Type: "context", Frontmatter: "FM", ManifestBody: "B"}
	if err := srv.fetchManifestBody(&resp, nil); err != nil {
		t.Fatalf("fetchManifestBody: %v", err)
	}
	if resp.Frontmatter != "FM" || resp.ManifestBody != "B" {
		t.Errorf("inline fields must be untouched: %+v", resp)
	}
}
