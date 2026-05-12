package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/sync"
)

func TestParsePolicies_AcceptsGoDurationAndDaySuffix(t *testing.T) {
	t.Parallel()
	policies, err := parsePolicies([]string{
		"artifacts.searched=720h",
		"artifacts.viewed=30d",
	})
	if err != nil {
		t.Fatalf("parsePolicies: %v", err)
	}
	if len(policies) != 2 {
		t.Fatalf("got %d policies, want 2", len(policies))
	}
	if policies[0].Type != "artifacts.searched" || policies[0].MaxAge.Hours() != 720 {
		t.Errorf("policy[0] = %+v", policies[0])
	}
	if policies[1].Type != "artifacts.viewed" || policies[1].MaxAge.Hours() != 30*24 {
		t.Errorf("policy[1] = %+v", policies[1])
	}
}

func TestParsePolicies_RejectsBadInput(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"missing-equals",
		"bad=notaduration",
		"bad=10x",
	} {
		if _, err := parsePolicies([]string{in}); err == nil {
			t.Errorf("parsePolicies(%q) = nil error, want error", in)
		}
	}
}

func TestGuessTokenURL(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"https://issuer.example/oauth2/device":    "https://issuer.example/oauth2/token",
		"https://issuer.example/v1/oauth/device":  "https://issuer.example/v1/oauth/token",
		"https://issuer.example/device":           "https://issuer.example/token",
		"https://issuer.example/custom-auth":      "https://issuer.example/custom-auth/token",
	}
	for in, want := range cases {
		if got := guessTokenURL(in); got != want {
			t.Errorf("guessTokenURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReplaceSuffix(t *testing.T) {
	t.Parallel()
	if got := replaceSuffix("path/device", "/device", "/token"); got != "path/token" {
		t.Errorf("replaceSuffix swap: got %q", got)
	}
	if got := replaceSuffix("path/other", "/device", "/token"); got != "path/other" {
		t.Errorf("replaceSuffix no-op: got %q", got)
	}
	if got := replaceSuffix("d", "/device", "/token"); got != "d" {
		t.Errorf("replaceSuffix short input: got %q", got)
	}
}

func TestSplitOn(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"":                      {},
		"a":                     {"a"},
		"a b c":                 {"a", "b", "c"},
		"a,b,c":                 {"a", "b", "c"},
		"  a, b  c, ":           {"a", "b", "c"},
		"openid profile,groups": {"openid", "profile", "groups"},
	}
	for in, want := range cases {
		got := splitOn(in)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("splitOn(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestEnvDefault(t *testing.T) {
	const key = "PODIUM_TEST_ENVDEFAULT_XYZZY"
	t.Setenv(key, "")
	if got := envDefault(key, "fallback"); got != "fallback" {
		t.Errorf("envDefault unset: got %q, want fallback", got)
	}
	t.Setenv(key, "from-env")
	if got := envDefault(key, "fallback"); got != "from-env" {
		t.Errorf("envDefault set: got %q, want from-env", got)
	}
}

func TestLoadSignatureProvider(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		wantErr bool
		wantTyp string
	}{
		{"", false, "sign.Noop"},
		{"noop", false, "sign.Noop"},
		{"NOOP", false, "sign.Noop"},
		{"sigstore-keyless", false, "sign.SigstoreKeyless"},
		{"registry-managed", false, "sign.RegistryManagedKey"},
		{"unknown", true, ""},
	}
	for _, c := range cases {
		got, err := loadSignatureProvider(c.name)
		if c.wantErr {
			if err == nil {
				t.Errorf("loadSignatureProvider(%q) = nil error, want error", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("loadSignatureProvider(%q) = %v", c.name, err)
			continue
		}
		gotTyp := fmt.Sprintf("%T", got)
		if gotTyp != c.wantTyp {
			t.Errorf("loadSignatureProvider(%q) type = %s, want %s", c.name, gotTyp, c.wantTyp)
		}
		_ = sign.Provider(got)
	}
}

func TestStringSliceFlag_Set(t *testing.T) {
	t.Parallel()
	var s stringSliceFlag
	if err := s.Set("a"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_ = s.Set("b")
	_ = s.Set("c")
	if got := []string(s); strings.Join(got, ",") != "a,b,c" {
		t.Errorf("stringSliceFlag = %v", got)
	}
	if str := s.String(); !strings.Contains(str, "a") || !strings.Contains(str, "c") {
		t.Errorf("String() = %q", str)
	}
}

func TestFormatToggles_EmptyAndPopulated(t *testing.T) {
	t.Parallel()
	if got := formatToggles(nil); got != "(none)" {
		t.Errorf("empty = %q", got)
	}
	got := formatToggles([]sync.LockToggle{{ID: "personal/a"}, {ID: "personal/b"}})
	if got != "personal/a, personal/b" {
		t.Errorf("populated = %q", got)
	}
}

func TestFormatList_EmptyAndPopulated(t *testing.T) {
	t.Parallel()
	if got := formatList(nil); got != "(none)" {
		t.Errorf("empty = %q", got)
	}
	if got := formatList([]string{"x", "y"}); got != "x, y" {
		t.Errorf("populated = %q", got)
	}
}

func TestPrintJSON_EmitsAdapterTargetAndArtifacts(t *testing.T) {
	got := captureStdout(t, func() {
		printJSON(&sync.Result{
			Adapter: "claude-code",
			Target:  "/tmp/proj",
			Artifacts: []sync.ArtifactResult{
				{ID: "personal/a", Layer: "personal", Files: []string{".claude/agents/a.md"}},
				{ID: "personal/b", Layer: "personal", Files: []string{".claude/agents/b.md", ".claude/agents/b2.md"}},
			},
		})
	})
	var envelope struct {
		Adapter   string `json:"adapter"`
		Target    string `json:"target"`
		Artifacts []struct {
			ID    string   `json:"id"`
			Layer string   `json:"layer"`
			Files []string `json:"files"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("printJSON output not valid JSON: %v\n%s", err, got)
	}
	if envelope.Adapter != "claude-code" || envelope.Target != "/tmp/proj" {
		t.Errorf("envelope = %+v", envelope)
	}
	if len(envelope.Artifacts) != 2 || envelope.Artifacts[1].Files[1] != ".claude/agents/b2.md" {
		t.Errorf("artifacts = %+v", envelope.Artifacts)
	}
}

func TestPrintHuman_IncludesAdapterAndArtifacts(t *testing.T) {
	got := captureStdout(t, func() {
		printHuman(&sync.Result{
			Adapter: "claude-code",
			Target:  "/tmp/proj",
			Artifacts: []sync.ArtifactResult{
				{ID: "personal/a", Layer: "personal", Files: []string{".claude/agents/a.md"}},
			},
		}, true)
	})
	for _, want := range []string{
		"(dry-run; nothing written)",
		"adapter: claude-code",
		"target:  /tmp/proj",
		"personal/a",
		"[personal]",
		".claude/agents/a.md",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("printHuman output missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// captureStdout is defined in config_test.go and shared across the package.

// --- subcommand --help and missing-arg exit-code tests --- //

func TestSubcommands_HelpExitsZero(t *testing.T) {
	t.Parallel()
	for name, cmd := range map[string]func([]string) int{
		"loginCmd":              loginCmd,
		"logoutCmd":             logoutCmd,
		"signCmd":               signCmd,
		"verifyCmd":             verifyCmd,
		"quotaCmd":              quotaCmd,
		"impactCmd":             impactCmd,
		"lintCmd":               lintCmd,
		"syncCmd":               syncCmd,
		"syncOverrideCmd":       syncOverrideCmd,
		"syncSaveAsCmd":         syncSaveAsCmd,
		"adminGrantCmd":         adminGrantCmd,
		"adminRevokeCmd":        adminRevokeCmd,
		"adminShowEffectiveCmd": adminShowEffectiveCmd,
		"adminReembedCmd":       adminReembedCmd,
		"adminEraseCmd":         adminEraseCmd,
		"adminRetentionCmd":     adminRetentionCmd,
		"adminRuntimeList":      adminRuntimeList,
	} {
		t.Run(name+"_help", func(t *testing.T) {
			withStderr(t, func() {
				if code := cmd([]string{"--help"}); code != 0 {
					t.Errorf("%s(--help) = %d, want 0", name, code)
				}
			})
		})
	}
}

func TestDispatchers_NoArgsExit2_HelpExit0(t *testing.T) {
	t.Parallel()
	for name, cmd := range map[string]func([]string) int{
		"cacheCmd":         cacheCmd,
		"configCmd":        configCmd,
		"layerCmd":         layerCmd,
		"profileCmd":       profileCmd,
		"domainCmd":        domainCmd,
		"artifactCmd":      artifactCmd,
		"adminRuntimeCmd":  adminRuntimeCmd,
	} {
		t.Run(name+"_noargs", func(t *testing.T) {
			withStderr(t, func() {
				if code := cmd(nil); code != 2 {
					t.Errorf("%s(nil) = %d, want 2", name, code)
				}
			})
		})
		t.Run(name+"_help", func(t *testing.T) {
			withStderr(t, func() {
				if code := cmd([]string{"help"}); code != 0 {
					t.Errorf("%s(help) = %d, want 0", name, code)
				}
			})
		})
		t.Run(name+"_unknown", func(t *testing.T) {
			withStderr(t, func() {
				if code := cmd([]string{"definitelynotacommand"}); code != 2 {
					t.Errorf("%s(unknown) = %d, want 2", name, code)
				}
			})
		})
	}
}

func TestSubcommands_MissingRegistryExits2(t *testing.T) {
	// Each of these requires --registry. Without the env var or flag
	// set, they should exit 2 before touching the network.
	t.Setenv("PODIUM_REGISTRY", "")
	cases := map[string][]string{
		"logoutCmd":       nil,
		"quotaCmd":        nil,
		"impactCmd":       {"some/id"},
		"adminGrantCmd":   {"alice"},
		"adminRevokeCmd":  {"alice"},
		"adminReembedCmd": nil,
	}
	type cmdEntry struct {
		fn   func([]string) int
		args []string
	}
	entries := map[string]cmdEntry{
		"logoutCmd":       {logoutCmd, cases["logoutCmd"]},
		"quotaCmd":        {quotaCmd, cases["quotaCmd"]},
		"impactCmd":       {impactCmd, cases["impactCmd"]},
		"adminGrantCmd":   {adminGrantCmd, cases["adminGrantCmd"]},
		"adminRevokeCmd":  {adminRevokeCmd, cases["adminRevokeCmd"]},
		"adminReembedCmd": {adminReembedCmd, cases["adminReembedCmd"]},
	}
	for name, e := range entries {
		t.Run(name, func(t *testing.T) {
			withStderr(t, func() {
				if code := e.fn(e.args); code != 2 {
					t.Errorf("%s = %d, want 2", name, code)
				}
			})
		})
	}
}

func TestImpactCmd_NoArgsExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if code := impactCmd(nil); code != 2 {
			t.Errorf("impactCmd(nil) = %d, want 2", code)
		}
	})
}

func TestAdminEraseCmd_NoArgsExits2(t *testing.T) {
	withStderr(t, func() {
		if code := adminEraseCmd(nil); code != 2 {
			t.Errorf("adminEraseCmd(nil) = %d, want 2", code)
		}
	})
}

func TestAdminRetentionCmd_NoPolicyExits2(t *testing.T) {
	withStderr(t, func() {
		if code := adminRetentionCmd(nil); code != 2 {
			t.Errorf("adminRetentionCmd(nil) = %d, want 2", code)
		}
	})
}

func TestAdminReembedCmd_ArtifactWithoutVersionExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if code := adminReembedCmd([]string{"--artifact", "foo"}); code != 2 {
			t.Errorf("adminReembedCmd(--artifact only) = %d, want 2", code)
		}
	})
}

func TestSignCmd_MissingHashExits2(t *testing.T) {
	withStderr(t, func() {
		if code := signCmd(nil); code != 2 {
			t.Errorf("signCmd(nil) = %d, want 2", code)
		}
	})
}

func TestVerifyCmd_MissingFieldsExits2(t *testing.T) {
	withStderr(t, func() {
		if code := verifyCmd(nil); code != 2 {
			t.Errorf("verifyCmd(nil) = %d, want 2", code)
		}
	})
}

func TestSignCmd_RoundTripWithNoopProvider(t *testing.T) {
	out := captureStdout(t, func() {
		withStderr(t, func() {
			rc := signCmd([]string{
				"--provider", "noop",
				"--content-hash", "sha256:" + strings.Repeat("a", 64),
			})
			if rc != 0 {
				t.Errorf("signCmd rc = %d, want 0", rc)
			}
		})
	})
	if out == "" {
		t.Errorf("expected signCmd to emit an envelope on stdout; got empty")
	}
}

func TestLoginCmd_MissingIssuerExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	t.Setenv("PODIUM_OAUTH_AUTHORIZATION_ENDPOINT", "")
	withStderr(t, func() {
		if code := loginCmd(nil); code != 2 {
			t.Errorf("loginCmd(nil) = %d, want 2", code)
		}
	})
}

func TestDomainSearchCmd_NoQueryExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if code := domainSearch(nil); code != 2 {
			t.Errorf("domainSearch(nil) = %d, want 2", code)
		}
	})
}

func TestArtifactShowCmd_NoArgsExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if code := artifactShow(nil); code != 2 {
			t.Errorf("artifactShow(nil) = %d, want 2", code)
		}
	})
}

func TestDomainAnalyze_MissingRegistryExits2(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "")
	withStderr(t, func() {
		if code := domainAnalyze(nil); code != 2 {
			t.Errorf("domainAnalyze(nil) = %d, want 2", code)
		}
	})
}

// withStderr swaps os.Stderr for a discard pipe so noisy validation
// errors don't clutter the test output.
func withStderr(t *testing.T, fn func()) {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = orig
		_ = w.Close()
		go func() { _, _ = io.Copy(io.Discard, r); _ = r.Close() }()
	}()
	fn()
}

