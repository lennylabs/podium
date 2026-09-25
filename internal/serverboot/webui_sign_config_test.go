package serverboot

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §13.10 — --web-ui / --web-ui-allow-public-bind map to
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

// Spec: §13.10 — validate() refuses the web UI on a non-loopback
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

// Spec: §13.10 — the web UI binds a non-loopback address when both
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

// Spec: §13.10 — --sign / PODIUM_SIGN accepts registry-key and none;
// any other value is named at startup rather than silently read as a mode.
// Matrix: §6.10 (config.invalid_sign_mode)
func TestValidate_SignModeRejectsUnknown(t *testing.T) {
	c := &Config{bind: "127.0.0.1:8080", signMode: "sigstore", storeType: "sqlite", objectStore: "filesystem"}
	err := c.validate()
	if err == nil || !strings.Contains(err.Error(), "config.invalid_sign_mode") {
		t.Errorf("validate() = %v, want config.invalid_sign_mode", err)
	}
	if err != nil && !strings.Contains(err.Error(), "registry-key or none") {
		t.Errorf("validate() = %v, want the message to name both accepted values", err)
	}
}

// Spec: §13.10 — registry-key and none are accepted, and an empty value
// (which resolves to registry-key) is accepted.
func TestValidate_SignModeAccepts(t *testing.T) {
	for _, mode := range []string{"", "registry-key", "none"} {
		c := &Config{bind: "127.0.0.1:8080", signMode: mode, storeType: "sqlite", objectStore: "filesystem"}
		if err := c.validate(); err != nil {
			t.Errorf("validate() with signMode=%q = %v, want nil", mode, err)
		}
	}
}

// Spec: §13.10 / §4.7.9 — registrySignerFor returns a working
// registry-managed key and reports signing on for an empty mode and for
// "registry-key", because the registry signs by default, and reports signing
// off with the zero key for "none".
func TestRegistrySignerFor(t *testing.T) {
	t.Setenv("PODIUM_SIGN_KEY_PATH", t.TempDir()+"/registry-signing.key")

	off, on, err := registrySignerFor("none")
	if err != nil {
		t.Fatalf("registrySignerFor(none): %v", err)
	}
	if on || off.PrivateKey != nil {
		t.Errorf("registrySignerFor(none) = (%v, %v), want the zero key and signing off", off, on)
	}

	def, on, err := registrySignerFor("")
	if err != nil {
		t.Fatalf("registrySignerFor(\"\"): %v", err)
	}
	if !on || def.PrivateKey == nil {
		t.Fatal("registrySignerFor(\"\") reports no signer, want one (signing is on by default)")
	}

	signer, on, err := registrySignerFor("registry-key")
	if err != nil {
		t.Fatalf("registrySignerFor(registry-key): %v", err)
	}
	if !on || signer.PrivateKey == nil {
		t.Fatal("registrySignerFor(registry-key) reports no signer, want one")
	}
	// A registry-managed signature is a non-empty JSON envelope over the
	// content hash, and the same provider verifies it. spec: §4.7.9. The
	// §13.4 rehash pass relies on that round trip: it verifies a stored
	// envelope with the loader's own provider before it rewrites the row.
	hash := "sha256:" + strings.Repeat("ab", 32)
	env, err := signer.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.Contains(env, "signature") {
		t.Errorf("signature envelope = %q, want a JSON object with a signature field", env)
	}
	if err := signer.Verify(context.Background(), hash, env); err != nil {
		t.Errorf("Verify over the provider's own envelope: %v", err)
	}
}

// Spec: §13.4, §13.12 — PODIUM_MIGRATION_OBJECT_READ_TIMEOUT bounds each
// object-storage read the first-start stored-value rewrite makes. An unset,
// zero, negative, or unparsable value takes the 30-second default, because a
// zero deadline is no deadline at all and run's context carries none.
func TestLoadConfig_MigrationObjectReadTimeout(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  time.Duration
	}{
		{name: "unset", want: 30 * time.Second},
		{name: "zero", value: "0", set: true, want: 30 * time.Second},
		{name: "negative", value: "-1s", set: true, want: 30 * time.Second},
		{name: "unparsable", value: "later", set: true, want: 30 * time.Second},
		{name: "configured", value: "2s", set: true, want: 2 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnvForTest(t, "PODIUM_MIGRATION_OBJECT_READ_TIMEOUT", tc.value, tc.set)
			if got := LoadConfig().migrationObjectReadTimeout; got != tc.want {
				t.Errorf("migrationObjectReadTimeout = %v, want %v", got, tc.want)
			}
		})
	}
}

// The §6.3.4 browser-flow keys the key-placement rule places in §13.10: the
// enablement boolean, the transaction TTL, the acquisition values, the scope
// set, and the exchange bound.

// setEnvForTest sets key for the duration of the test and restores what was
// there, distinguishing an unset variable from an empty one.
func setEnvForTest(t *testing.T, key, value string, set bool) {
	t.Helper()
	orig, had := os.LookupEnv(key)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, orig)
			return
		}
		_ = os.Unsetenv(key)
	})
	if !set {
		_ = os.Unsetenv(key)
		return
	}
	_ = os.Setenv(key, value)
}

// Spec: §7.3.4 — the transaction TTL is the sign-in window carried as
// __Host-podium_auth's Max-Age. An unset, unparsable, or non-positive value
// takes the 10-minute default. The 0 case is what the table exists for: the
// shipped envInt idiom passes a literal 0 through, and a zero Max-Age is not
// the window the cookie bounds.
func TestLoadConfig_WebUIAuthTransactionTTL(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  time.Duration
	}{
		{name: "unset", want: 10 * time.Minute},
		{name: "zero", value: "0", set: true, want: 10 * time.Minute},
		{name: "negative", value: "-5m", set: true, want: 10 * time.Minute},
		{name: "unparsable", value: "soon", set: true, want: 10 * time.Minute},
		{name: "empty", value: "", set: true, want: 10 * time.Minute},
		{name: "configured", value: "90s", set: true, want: 90 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnvForTest(t, "PODIUM_WEB_UI_AUTH_TRANSACTION_TTL", tc.value, tc.set)
			if got := LoadConfig().webUIAuthTransactionTTL; got != tc.want {
				t.Errorf("webUIAuthTransactionTTL = %v, want %v", got, tc.want)
			}
		})
	}
}

// Spec: §6.3.4 — the callback's token-endpoint request carries a deadline, so
// every exchange either answers within it or fails. An unset, unparsable, or
// non-positive value takes the 10-second default, because a zero
// http.Client.Timeout is no deadline at all and nothing else ends a request
// whose token endpoint accepts the connection and never answers.
func TestLoadConfig_WebUIOAuthExchangeTimeout(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  time.Duration
	}{
		{name: "unset", want: 10 * time.Second},
		{name: "zero", value: "0", set: true, want: 10 * time.Second},
		{name: "negative", value: "-1s", set: true, want: 10 * time.Second},
		{name: "unparsable", value: "later", set: true, want: 10 * time.Second},
		{name: "configured", value: "2s", set: true, want: 2 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnvForTest(t, "PODIUM_WEB_UI_OAUTH_EXCHANGE_TIMEOUT", tc.value, tc.set)
			if got := LoadConfig().webUIOAuthExchangeTimeout; got != tc.want {
				t.Errorf("webUIOAuthExchangeTimeout = %v, want %v", got, tc.want)
			}
		})
	}
}

// Spec: §6.3.4 — the scope set defaults to "openid profile email groups"
// rather than to "openid" alone, because a token issued without the scope
// that carries the group claim carries none and every group-scoped visibility
// decision narrows silently for a browser caller.
func TestLoadConfig_WebUIOAuthScopes(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  []string
	}{
		{name: "unset", want: []string{"openid", "profile", "email", "groups"}},
		{name: "empty", value: "", set: true, want: []string{"openid", "profile", "email", "groups"}},
		{name: "configured", value: "openid acme-groups", set: true, want: []string{"openid", "acme-groups"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnvForTest(t, "PODIUM_WEB_UI_OAUTH_SCOPES", tc.value, tc.set)
			if got := LoadConfig().webUIOAuthScopes; !slices.Equal(got, tc.want) {
				t.Errorf("webUIOAuthScopes = %v, want %v", got, tc.want)
			}
		})
	}
}

// Spec: §6.3.4 / §13.10 — the enablement boolean and the acquisition values
// are read at boot from their PODIUM_* variables, and the acquisition set
// carries no flag, so the whole set stays off the process table.
func TestLoadConfig_WebUIAuthAcquisitionValues(t *testing.T) {
	setEnvForTest(t, "PODIUM_WEB_UI_AUTH", "true", true)
	setEnvForTest(t, "PODIUM_WEB_UI_OAUTH_CLIENT_ID", "podium-web-ui", true)
	setEnvForTest(t, "PODIUM_WEB_UI_OAUTH_CLIENT_SECRET", "s3cret", true)
	setEnvForTest(t, "PODIUM_WEB_UI_REDIRECT_URI", "https://registry.acme.com/v1/ui/auth/callback", true)
	setEnvForTest(t, "PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT", "https://idp.acme.com/authorize", true)
	setEnvForTest(t, "PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT", "https://idp.acme.com/token", true)

	c := LoadConfig()
	if !c.webUIAuth {
		t.Error("webUIAuth = false, want true")
	}
	got := map[string]string{
		"client id":              c.webUIOAuthClientID,
		"client secret":          c.webUIOAuthClientSecret,
		"redirect uri":           c.webUIRedirectURI,
		"authorization endpoint": c.webUIOAuthAuthorizationEndpoint,
		"token endpoint":         c.webUIOAuthTokenEndpoint,
	}
	want := map[string]string{
		"client id":              "podium-web-ui",
		"client secret":          "s3cret",
		"redirect uri":           "https://registry.acme.com/v1/ui/auth/callback",
		"authorization endpoint": "https://idp.acme.com/authorize",
		"token endpoint":         "https://idp.acme.com/token",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// Spec: §6.3.4 — the browser flow does not read the device-code key. A
// registry that configures PODIUM_OAUTH_AUTHORIZATION_ENDPOINT alone leaves
// the browser flow's own authorization endpoint empty, which is the startup
// refusal the guard names rather than a redirect to nowhere.
func TestLoadConfig_DeviceCodeKeyIsNotTheBrowserFlowEndpoint(t *testing.T) {
	setEnvForTest(t, "PODIUM_OAUTH_AUTHORIZATION_ENDPOINT", "https://idp.acme.com/device", true)
	setEnvForTest(t, "PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT", "", false)
	c := LoadConfig()
	if c.webUIOAuthAuthorizationEndpoint != "" {
		t.Errorf("webUIOAuthAuthorizationEndpoint = %q, want empty", c.webUIOAuthAuthorizationEndpoint)
	}
	if c.oauthAuthorizationEndpoint != "https://idp.acme.com/device" {
		t.Errorf("oauthAuthorizationEndpoint = %q, want the device-code key's value", c.oauthAuthorizationEndpoint)
	}
}
