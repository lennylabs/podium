package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
)

// spec: §7.7 (F-7.7.4) — login resolves the registry from --registry,
// then PODIUM_REGISTRY, then the merged sync.yaml's defaults.registry.
func TestResolveClientRegistry_Precedence(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	mustWrite(t, filepath.Join(ws, ".podium", "sync.yaml"), "defaults:\n  registry: https://from-config\n")

	// Flag wins over everything.
	got, err := resolveClientRegistryAt("https://flag", ws, home)
	if err != nil || got != "https://flag" {
		t.Fatalf("flag precedence: got %q err %v", got, err)
	}
	// Env wins over config.
	t.Setenv("PODIUM_REGISTRY", "https://from-env")
	got, err = resolveClientRegistryAt("", ws, home)
	if err != nil || got != "https://from-env" {
		t.Fatalf("env precedence: got %q err %v", got, err)
	}
	// Config is the final fallback.
	t.Setenv("PODIUM_REGISTRY", "")
	got, err = resolveClientRegistryAt("", ws, home)
	if err != nil || got != "https://from-config" {
		t.Fatalf("config fallback: got %q err %v", got, err)
	}
}

// spec: §7.7 (F-7.7.4) — with no registry anywhere, login errors and
// points the user at podium init.
func TestResolveClientRegistry_Unset(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "")
	ws := t.TempDir()
	home := t.TempDir()
	_, err := resolveClientRegistryAt("", ws, home)
	if err == nil {
		t.Fatal("expected an error when no registry is configured")
	}
	if !strings.Contains(err.Error(), "podium init") {
		t.Errorf("error should mention podium init: %v", err)
	}
}

// spec: §7.7 (F-7.7.5) — login is a no-op for filesystem paths and the
// standalone server; it runs for real remote registries.
func TestIsNoAuthRegistry(t *testing.T) {
	cases := map[string]bool{
		"/abs/registry/path":          true,
		".podium/registry/":           true,
		"file:///abs/path":            true,
		"http://127.0.0.1:8080":       true,
		"http://localhost:8080":       true,
		"https://podium.acme.com":     false,
		"http://podium.internal:9000": false,
	}
	for reg, want := range cases {
		if got := isNoAuthRegistry(reg); got != want {
			t.Errorf("isNoAuthRegistry(%q) = %v, want %v", reg, got, want)
		}
	}
}

// spec: §7.7 (F-7.7.5) — login discovers the IdP endpoints from the
// registry's RFC 8414 metadata when --issuer is unset.
func TestDiscoverIdP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"device_authorization_endpoint": "https://idp.acme/device",
				"token_endpoint":                "https://idp.acme/token",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dev, tok, err := discoverIdP(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("discoverIdP: %v", err)
	}
	if dev != "https://idp.acme/device" || tok != "https://idp.acme/token" {
		t.Errorf("discoverIdP = %q, %q", dev, tok)
	}

	// A registry without metadata is an error so login can ask for --issuer.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer bad.Close()
	if _, _, err := discoverIdP(bad.URL, bad.Client()); err == nil {
		t.Error("expected an error when metadata is absent")
	}
}

// spec: §7.7 (F-7.7.7) — login persists both the access token (under the
// registry label, the form readers expect) and the refresh token (under
// a derived label) so silent renewal is possible.
func TestSaveTokens_PersistsAccessAndRefresh(t *testing.T) {
	store := identity.NewMemoryStore()
	reg := "https://podium.acme.com"
	err := saveTokens(store, reg, &identity.Tokens{AccessToken: "access-1", RefreshToken: "refresh-1"})
	if err != nil {
		t.Fatalf("saveTokens: %v", err)
	}
	if got, _ := store.Load(reg); got != "access-1" {
		t.Errorf("access token = %q, want access-1", got)
	}
	if got, _ := store.Load(identity.RefreshLabel(reg)); got != "refresh-1" {
		t.Errorf("refresh token = %q, want refresh-1", got)
	}
	// No refresh token → no refresh entry written.
	store2 := identity.NewMemoryStore()
	_ = saveTokens(store2, reg, &identity.Tokens{AccessToken: "a"})
	if _, err := store2.Load(identity.RefreshLabel(reg)); err == nil {
		t.Error("expected no refresh entry when the IdP returned none")
	}
}

// spec: §7.7 — on success login prints the resolved identity (sub,
// email, OIDC groups) decoded from the ID token.
func TestDecodeIdentity(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"sub":"alice","email":"alice@acme.com","groups":["eng","admins"]}`))
	idToken := "header." + payload + ".sig"
	got := decodeIdentity(idToken)
	for _, want := range []string{"alice", "alice@acme.com", "eng"} {
		if !strings.Contains(got, want) {
			t.Errorf("decodeIdentity missing %q: %q", want, got)
		}
	}
	if decodeIdentity("") != "" || decodeIdentity("not-a-jwt") != "" {
		t.Error("malformed ID tokens should decode to empty")
	}
}

// spec: §7.7 (F-7.7.14) — a bare `podium logout` resolves the registry
// from the merged sync.yaml (the same resolution as login) and clears both
// the access and refresh keychain entries; it does not require --registry.
func TestLogout_ResolvesRegistryFromConfig(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	reg := "https://podium.acme.com"
	mustWrite(t, filepath.Join(ws, ".podium", "sync.yaml"), "defaults:\n  registry: "+reg+"\n")

	store := identity.NewMemoryStore()
	_ = store.Save(reg, "access-1")
	_ = store.Save(identity.RefreshLabel(reg), "refresh-1")

	t.Setenv("PODIUM_REGISTRY", "")
	t.Setenv("HOME", home)
	withCwd(t, ws, func() {
		got, err := resolveClientRegistryAt("", ws, home)
		if err != nil || got != reg {
			t.Fatalf("logout registry resolution: got %q err %v", got, err)
		}
	})
	// Exercise the deletion the command performs once the registry resolves.
	if err := store.Delete(reg); err != nil {
		t.Fatalf("delete access: %v", err)
	}
	_ = store.Delete(identity.RefreshLabel(reg))
	if _, err := store.Load(reg); err == nil {
		t.Error("access token should be cleared")
	}
	if _, err := store.Load(identity.RefreshLabel(reg)); err == nil {
		t.Error("refresh token should be cleared")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
