package e2e

// End-to-end coverage for the §4.6 visibility entry rule on the startup
// paths: a registry.yaml layer declaration and a .layer-config file read
// through --layer-path. Each refuses startup with config.invalid before a
// listener binds, rather than storing an entry the SQL stores would read back
// as several entries.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §4.6 — a layer declared in the registry config whose groups or users
// entry is empty or contains a control character refuses startup with
// config.invalid, naming the layer and the entry's position. An unset
// ${ENV_VAR} interpolated into an entry yields an empty entry and is refused
// on the same terms.
func TestLayerVisibilityEntries_DeclaredLayerRefusesStart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		visibility string
		want       string
	}{
		{"line feed in a users entry", `users: ["alice@acme.com\nbob@acme.com"]`, "visibility.users[0] contains a control character"},
		{"empty group from an unset variable", `groups: [acme-eng, "${PODIUM_E2E_UNSET_GROUP}"]`, "visibility.groups[1] is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			layerRoot := writeRegistry(t, map[string]string{
				"finance/ap/pay-invoice/ARTIFACT.md": agentArtifact("pay"),
			})
			cfgPath := filepath.Join(home, "registry.yaml")
			cfg := "" +
				"registry:\n" +
				"  layers:\n" +
				"    - id: team-finance\n" +
				"      source:\n" +
				"        local:\n" +
				"          path: " + layerRoot + "\n" +
				"      visibility:\n" +
				"        " + tc.visibility + "\n"
			if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
				t.Fatalf("write registry.yaml: %v", err)
			}
			// The boot aborts before the listener binds, so port 0 suffices.
			res := runPodium(t, "", []string{
				"HOME=" + home,
				"PODIUM_CONFIG_FILE=" + cfgPath,
				"PODIUM_INGEST_OFFLINE=true",
			}, "serve", "--standalone", "--bind", "127.0.0.1:0")
			assertEntryStartupRefusal(t, res, "team-finance", tc.want)
		})
	}
}

// Spec: §4.6 — a .layer-config that a registry reads through --layer-path
// and that carries a users entry with a line feed refuses startup with
// config.invalid, naming the file and the entry's position.
func TestLayerVisibilityEntries_LayerConfigRefusesStart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	reg := writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": agentArtifact("pay"),
		".layer-config":                      "visibility:\n  users: [\"alice@acme.com\\nbob@acme.com\"]\n",
	})
	res := runPodium(t, "", []string{"HOME=" + home, "PODIUM_INGEST_OFFLINE=true"},
		"serve", "--standalone", "--bind", "127.0.0.1:0", "--layer-path", reg)
	assertEntryStartupRefusal(t, res, ".layer-config", "visibility.users[0] contains a control character")
}

// assertEntryStartupRefusal asserts a non-zero exit whose output carries
// config.invalid and each wanted fragment.
func assertEntryStartupRefusal(t *testing.T, res cliResult, wants ...string) {
	t.Helper()
	if res.Exit == 0 {
		t.Fatalf("expected a non-zero exit (refuse to start)\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	}
	out := res.Stdout + res.Stderr
	for _, want := range append([]string{"config.invalid"}, wants...) {
		if !strings.Contains(out, want) {
			t.Errorf("startup error does not carry %q:\n%s", want, out)
		}
	}
}
