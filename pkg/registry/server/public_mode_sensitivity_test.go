package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// publicSensitivityRegistry writes a filesystem registry with a low-sensitivity
// context artifact and a high-sensitivity skill, returning the directory.
func publicSensitivityRegistry(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testharness.WriteTree(t, dir,
		testharness.WriteTreeOption{
			Path:    "ctx/intro/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: low sensitivity intro\nsensitivity: low\n---\n\nIntro body.\n",
		},
		testharness.WriteTreeOption{
			Path:    "finance/close/run/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\nsensitivity: high\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		},
		testharness.WriteTreeOption{
			Path:    "finance/close/run/SKILL.md",
			Content: "---\nname: run\ndescription: run the close\n---\n\nbody\n",
		},
	)
	return dir
}

func loadStatus(t *testing.T, baseURL, id string) (int, string) {
	t.Helper()
	resp, err := http.Get(baseURL + "/v1/load_artifact?id=" + id)
	if err != nil {
		t.Fatalf("GET load_artifact %s: %v", id, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Spec: §13.10 / §13.2.2 — a public-mode bootstrap rejects medium and high
// sensitivity artifacts at ingest with ingest.public_mode_rejects_sensitive,
// so the high-sensitivity artifact is not served while the low one is.
// F-13.2.2.
func TestNewFromFilesystem_PublicModeRejectsSensitive(t *testing.T) {
	t.Parallel()
	dir := publicSensitivityRegistry(t)
	srv, err := server.NewFromFilesystem(dir, server.WithPublicMode())
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	if st, _ := loadStatus(t, ts.URL, "ctx/intro"); st != http.StatusOK {
		t.Errorf("low-sensitivity artifact load = %d, want 200", st)
	}
	if st, body := loadStatus(t, ts.URL, "finance/close/run"); st == http.StatusOK {
		t.Errorf("high-sensitivity artifact served in public mode (status 200):\n%s", body)
	}
}

// Spec: §13.10 — a non-public bootstrap imposes no sensitivity floor, so the
// high-sensitivity artifact is ingested and served normally. This is the
// control for F-13.2.2.
func TestNewFromFilesystem_NonPublicAcceptsSensitive(t *testing.T) {
	t.Parallel()
	dir := publicSensitivityRegistry(t)
	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	if st, body := loadStatus(t, ts.URL, "finance/close/run"); st != http.StatusOK {
		t.Errorf("high-sensitivity artifact load (non-public) = %d, want 200\n%s", st, body)
	}
}
