package e2e

// End-to-end coverage for the §4.7.11 layer trust policy through the
// compiled binary: the trust flags of `podium layer register` and `podium
// layer update`, `podium layer show`, the start refusal of a declared policy
// that breaks a rule, the start refusal of an invalid
// PODIUM_LAYER_TRUST_DEFAULT, and the start warning for a layer the default
// leaves with no requirement.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	e2eTrustIssuer   = "https://token.actions.githubusercontent.com"
	e2eTrustRepo     = "https://github.com/acme/finance"
	e2eTrustIdentity = e2eTrustRepo + "/.github/workflows/podium-attest.yml@refs/heads/main"
)

// shownLayer runs `podium layer show id` and decodes the printed object.
func shownLayer(t *testing.T, baseURL, id string) map[string]any {
	t.Helper()
	res := runPodium(t, "", brEnv(baseURL), "layer", "show", id)
	cliWantExit(t, res, 0, "layer show")
	var obj map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &obj); err != nil {
		t.Fatalf("layer show printed no JSON object: %v\n%s", err, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "\n  \"id\": \""+id+"\"") {
		t.Errorf("layer show output is not indented JSON:\n%s", res.Stdout)
	}
	return obj
}

// Spec: §7.3.1 (Layer CLI), §4.7.11 (Derived source binding) — `podium
// layer register` with the trust flags stores the policy with the derived
// source binding, `podium layer show` prints it, `podium layer update`
// patches the mode and replaces the requirement, and
// --clear-trust-requirements removes it. A binding flag with no
// --trust-signer is a usage error, and a policy that breaks a rule is
// refused by the registry.
func TestLayerTrust_CLIRegisterUpdateShow(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	env := brEnv(srv.BaseURL)
	res := runPodium(t, "", env, "layer", "register", "--id", "finance", "--repo", e2eTrustRepo, "--ref", "podium/signed",
		"--trust", "require", "--trust-signer", e2eTrustIssuer+"="+e2eTrustIdentity)
	cliWantExit(t, res, 0, "register with a trust policy")

	obj := shownLayer(t, srv.BaseURL, "finance")
	reqs, _ := obj["trust_requirements"].([]any)
	if obj["trust_mode"] != "require" || len(reqs) != 1 {
		t.Fatalf("shown policy = %v %v", obj["trust_mode"], obj["trust_requirements"])
	}
	signer := reqs[0].(map[string]any)["signers"].([]any)[0].(map[string]any)
	if signer["source_repository"] != e2eTrustRepo || signer["source_repository_ref"] != "refs/heads/main" {
		t.Errorf("shown signer = %v, want the derived binding", signer)
	}

	res = runPodium(t, "", env, "layer", "update", "--id", "finance", "--trust", "warn",
		"--trust-signer", "https://accounts.google.com=alice@acme.com", "--trust-instance", "public-good")
	cliWantExit(t, res, 0, "update the trust policy")
	obj = shownLayer(t, srv.BaseURL, "finance")
	if obj["trust_mode"] != "warn" || !strings.Contains(mustJSON(obj["trust_requirements"]), `"identity":"alice@acme.com"`) {
		t.Errorf("updated policy = %v %v", obj["trust_mode"], obj["trust_requirements"])
	}

	res = runPodium(t, "", env, "layer", "update", "--id", "finance", "--trust", "none", "--clear-trust-requirements")
	cliWantExit(t, res, 0, "clear the requirement")
	if obj = shownLayer(t, srv.BaseURL, "finance"); obj["trust_mode"] != "none" || obj["trust_requirements"] != nil {
		t.Errorf("cleared policy = %v %v", obj["trust_mode"], obj["trust_requirements"])
	}

	res = runPodium(t, "", env, "layer", "update", "--id", "finance", "--trust-source-repository-ref", "refs/heads/main")
	cliWantExit(t, res, 2, "binding flag without a signer")
	cliContains(t, res.Stderr, "--trust-source-repository-ref apply to a --trust-signer", "usage error")

	res = runPodium(t, "", env, "layer", "update", "--id", "finance", "--trust", "require")
	cliWantExit(t, res, 1, "require with no requirement")
	cliContains(t, res.Stderr, "registry.invalid_argument", "refusal code")
	cliContains(t, res.Stderr, "trust_mode require needs a requirement", "refusal rule")

	res = runPodium(t, "", env, "layer", "show", "absent")
	cliWantExit(t, res, 1, "show an absent layer")
	cliContains(t, res.Stderr, "no layer absent", "absent layer")
}

// writeTrustRegistryYAML writes a registry.yaml under home that declares the
// local layer finance with the trust policy block policy.
func writeTrustRegistryYAML(t *testing.T, home, policy string) string {
	t.Helper()
	root := writeRegistry(t, map[string]string{"finance/ap/pay-invoice/ARTIFACT.md": agentArtifact("pay")})
	cfg := "registry:\n  layers:\n    - id: finance\n      source:\n        local:\n          path: " + root + "\n" + policy
	path := filepath.Join(home, "registry.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	return path
}

// Spec: §7.3.1 (Layer trust policy), §4.7.11 (Policy rules) — a declared
// layer whose trust policy breaks a rule refuses startup with
// config.invalid_layer_trust naming the layer and the rule, before the
// listener binds; so does a PODIUM_LAYER_TRUST_DEFAULT that is no mode.
// Matrix: §6.10 (config.invalid_layer_trust)
func TestLayerTrust_StartRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, policy, def, want string
	}{
		{"declared require without requirement", "      trust_mode: require\n", "", "declared layer finance: layer trust policy: trust_mode require needs a requirement"},
		{"declared root with ..", "      trust_layer_root: a/../b\n", "", `declared layer finance: layer trust policy: trust_layer_root "a/../b" holds the segment ".."`},
		{"declared misspelled signer key", "      trust_requirements:\n        - signers: [{issuer: i, identity: alice@acme.com, instance_name: github}]\n", "", `holds the key "instance_name"`},
		{"invalid default", "", "enforce", `PODIUM_LAYER_TRUST_DEFAULT must be none, warn, or require, got "enforce"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			cfgPath := writeTrustRegistryYAML(t, home, tc.policy)
			res := runPodium(t, "", []string{"HOME=" + home, "PODIUM_CONFIG_FILE=" + cfgPath, "PODIUM_INGEST_OFFLINE=true", "PODIUM_LAYER_TRUST_DEFAULT=" + tc.def},
				"serve", "--standalone", "--bind", "127.0.0.1:0")
			if res.Exit == 0 {
				t.Fatalf("the registry started\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			}
			out := res.Stdout + res.Stderr
			for _, want := range []string{"config.invalid_layer_trust", tc.want} {
				if !strings.Contains(out, want) {
					t.Errorf("startup error lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

// Spec: §4.7.11 (Policy changes), §13.12 — a registry started with
// PODIUM_LAYER_TRUST_DEFAULT=require logs a warning for each stored layer
// with no trust_mode and no requirement, and none for a layer whose
// declaration carries a policy; the declared policy is seeded and `podium
// layer show` prints it.
func TestLayerTrust_StartWarningAndDeclaredPolicy(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	root := writeRegistry(t, map[string]string{"x/ARTIFACT.md": agentArtifact("bare")})
	cfgPath := writeTrustRegistryYAML(t, home,
		"      trust_mode: warn\n      trust_requirements:\n        - signers: [{issuer: i, identity: alice@acme.com}]\n"+
			"    - id: bare\n      source:\n        local:\n          path: "+root+"\n")
	srv := startServerArgs(t, []string{"HOME=" + home, "PODIUM_CONFIG_FILE=" + cfgPath, "PODIUM_INGEST_OFFLINE=true", "PODIUM_LAYER_TRUST_DEFAULT=require"},
		"serve", "--standalone")
	log := srv.log()
	if !strings.Contains(log, "warning: layer trust: layer bare (tenant ") ||
		!strings.Contains(log, ") resolves to trust mode require through PODIUM_LAYER_TRUST_DEFAULT and has no trust requirement") {
		t.Errorf("the start log names no unrequired layer:\n%s", log)
	}
	if strings.Contains(log, "layer finance (tenant") {
		t.Errorf("the start log names a layer with a policy:\n%s", log)
	}
	if obj := shownLayer(t, srv.BaseURL, "finance"); obj["trust_mode"] != "warn" {
		t.Errorf("declared policy = %v", obj["trust_mode"])
	}
}
