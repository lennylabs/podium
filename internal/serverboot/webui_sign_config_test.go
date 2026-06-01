package serverboot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §13.10 (F-13.10.9) — --web-ui / --web-ui-allow-public-bind map to
// PODIUM_WEB_UI / PODIUM_WEB_UI_ALLOW_PUBLIC_BIND, which LoadConfig reads into
// the resolved config.
func TestLoadConfig_WebUIEnv(t *testing.T) {
	t.Setenv("PODIUM_WEB_UI", "true")
	t.Setenv("PODIUM_WEB_UI_ALLOW_PUBLIC_BIND", "true")
	c := LoadConfig()
	if !c.webUI || !c.webUIAllowPublicBind {
		t.Errorf("webUI=%v webUIAllowPublicBind=%v, want both true", c.webUI, c.webUIAllowPublicBind)
	}
}

// Spec: §13.10 (F-13.10.9) — validate() refuses the web UI on a non-loopback
// bind without both the escape hatch and an identity provider, surfacing
// config.web_ui_public_bind_refused.
func TestValidate_WebUINonLoopbackRefused(t *testing.T) {
	c := &Config{
		bind:                 "0.0.0.0:8080",
		webUI:                true,
		webUIAllowPublicBind: false,
		identityProvider:     "",
		storeType:            "sqlite",
		objectStore:          "filesystem",
	}
	if err := c.validate(); !errors.Is(err, server.ErrWebUIPublicBindRefused) {
		t.Errorf("validate() = %v, want ErrWebUIPublicBindRefused", err)
	}
}

// Spec: §13.10 (F-13.10.9) — the web UI binds a non-loopback address when both
// the escape hatch and an identity provider are configured.
func TestValidate_WebUINonLoopbackAllowed(t *testing.T) {
	c := &Config{
		bind:                 "0.0.0.0:8080",
		webUI:                true,
		webUIAllowPublicBind: true,
		identityProvider:     "oidc",
		storeType:            "sqlite",
		objectStore:          "filesystem",
	}
	if err := c.validate(); err != nil {
		t.Errorf("validate() = %v, want nil", err)
	}
}

// Spec: §13.10 (F-13.10.14) — the only accepted --sign / PODIUM_SIGN value is
// registry-key; any other value is named at startup rather than silently
// leaving signing disabled.
func TestValidate_SignModeRejectsUnknown(t *testing.T) {
	c := &Config{bind: "127.0.0.1:8080", signMode: "sigstore", storeType: "sqlite", objectStore: "filesystem"}
	err := c.validate()
	if err == nil || !strings.Contains(err.Error(), "config.invalid_sign_mode") {
		t.Errorf("validate() = %v, want config.invalid_sign_mode", err)
	}
}

// Spec: §13.10 (F-13.10.14) — registry-key is accepted; an empty value (signing
// disabled) is accepted.
func TestValidate_SignModeAccepts(t *testing.T) {
	for _, mode := range []string{"", "registry-key"} {
		c := &Config{bind: "127.0.0.1:8080", signMode: mode, storeType: "sqlite", objectStore: "filesystem"}
		if err := c.validate(); err != nil {
			t.Errorf("validate() with signMode=%q = %v, want nil", mode, err)
		}
	}
}

// Spec: §13.10 / §4.7.9 (F-13.10.14) — registrySignerFor returns a working
// registry-managed signer for "registry-key" and nil when signing is disabled.
func TestRegistrySignerFor(t *testing.T) {
	t.Setenv("PODIUM_SIGN_KEY_PATH", t.TempDir()+"/registry-signing.key")

	off, err := registrySignerFor("")
	if err != nil {
		t.Fatalf("registrySignerFor(\"\"): %v", err)
	}
	if off != nil {
		t.Errorf("registrySignerFor(\"\") = non-nil, want nil (signing disabled)")
	}

	signer, err := registrySignerFor("registry-key")
	if err != nil {
		t.Fatalf("registrySignerFor(registry-key): %v", err)
	}
	if signer == nil {
		t.Fatal("registrySignerFor(registry-key) = nil, want a signer")
	}
	// A registry-managed signature is a non-empty JSON envelope over the
	// content hash. spec: §4.7.9.
	env, err := signer(context.Background(), "sha256:"+strings.Repeat("ab", 32))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.Contains(env, "signature") {
		t.Errorf("signature envelope = %q, want a JSON object with a signature field", env)
	}
}
