package e2e

// End-to-end coverage for the §4.7.11 visibility of a layer that
// --layer-path bootstraps, through the compiled binary across restarts on one
// store: a .layer-config that admits fewer callers applies at the start, one
// that admits more waits for a startup ingest that passes the layer step and
// is held in visibility_pending until then, `podium layer show` prints the
// pending visibility, and each change records layer.config_changed with its
// cause in the audit log.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// bootstrapVisibilityStart is the environment of a --layer-path registry
// under require whose layer team is signed by the harness's default
// identity, with the extra variables appended. It returns the environment,
// the layer directory, the audit log path, and the harness.
func bootstrapVisibilityStart(t *testing.T, extra ...string) ([]string, string, string, string, *sh.Harness) {
	t.Helper()
	h := sh.New(t)
	home := t.TempDir()
	root := writeRegistry(t, map[string]string{
		".registry-config":                     "multi_layer: true\nlayer_order: [team]\n",
		"team/.layer-config":                   "visibility:\n  groups: [acme-finance]\n",
		"team/finance/pay-invoice/ARTIFACT.md": agentArtifact("pay"),
	})
	rootFile := writeE2EFile(t, home, "github.json", h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	cfgPath := writeE2EFile(t, home, "registry.yaml", []byte("registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: "+rootFile+"\n"))
	reqs := writeE2EFile(t, home, "requirements.json", []byte(`{"trust_requirements":[{"signers":[{"issuer":"`+sh.DefaultIssuer+`","identity":"`+sh.DefaultSAN+`","instance":"github"}]}]}`))
	audit := filepath.Join(home, "audit.log")
	env := append([]string{
		"HOME=" + home, "PODIUM_CONFIG_FILE=" + cfgPath, "PODIUM_INGEST_OFFLINE=true",
		"PODIUM_LAYER_TRUST_DEFAULT=require", "PODIUM_LAYER_TRUST_REQUIREMENTS_FILE=" + reqs,
		"PODIUM_AUDIT_LOG_PATH=" + audit,
	}, extra...)
	return env, root, filepath.Join(root, "team"), audit, h
}

// visibilityEvents returns the cause and the new visibility of each
// layer.config_changed of the action visibility in the audit log.
func visibilityEvents(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var ev struct {
			Type    string            `json:"type"`
			Target  string            `json:"target"`
			Context map[string]string `json:"context"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Type != "layer.config_changed" || ev.Context["action"] != "visibility" {
			continue
		}
		out = append(out, ev.Target+" "+ev.Context["cause"]+" "+ev.Context["visibility"]+" pending="+ev.Context["visibility_pending"])
	}
	return out
}

// Spec: §4.7.11 (Visibility of a path-bootstrapped layer, Startup ingest),
// §13.10, §13.11.1, §6.3.3 — a --layer-path registry under require behind
// trusted-headers, restarted on one store. The signed file admits the group
// alone. An unsigned file that admits every caller changes nothing a caller
// observes and is held pending; once signed, the startup ingest applies it.
// An unsigned file that names one user applies at the next start, although
// the layer step refuses the cycle.
func TestBootstrapVisibility_RestartsWithAChangedLayerConfig(t *testing.T) {
	t.Parallel()
	env, _, team, audit, h := bootstrapVisibilityStart(t, "PODIUM_IDENTITY_PROVIDER=trusted-headers")
	alice := map[string]string{"X-Podium-User-Sub": "alice@acme.com", "X-Podium-User-Groups": "acme-finance"}
	bob := map[string]string{"X-Podium-User-Sub": "bob@acme.com"}
	start := func() *serverProc {
		t.Helper()
		return startServerArgs(t, env, "serve", "--standalone", "--layer-path", filepath.Dir(team))
	}
	expect := func(srv *serverProc, step string, a, b, anonymous int) {
		t.Helper()
		url := srv.BaseURL + "/v1/load_artifact?id=finance/pay-invoice"
		ga, _ := gwHeaderGet(t, url, alice)
		gb, _ := gwHeaderGet(t, url, bob)
		gn, _ := gwHeaderGet(t, url, nil)
		if ga != a || gb != b || gn != anonymous {
			t.Fatalf("%s: alice %d bob %d anonymous %d, want %d %d %d\nlog:\n%s", step, ga, gb, gn, a, b, anonymous, srv.log())
		}
	}

	signE2ELayer(t, h, team, time.Minute)
	srv := start()
	expect(srv, "signed group", 200, 404, 404)
	stopProc(srv.cmd)

	writeE2EFile(t, team, ".layer-config", []byte("visibility:\n  public: true\n"))
	srv = start()
	cliContains(t, srv.log(), "the startup ingest of layer team was refused with ingest.attestation_mismatch", "start log")
	expect(srv, "unsigned public", 200, 404, 404)
	code, body := gwHeaderGet(t, srv.BaseURL+"/v1/layers", alice)
	var list struct {
		Layers []struct {
			Source  string `json:"visibility_source"`
			Pending *struct {
				Public bool `json:"public"`
			} `json:"visibility_pending"`
		} `json:"layers"`
	}
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list.Layers) != 1 ||
		list.Layers[0].Source != "declared" || list.Layers[0].Pending == nil || !list.Layers[0].Pending.Public {
		t.Fatalf("layer list after the unsigned widening: %d %s", code, body)
	}
	stopProc(srv.cmd)

	signE2ELayer(t, h, team, 2*time.Minute)
	srv = start()
	expect(srv, "signed public", 200, 200, 200)
	stopProc(srv.cmd)

	writeE2EFile(t, team, ".layer-config", []byte("visibility:\n  users: [alice@acme.com]\n"))
	srv = start()
	expect(srv, "unsigned user", 200, 404, 404)
	stopProc(srv.cmd)

	want := []string{
		`team cycle {"public":false,"organization":false,"groups":["acme-finance"],"users":null} pending=`,
		`team start {"public":false,"organization":false,"groups":["acme-finance"],"users":null} pending={"public":true,"organization":false,"groups":null,"users":null}`,
		`team cycle {"public":true,"organization":false,"groups":null,"users":null} pending=`,
		`team start {"public":false,"organization":false,"groups":null,"users":["alice@acme.com"]} pending=`,
	}
	if got := visibilityEvents(t, audit); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("visibility events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Pending change),
// §7.3.1 — `podium layer show` prints a pending visibility beside the
// visibility in force on standard error, and the layer object carries
// visibility_source and visibility_pending.
func TestBootstrapVisibility_LayerShowPrintsThePendingVisibility(t *testing.T) {
	t.Parallel()
	env, _, team, _, h := bootstrapVisibilityStart(t)
	signE2ELayer(t, h, team, time.Minute)
	srv := startServerArgs(t, env, "serve", "--standalone", "--layer-path", filepath.Dir(team))
	stopProc(srv.cmd)
	writeE2EFile(t, team, ".layer-config", []byte("visibility:\n  organization: true\n"))
	srv = startServerArgs(t, env, "serve", "--standalone", "--layer-path", filepath.Dir(team))

	res := runPodium(t, "", brEnv(srv.BaseURL), "layer", "show", "team")
	cliWantExit(t, res, 0, "layer show")
	cliContains(t, res.Stdout, `"visibility_source": "declared"`, "source")
	cliContains(t, res.Stdout, `"visibility_pending": {`, "pending member")
	cliContains(t, res.Stderr, `visibility in force: {"groups":["acme-finance"],"organization":false,"public":false,"users":null}`, "in force")
	cliContains(t, res.Stderr, `visibility pending: {"public":false,"organization":true,"groups":null,"users":null}`, "pending")
	cliContains(t, res.Stderr, "A wider visibility takes effect when a cycle passes the layer step, or through `podium layer update` once .layer-config holds it.", "note")
	cliContains(t, res.Stderr, "For a layer that --layer-path loads, a changed .layer-config that admits fewer callers has taken effect", "next step")
}

// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Row created by a
// start, Start write), §13.12 — a first start under warn with no bundle and
// a file that admits every caller creates a row bounded by the deployment
// default organization, which the failed startup ingest fixes as declared.
// A restart that removes the file under the default private applies the
// narrower default, and a later return of the default to organization does
// not widen the declared row.
func TestBootstrapVisibility_RowCreatedUnderWarnIsBoundedByTheDefault(t *testing.T) {
	t.Parallel()
	env, root, team, _, _ := bootstrapVisibilityStart(t, "PODIUM_IDENTITY_PROVIDER=trusted-headers", "PODIUM_LAYER_TRUST_DEFAULT=warn")
	writeE2EFile(t, team, ".layer-config", []byte("visibility:\n  public: true\n"))
	bob := map[string]string{"X-Podium-User-Sub": "bob@acme.com"}
	run := func(def string, bobWant, anonymousWant int) {
		t.Helper()
		srv := startServerArgs(t, append(env, "PODIUM_DEFAULT_LAYER_VISIBILITY="+def), "serve", "--standalone", "--layer-path", root)
		url := srv.BaseURL + "/v1/load_artifact?id=finance/pay-invoice"
		gb, _ := gwHeaderGet(t, url, bob)
		gn, _ := gwHeaderGet(t, url, nil)
		stopProc(srv.cmd)
		if gb != bobWant || gn != anonymousWant {
			t.Fatalf("default %s: bob %d anonymous %d, want %d %d\nlog:\n%s", def, gb, gn, bobWant, anonymousWant, srv.log())
		}
	}
	run("organization", 200, 404)
	if err := os.Remove(filepath.Join(team, ".layer-config")); err != nil {
		t.Fatal(err)
	}
	run("private", 404, 404)
	run("organization", 404, 404)
}
