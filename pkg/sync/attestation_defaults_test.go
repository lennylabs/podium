package sync

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// decodeDefaults parses a sync.yaml body and decodes its attestation block.
func decodeDefaults(t *testing.T, body string) (AttestationDefaults, error) {
	t.Helper()
	cfg := &SyncConfig{}
	if err := yaml.Unmarshal([]byte(body), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return cfg.Defaults.AttestationDefaults()
}

// Spec: §7.5.2, §4.7.11 (User sources of the MCP server) — the
// defaults.attestation block decodes its keys, and an absent or null block
// is empty.
func TestAttestationDefaults_Decode(t *testing.T) {
	t.Parallel()
	got, err := decodeDefaults(t, `defaults:
  registry: http://x
  attestation:
    policy: require
    policy_file: p.json
    trusted_root_file: r.json
    root_max_age: 24h
    overlay: deny
    layer_roots: {team: skills, other: &a docs}
`)
	want := AttestationDefaults{Policy: "require", PolicyFile: "p.json", TrustedRootFile: "r.json", RootMaxAge: "24h",
		Overlay: "deny", LayerRoots: map[string]string{"team": "skills", "other": "docs"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("decode = %+v, %v", got, err)
	}
	if set := strings.Join(got.Set(), ","); set != "layer_roots,overlay,policy,policy_file,root_max_age,trusted_root_file" {
		t.Errorf("Set = %s", set)
	}
	for _, body := range []string{"defaults: {}\n", "defaults:\n  attestation:\n", "defaults:\n  attestation: ~\n", "defaults:\n  attestation:\n    policy: ~\n"} {
		if got, err := decodeDefaults(t, body); err != nil || len(got.Set()) != 0 {
			t.Errorf("%q: %+v, %v", body, got, err)
		}
	}
}

// Spec: §7.5.2, §4.7.11 (User sources of the MCP server) — a block that is
// not a mapping, an unknown key, a key twice, and a value of another type
// are refused.
func TestAttestationDefaults_Refusals(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"defaults:\n  attestation: require\n",
		"defaults:\n  attestation:\n    polcy: require\n",
		"defaults:\n  attestation:\n    policy: [require]\n",
		"defaults:\n  attestation:\n    overlay: maybe\n",
		"defaults:\n  attestation:\n    layer_roots: [a]\n",
		"defaults:\n  attestation:\n    layer_roots: {a: 1}\n",
		"defaults:\n  attestation:\n    ? [a]\n    : b\n",
	} {
		if _, err := decodeDefaults(t, body); err == nil || !strings.Contains(err.Error(), "defaults.attestation") {
			t.Errorf("%q: err = %v", body, err)
		}
	}
	if _, err := decodeDefaults(t, "defaults:\n  attestation:\n    policy: a\n    policy: b\n"); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("a key given twice: %v", err)
	}
}

// Spec: §4.7.11 (Policy sources) — a path a sync.yaml names resolves against
// the directory of that file.
func TestResolvePath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	abs := filepath.Join(home, "abs.json")
	for in, want := range map[string]string{"": "", abs: abs, "p.json": filepath.Join(home, ".podium", "p.json"), "../x": filepath.Join(home, "x")} {
		if got := ResolvePath(filepath.Join(home, ".podium", "sync.yaml"), in); got != want {
			t.Errorf("ResolvePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// Spec: §4.7.11 (Known keys), §7.5.2 — a key outside the schema at the top
// level or under defaults is refused and named, so a misplaced or
// misspelled protective block is not dropped; every schema key passes.
func TestCheckKnownKeys(t *testing.T) {
	t.Parallel()
	for body, key := range map[string]string{
		"attestation:\n  policy: require\n":                 `"attestation"`,
		"defaults:\n  attestations:\n    policy: require\n": `"defaults.attestations"`,
		"default:\n  attestation:\n    policy: require\n":   `"default"`,
	} {
		if err := CheckKnownKeys([]byte(body)); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%q: err = %v, want %s named", body, err, key)
		}
	}
	all := "defaults:\n  registry: x\n  harness: none\n  target: t\n  profile: p\n  identity: i\n  verify_signatures: always\n" +
		"  on_unparseable: keep\n  min_server_version: 0.1.0\n  attestation: {policy: none}\nlisting_budgets: {}\nprofiles: {}\ntargets: []\n"
	for _, body := range []string{all, "", "defaults: [\n", "- a\n", "defaults: x\n", "d: &a {x: 1}\ndefaults: *a\n"} {
		err := CheckKnownKeys([]byte(body))
		if (body == "d: &a {x: 1}\ndefaults: *a\n") != (err != nil) {
			t.Errorf("%q: err = %v", body, err)
		}
	}
}
