package e2e

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// ipvServe runs `podium serve --standalone` over a seed layer with env and the
// extra arguments, waits for it to exit, and returns its combined output and
// exit code. Each environment sets PODIUM_IDENTITY_PROVIDER, PODIUM_PUBLIC_MODE,
// PODIUM_MULTI_TENANT, and PODIUM_ALLOW_PUBLIC_BIND explicitly, because
// mergeEnv inherits the test process environment and an inherited value would
// change which refusal the boot reaches.
func ipvServe(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	reg := writeRegistry(t, map[string]string{"seed/ARTIFACT.md": agentArtifact("seed")})
	argv := append([]string{"serve", "--standalone", "--layer-path", reg}, args...)
	cmd := exec.CommandContext(ctx, cmdharness.Bin(t, "podium"), argv...)
	cmd.Env = mergeEnv(env...)
	cmd.Stdin = bytes.NewReader(nil)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &exitErr):
		return out.String(), exitErr.ExitCode()
	default:
		t.Fatalf("run podium serve: %v\noutput:\n%s", err, out.String())
		return "", -1
	}
}

// ipvEnv returns a hermetic identity environment with HOME set and
// PODIUM_IDENTITY_PROVIDER set to provider.
func ipvEnv(t *testing.T, provider string, extra ...string) []string {
	t.Helper()
	return append([]string{
		"HOME=" + t.TempDir(),
		"PODIUM_IDENTITY_PROVIDER=" + provider,
		"PODIUM_PUBLIC_MODE=",
		"PODIUM_MULTI_TENANT=",
		"PODIUM_ALLOW_PUBLIC_BIND=",
	}, extra...)
}

// Spec: §6.3 / §13.10 (External authentication) / §13.12 — a
// PODIUM_IDENTITY_PROVIDER value that names no provider the registry
// recognizes, and is neither external nor none, stops the registry at startup
// with config.identity_provider_unverified and exit code 1. The refusal names
// the value, the providers the registry verifies, and external, and it is
// reported ahead of the bind and multi-tenant checks the misread value would
// otherwise reach. The registry binds no listener.
func TestIdentityProviderValue_UnknownValueRefusesStartup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value string
		extra []string
		args  []string
	}{
		{name: "typo of a verified provider", value: "oidc-jwy"},
		{name: "free-form label", value: "oidc"},
		{name: "non-loopback bind", value: "oidc-jwy", args: []string{"--bind", "0.0.0.0:0"}},
		{name: "multi-tenant", value: "oidc-jwy", extra: []string{"PODIUM_MULTI_TENANT=true"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, code := ipvServe(t, ipvEnv(t, tc.value, tc.extra...), tc.args...)
			if code != 1 {
				t.Errorf("exit code = %d, want 1\noutput:\n%s", code, out)
			}
			for _, want := range []string{
				"config.identity_provider_unverified",
				`PODIUM_IDENTITY_PROVIDER="` + tc.value + `"`,
				"injected-session-token", "oidc-jwt", "trusted-headers",
				"PODIUM_IDENTITY_PROVIDER=external",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			if listeningAddr.MatchString(out) {
				t.Errorf("the refused start bound a listener:\n%s", out)
			}
		})
	}
}

// Spec: §13.12 — identity_provider.type in registry.yaml is read under the same
// rule as PODIUM_IDENTITY_PROVIDER, so an unrecognized type refuses startup
// with config.identity_provider_unverified naming the value.
func TestIdentityProviderValue_UnknownYAMLTypeRefusesStartup(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfgPath := filepath.Join(home, "registry.yaml")
	if err := os.WriteFile(cfgPath, []byte("registry:\n  identity_provider:\n    type: trusted-header\n"), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	env := []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_IDENTITY_PROVIDER=",
		"PODIUM_PUBLIC_MODE=",
		"PODIUM_MULTI_TENANT=",
	}
	out, code := ipvServe(t, env)
	if code != 1 {
		t.Errorf("exit code = %d, want 1\noutput:\n%s", code, out)
	}
	for _, want := range []string{"config.identity_provider_unverified", `"trusted-header"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// Spec: §4.6 (No-identity bypass) / §13.10 (External authentication) — a
// registry started with PODIUM_IDENTITY_PROVIDER=external on a loopback bind
// starts, wires no request-time verifier, and resolves every caller as
// anonymous-public, so an anonymous caller loads an artifact from a layer whose
// declared visibility names a group.
func TestIdentityProviderValue_ExternalStartsWithVisibilityBypassed(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	engRoot := writeRegistry(t, map[string]string{"secret/ARTIFACT.md": agentArtifact("engineering secret")})
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: eng-layer\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + engRoot + "\n" +
		"      visibility:\n" +
		"        groups: [engineering]\n"
	cfgPath := filepath.Join(home, "registry.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_IDENTITY_PROVIDER=external",
		"PODIUM_PUBLIC_MODE=",
		"PODIUM_MULTI_TENANT=",
	}, "serve", "--standalone")

	if st, body := gwHeaderGet(t, srv.BaseURL+"/v1/load_artifact?id=secret", nil); st != 200 {
		t.Errorf("anonymous load of the group-gated artifact = %d, want 200\nbody: %s\nlog:\n%s", st, body, srv.log())
	}
	var health struct {
		Mode string `json:"mode"`
	}
	getJSON(t, srv.BaseURL+"/healthz", &health)
	if health.Mode == "public" {
		t.Errorf("healthz mode = %q, want a non-public registry", health.Mode)
	}
}

// Spec: §13.10 (External authentication) — external verifies no caller, so a
// non-loopback bind is refused at startup with config.public_bind_refused and
// exit code 1, naming the value and the address, and --allow-public-bind lets
// the registry bind there.
func TestIdentityProviderValue_ExternalNonLoopbackBind(t *testing.T) {
	t.Parallel()
	out, code := ipvServe(t, ipvEnv(t, "external"), "--bind", "0.0.0.0:0")
	if code != 1 {
		t.Errorf("exit code = %d, want 1\noutput:\n%s", code, out)
	}
	for _, want := range []string{"config.public_bind_refused", "PODIUM_IDENTITY_PROVIDER=external", `"0.0.0.0:0"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if listeningAddr.MatchString(out) {
		t.Errorf("the refused start bound a listener:\n%s", out)
	}

	srv := launchServer(t, ipvEnv(t, "external"),
		"serve", "--standalone", "--allow-public-bind", "--bind", "0.0.0.0:0")
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Errorf("/healthz on the allowed non-loopback bind = %d, want 200\nlog:\n%s", st, srv.log())
	}
}
