package e2e

import (
	"strings"
	"testing"
)

// Spec: §7.3.1 (Operator token in public mode), §8.5, §13.10 — through the
// compiled CLI against `podium serve --public-mode`: podium layer register and
// podium admin erase without PODIUM_OPERATOR_TOKEN, or with a different value,
// exit 1 with 403 auth.forbidden and store nothing; with the registry's value
// the registration succeeds and podium layer list shows it to a caller with
// no token.
// Matrix: §6.10 (auth.forbidden)
func TestPublicModeLayerWrites_CLI(t *testing.T) {
	t.Parallel()
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_PUBLIC_MODE=true",
		"PODIUM_OPERATOR_TOKEN=" + e2eOperatorToken,
	}, "serve", "--standalone")
	dir := t.TempDir()
	mkArtifact(t, dir+"/notes", smallteamLowArtifact("operator notes"))
	register := func(token string) cliResult {
		return runPodium(t, "", []string{"PODIUM_OPERATOR_TOKEN=" + token},
			"layer", "register", "--registry", srv.BaseURL, "--id", "ops-notes", "--local", dir)
	}
	for _, token := range []string{"", strings.Repeat("x", 32)} {
		res := register(token)
		if res.Exit != 1 || !strings.Contains(res.Stderr, "HTTP 403") || !strings.Contains(res.Stderr, `"code": "auth.forbidden"`) {
			t.Errorf("layer register with token %q: exit=%d stderr=%q, want exit 1 and 403 auth.forbidden", token, res.Exit, res.Stderr)
		}
		erase := runPodium(t, "", []string{"PODIUM_OPERATOR_TOKEN=" + token},
			"admin", "erase", "alice@acme.com", "--salt", "e2e-test-salt", "--registry", srv.BaseURL)
		if erase.Exit == 0 || !strings.Contains(erase.Stderr, "auth.forbidden") {
			t.Errorf("admin erase with token %q: exit=%d stderr=%q, want a failure carrying auth.forbidden", token, erase.Exit, erase.Stderr)
		}
	}
	list := runPodium(t, "", []string{"PODIUM_OPERATOR_TOKEN="}, "layer", "list", "--registry", srv.BaseURL)
	if list.Exit != 0 || strings.Contains(list.Stdout, "ops-notes") {
		t.Fatalf("layer list after refused registrations: exit=%d stdout=%q, want exit 0 without ops-notes", list.Exit, list.Stdout)
	}

	cliWantExit(t, register(e2eOperatorToken), 0, "layer register with the operator token")
	list = runPodium(t, "", []string{"PODIUM_OPERATOR_TOKEN="}, "layer", "list", "--registry", srv.BaseURL)
	if list.Exit != 0 || !strings.Contains(list.Stdout, "ops-notes") {
		t.Errorf("layer list without a token: exit=%d stdout=%q, want the registered layer", list.Exit, list.Stdout)
	}
}

// Spec: §13.10 (Loopback bind without an identity provider) — a registry with
// no identity provider and not in public mode refuses a non-loopback bind at
// startup with config.public_bind_refused naming the address, and
// --allow-public-bind lets it bind there.
func TestNoIdentityProvider_NonLoopbackBind(t *testing.T) {
	t.Parallel()
	env := []string{"HOME=" + t.TempDir(), "PODIUM_IDENTITY_PROVIDER=", "PODIUM_PUBLIC_MODE=", "PODIUM_ALLOW_PUBLIC_BIND="}
	out := smallteamRawExecFail(t, env, "serve", "--standalone", "--bind", "0.0.0.0:0")
	if !strings.Contains(out, "config.public_bind_refused") || !strings.Contains(out, `"0.0.0.0:0"`) {
		t.Fatalf("serve with no identity provider on 0.0.0.0: want config.public_bind_refused naming the address; got:\n%s", out)
	}
	if listeningAddr.MatchString(out) {
		t.Errorf("the refused start bound a listener:\n%s", out)
	}

	srv := launchServer(t, []string{"HOME=" + t.TempDir(), "PODIUM_IDENTITY_PROVIDER=", "PODIUM_PUBLIC_MODE="},
		"serve", "--standalone", "--allow-public-bind", "--bind", "0.0.0.0:0")
	var health struct {
		Mode string `json:"mode"`
	}
	getJSON(t, srv.BaseURL+"/healthz", &health)
	if health.Mode == "public" {
		t.Errorf("healthz mode=%q, want a non-public registry", health.Mode)
	}
}
