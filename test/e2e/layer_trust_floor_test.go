package e2e

// End-to-end coverage for the §4.7.11 tenant trust floor of a single-tenant
// registry through the compiled binary: the PODIUM_LAYER_TRUST_FLOOR_FILE
// setting and the registry.yaml trust_floor key, the start log and the
// per-cycle refusal of a declared layer the floor makes invalid, the
// registration refusal below the floor, the precedence of the variable,
// the start refusals of a value the registry cannot use and of either
// setting on a multi-tenant registry, and the served outcome that reads the
// floor of the running process.

import (
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// floorServer starts a registry whose registry.yaml declares the local layer
// finance with the explicit trust mode none, with the extra registry.yaml
// lines extra and the environment env.
func floorServer(t *testing.T, extra string, env ...string) (*serverProc, string) {
	t.Helper()
	home := t.TempDir()
	cfgPath := writeTrustRegistryYAML(t, home, "      trust_mode: none\n"+extra)
	return startServerArgs(t, append([]string{"HOME=" + home, "PODIUM_CONFIG_FILE=" + cfgPath, "PODIUM_INGEST_OFFLINE=true"}, env...),
		"serve", "--standalone"), home
}

// Spec: §4.7.11 (Tenant trust floor, Policy that became invalid, Startup
// ingest), §13.12, §7.3.1 (Layer trust policy) — a single-tenant registry
// started with PODIUM_LAYER_TRUST_FLOOR_FILE, and separately with the
// registry.yaml key trust_floor, starts with a declared layer whose
// explicit mode none is below the floor: the start log names the layer,
// its startup ingest is refused, the record holds
// ingest.attestation_policy_invalid with below_floor, and a reingest exits
// 1 with that envelope. A registration below the floor is refused with
// registry.invalid_argument, and one that meets it is stored.
func TestTrustFloor_SingleTenantSetting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		key  string
		file bool
	}{
		{name: "file", file: true},
		{name: "key", key: "  trust_floor:\n    mode: warn\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var env []string
			if tc.file {
				env = append(env, "PODIUM_LAYER_TRUST_FLOOR_FILE="+writeE2EFile(t, t.TempDir(), "floor.json", []byte(`{"mode":"warn"}`)))
			}
			srv, _ := floorServer(t, tc.key, env...)
			log := srv.log()
			cliContains(t, log, "layer finance (tenant ", "start log layer")
			cliContains(t, log, "does not meet the tenant trust floor (below_floor)", "start log constraint")
			cliContains(t, log, `the startup ingest of layer finance was refused with ingest.attestation_policy_invalid (constraint "below_floor")`, "startup refusal")

			obj := shownLayer(t, srv.BaseURL, "finance")
			details, _ := obj["attestation_details"].(map[string]any)
			if obj["attestation_status"] != "ingest.attestation_policy_invalid" || details["constraint"] != "below_floor" {
				t.Errorf("record = %v %v", obj["attestation_status"], obj["attestation_details"])
			}
			env = brEnv(srv.BaseURL)
			res := runPodium(t, "", env, "layer", "reingest", "finance")
			cliWantExit(t, res, 1, "reingest below the floor")
			cliContains(t, res.Stderr, `"code": "ingest.attestation_policy_invalid"`, "reingest code")
			cliContains(t, res.Stderr, `"constraint": "below_floor"`, "reingest constraint")

			scratch := writeRegistry(t, map[string]string{"x/ARTIFACT.md": agentArtifact("x")})
			res = runPodium(t, "", env, "layer", "register", "--id", "scratch", "--local", scratch, "--trust", "none")
			cliWantExit(t, res, 1, "registration below the floor")
			cliContains(t, res.Stderr, "registry.invalid_argument", "refusal code")
			cliContains(t, res.Stderr, `trust_mode \"none\" is below the tenant trust floor's mode warn`, "refusal rule")
			res = runPodium(t, "", env, "layer", "register", "--id", "scratch", "--local", scratch, "--trust", "warn",
				"--trust-signer", "https://accounts.google.com=alice@acme.com")
			cliWantExit(t, res, 0, "registration that meets the floor")
		})
	}
}

// Spec: §13.12, §4.7.11 (Tenant trust floor) — PODIUM_LAYER_TRUST_FLOOR_FILE
// takes precedence over the registry.yaml key: with the key at require and
// the file at warn, a registration under warn is stored.
func TestTrustFloor_VariableWinsOverKey(t *testing.T) {
	t.Parallel()
	file := writeE2EFile(t, t.TempDir(), "floor.json", []byte(`{"mode":"warn"}`))
	srv, _ := floorServer(t, "  trust_floor:\n    mode: require\n", "PODIUM_LAYER_TRUST_FLOOR_FILE="+file)
	scratch := writeRegistry(t, map[string]string{"x/ARTIFACT.md": agentArtifact("x")})
	res := runPodium(t, "", brEnv(srv.BaseURL), "layer", "register", "--id", "scratch", "--local", scratch, "--trust", "warn",
		"--trust-signer", "https://accounts.google.com=alice@acme.com")
	cliWantExit(t, res, 0, "registration under the file's floor")
}

// Spec: §4.7.11 (Tenant trust floor), §13.12 — a floor file that does not
// parse, a floor whose entry names an instance the registry does not
// configure, a trust_floor key with a key of another name, and the variable
// on a multi-tenant registry each refuse the start with
// config.invalid_layer_trust, naming the setting.
// Matrix: §6.10 (config.invalid_layer_trust)
func TestTrustFloor_StartRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, key, file string
		env             []string
		want            string
	}{
		{name: "file does not parse", file: "{mode", want: "PODIUM_LAYER_TRUST_FLOOR_FILE"},
		{name: "unknown instance", file: `{"signers":[{"issuer":"i","instance":"acme-sigstore"}]}`, want: `names the Sigstore instance "acme-sigstore"`},
		{name: "key of another name", key: "  trust_floor:\n    mod: warn\n", want: `registry.yaml trust_floor: trust_floor holds the key "mod"`},
		{name: "multi-tenant", file: `{"mode":"warn"}`, want: "is set on a multi-tenant registry",
			env: []string{"PODIUM_MULTI_TENANT=true", "PODIUM_IDENTITY_PROVIDER=trusted-headers", "PODIUM_TRUSTED_PROXY_SECRET=tf-proxy-secret-0123456789"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			cfgPath := writeTrustRegistryYAML(t, home, tc.key)
			env := append([]string{"HOME=" + home, "PODIUM_CONFIG_FILE=" + cfgPath, "PODIUM_INGEST_OFFLINE=true"}, tc.env...)
			if tc.file != "" {
				env = append(env, "PODIUM_LAYER_TRUST_FLOOR_FILE="+writeE2EFile(t, home, "floor.json", []byte(tc.file)))
			}
			res := runPodium(t, "", env, "serve", "--standalone", "--bind", "127.0.0.1:0")
			out := res.Stdout + res.Stderr
			if res.Exit == 0 || !strings.Contains(out, "config.invalid_layer_trust") || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d, want config.invalid_layer_trust with %q:\n%s", res.Exit, tc.want, out)
			}
		})
	}
}

// Spec: §4.7.11 (Served outcome, Tenant trust floor) — the served outcome
// reads the floor of the running process: a registered layer that a
// reingest verified is served as verified, a restart with a floor whose
// signer entry the layer's signer does not match serves the stored version
// as failed with ingest.attestation_policy_invalid and signer_outside_floor
// before any cycle of the layer runs, and a restart without the floor
// serves it as verified again.
func TestTrustFloor_ServedOutcomeReadsTheRunningFloor(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	home := t.TempDir()
	root := writeRegistry(t, map[string]string{"finance/ap/pay-invoice/ARTIFACT.md": agentArtifact("pay")})
	signE2ELayer(t, h, root, time.Minute)
	rootFile := writeE2EFile(t, home, "github.json", h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	cfgPath := writeE2EFile(t, home, "registry.yaml", []byte("registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: "+rootFile+"\n"))
	base := []string{"HOME=" + home, "PODIUM_CONFIG_FILE=" + cfgPath, "PODIUM_INGEST_OFFLINE=true"}
	outcome := func(srv *serverProc) string {
		t.Helper()
		res := runPodium(t, "", brEnv(srv.BaseURL), "artifact", "show", "finance/ap/pay-invoice")
		cliWantExit(t, res, 0, "artifact show")
		return res.Stderr
	}

	srv := startServerArgs(t, base, "serve", "--standalone")
	env := brEnv(srv.BaseURL)
	cliWantExit(t, runPodium(t, "", env, "layer", "register", "--id", "team", "--local", root, "--trust", "require",
		"--trust-signer", sh.DefaultIssuer+"="+sh.DefaultSAN, "--trust-instance", "github"), 0, "register")
	cliWantExit(t, runPodium(t, "", env, "layer", "reingest", "team"), 0, "reingest")
	cliContains(t, outcome(srv), `attestation_outcome: "verified"`, "outcome before the floor")
	stopProc(srv.cmd)

	floor := writeE2EFile(t, home, "floor.json", []byte(`{"mode":"require","signers":[{"issuer":"`+sh.DefaultIssuer+`","instance":"github","identity":"https://other"}]}`))
	srv = startServerArgs(t, append(base, "PODIUM_LAYER_TRUST_FLOOR_FILE="+floor), "serve", "--standalone")
	cliContains(t, srv.log(), "layer team (tenant ", "start log")
	shown := outcome(srv)
	cliContains(t, shown, `attestation_outcome: "failed"`, "outcome under the floor")
	cliContains(t, shown, "ingest.attestation_policy_invalid", "outcome code")
	cliContains(t, shown, "signer_outside_floor", "outcome constraint")
	stopProc(srv.cmd)

	srv = startServerArgs(t, base, "serve", "--standalone")
	cliContains(t, outcome(srv), `attestation_outcome: "verified"`, "outcome after the floor")
}
