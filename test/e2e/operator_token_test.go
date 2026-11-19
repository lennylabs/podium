package e2e

import (
	"strings"
	"testing"
)

// e2eOperatorToken is a test value of the minimum accepted length.
const e2eOperatorToken = "0123456789abcdef0123456789abcdef"

// Spec: §4.7, §7.3.1, §13.10, §13.12 — a public-mode registry refuses podium
// admin rebuild-overlap and podium admin reembed with auth.forbidden when
// the CLI's environment holds no PODIUM_OPERATOR_TOKEN or a different value,
// and admits both when it holds the registry's value. The registry here has
// no vector search and the overlap check off, so an admitted request reaches
// the handler and answers its own 404 registry.* code.
// Matrix: §6.10 (auth.forbidden)
func TestOperatorToken_PublicModeAdminCommands(t *testing.T) {
	t.Parallel()
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_PUBLIC_MODE=true",
		"PODIUM_OPERATOR_TOKEN=" + e2eOperatorToken,
		"PODIUM_LINT_SEMANTIC_OVERLAP=",
		"PODIUM_EMBEDDING_PROVIDER=",
		"PODIUM_VECTOR_BACKEND=",
	}, "serve", "--standalone")
	for _, tc := range []struct {
		cmd      string
		admitted string
	}{
		{"rebuild-overlap", "registry.overlap_rebuild_unavailable"},
		{"reembed", "registry.reembed_unavailable"},
	} {
		for _, token := range []string{"", strings.Repeat("x", 32)} {
			res := runPodium(t, "", []string{"PODIUM_OPERATOR_TOKEN=" + token}, "admin", tc.cmd, "--registry", srv.BaseURL)
			if res.Exit != 1 || !strings.Contains(res.Stderr, "HTTP 403") || !strings.Contains(res.Stderr, `"code": "auth.forbidden"`) {
				t.Errorf("podium admin %s with token %q: exit=%d stderr=%q, want exit 1 and 403 auth.forbidden", tc.cmd, token, res.Exit, res.Stderr)
			}
		}
		res := runPodium(t, "", []string{"PODIUM_OPERATOR_TOKEN=" + e2eOperatorToken}, "admin", tc.cmd, "--registry", srv.BaseURL)
		if !strings.Contains(res.Stderr, `"code": "`+tc.admitted+`"`) {
			t.Errorf("podium admin %s with the operator token: exit=%d stderr=%q, want the handler's %s", tc.cmd, res.Exit, res.Stderr, tc.admitted)
		}
	}
}

// Spec: §13.10, §13.12 — a public-mode registry started without
// PODIUM_OPERATOR_TOKEN refuses podium admin rebuild-overlap with
// auth.forbidden even when the CLI sends a token.
// Matrix: §6.10 (auth.forbidden)
func TestOperatorToken_PublicModeWithoutTokenRefuses(t *testing.T) {
	t.Parallel()
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_PUBLIC_MODE=true",
		"PODIUM_OPERATOR_TOKEN=",
	}, "serve", "--standalone")
	res := runPodium(t, "", []string{"PODIUM_OPERATOR_TOKEN=" + e2eOperatorToken}, "admin", "rebuild-overlap", "--registry", srv.BaseURL)
	if res.Exit != 1 || !strings.Contains(res.Stderr, `"code": "auth.forbidden"`) || !strings.Contains(res.Stderr, "PODIUM_OPERATOR_TOKEN") {
		t.Errorf("exit=%d stderr=%q, want exit 1 and auth.forbidden naming PODIUM_OPERATOR_TOKEN", res.Exit, res.Stderr)
	}
}

// Spec: §13.12 — on a public-mode registry a PODIUM_OPERATOR_TOKEN shorter
// than 32 characters refuses startup with config.invalid naming the variable,
// and the refusal does not print the value. The length is counted in
// characters, so 17 characters that take 33 bytes are refused.
// Matrix: §6.10 (config.invalid)
func TestOperatorToken_ShortTokenRefusesStartup(t *testing.T) {
	t.Parallel()
	for _, token := range []string{"too-short-value", strings.Repeat("é", 16) + "x"} {
		out := runPodium(t, "", []string{
			"HOME=" + t.TempDir(),
			"PODIUM_PUBLIC_MODE=true",
			"PODIUM_OPERATOR_TOKEN=" + token,
		}, "serve", "--standalone", "--bind", "127.0.0.1:0")
		combined := out.Stderr + out.Stdout
		if out.Exit == 0 || !strings.Contains(combined, "config.invalid") || !strings.Contains(combined, "PODIUM_OPERATOR_TOKEN must be at least 32 characters") {
			t.Fatalf("serve with the operator token %q: exit=%d\n%s", token, out.Exit, combined)
		}
		if strings.Contains(combined, token) {
			t.Errorf("the refusal printed the token value:\n%s", combined)
		}
	}
}

// Spec: §13.12 — PODIUM_OPERATOR_TOKEN has no effect outside public mode, so
// a registry outside public mode starts with a value public mode would refuse.
func TestOperatorToken_NotValidatedOutsidePublicMode(t *testing.T) {
	t.Parallel()
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_OPERATOR_TOKEN=too-short-value",
	}, "serve", "--standalone")
	res := runPodium(t, "", nil, "layer", "list", "--registry", srv.BaseURL)
	if res.Exit != 0 {
		t.Errorf("podium layer list against the started registry: exit=%d stderr=%q", res.Exit, res.Stderr)
	}
}
