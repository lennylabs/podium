package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Spec: §6.3.2.1 — env-var rotation is observed at the next
// registry call without a signal because the MCP server reads
// fresh on every call.
func TestCurrentToken_EnvVarRotation(t *testing.T) {
	t.Setenv("PODIUM_SESSION_TOKEN", "old-token")
	srv := &mcpServer{cfg: &config{sessionToken: "loaded-at-startup"}}
	got := srv.currentToken()
	if got != "old-token" {
		t.Fatalf("first read = %q, want old-token (env wins)", got)
	}
	t.Setenv("PODIUM_SESSION_TOKEN", "new-token")
	got = srv.currentToken()
	if got != "new-token" {
		t.Errorf("after rotation = %q, want new-token", got)
	}
}

// Spec: §6.3.2.1 — PODIUM_SESSION_TOKEN_FILE rotations are
// observed at the next call (the file is re-read).
func TestCurrentToken_FileRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srv := &mcpServer{cfg: &config{sessionTokenFile: path}}
	if got := srv.currentToken(); got != "first" {
		t.Fatalf("first read = %q, want first", got)
	}
	if err := os.WriteFile(path, []byte("second"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if got := srv.currentToken(); got != "second" {
		t.Errorf("after rewrite = %q, want second", got)
	}
}

// Spec: §6.3.2.1 — file source wins over env when both are set
// (file is the canonical rotation surface).
func TestCurrentToken_FileBeatsEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	_ = os.WriteFile(path, []byte("from-file"), 0o600)
	t.Setenv("PODIUM_SESSION_TOKEN", "from-env")
	srv := &mcpServer{cfg: &config{sessionTokenFile: path}}
	if got := srv.currentToken(); got != "from-file" {
		t.Errorf("got %q, want from-file", got)
	}
}

// Spec: §6.3.2.1 — PODIUM_SESSION_TOKEN_ENV redirects the env
// lookup to a custom variable, and rotations on that variable
// take effect at the next call.
func TestCurrentToken_CustomEnvSource(t *testing.T) {
	t.Setenv("PODIUM_SESSION_TOKEN_ENV", "MY_CUSTOM_TOKEN")
	t.Setenv("MY_CUSTOM_TOKEN", "custom-1")
	srv := &mcpServer{cfg: &config{}}
	if got := srv.currentToken(); got != "custom-1" {
		t.Fatalf("got %q, want custom-1", got)
	}
	t.Setenv("MY_CUSTOM_TOKEN", "custom-2")
	if got := srv.currentToken(); got != "custom-2" {
		t.Errorf("after rotation = %q, want custom-2", got)
	}
}
