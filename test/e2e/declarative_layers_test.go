package e2e

// End-to-end coverage for the §4.6 declarative `layers:` registry-config list.
// A registry.yaml that declares a local-source admin layer with a
// visibility block boots a standalone server that ingests the layer and
// exposes it through the §7.3.1 layer-management API.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	layerwebhook "github.com/lennylabs/podium/pkg/layer/webhook"
)

// Spec: §4.6 — a declared local-source layer in registry.yaml is
// ingested at startup (its artifacts are searchable) and is registered so the
// /v1/layers management surface reports it with the declared source and
// visibility.
func TestDeclarativeLayers_LocalLayerBootsAndServes(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	// The declared local layer's artifact tree.
	layerRoot := writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: pay vendor invoices\nsensitivity: low\n---\n\nbody\n",
	})

	cfgPath := filepath.Join(home, "registry.yaml")
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: org-defaults\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + layerRoot + "\n" +
		"      visibility:\n" +
		"        public: true\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	srv := startServerArgs(t,
		[]string{"HOME=" + home, "PODIUM_CONFIG_FILE=" + cfgPath, "PODIUM_INGEST_OFFLINE=true"},
		"serve", "--standalone")

	// The declared layer's artifact is searchable out of the box.
	var search struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	getJSON(t, srv.BaseURL+"/v1/search_artifacts?scope=finance", &search)
	found := false
	for _, r := range search.Results {
		if r.ID == "finance/ap/pay-invoice" {
			found = true
		}
	}
	if !found {
		t.Errorf("declared local layer artifact not searchable: %+v", search.Results)
	}

	// The layer is registered. The list answers the §7.3.1 layer object,
	// whose members are lower snake_case.
	var layers struct {
		Layers []struct {
			ID         string `json:"id"`
			SourceType string `json:"source_type"`
			Public     bool   `json:"public"`
		} `json:"layers"`
	}
	getJSON(t, srv.BaseURL+"/v1/layers", &layers)
	seen := false
	for _, l := range layers.Layers {
		if l.ID != "org-defaults" {
			continue
		}
		seen = true
		if l.SourceType != "local" {
			t.Errorf("SourceType = %q, want local", l.SourceType)
		}
		if !l.Public {
			t.Errorf("Public = false, want true (declared visibility block)")
		}
	}
	if !seen {
		t.Errorf("declared layer org-defaults missing from /v1/layers: %+v", layers.Layers)
	}
}

// declaredGitProviderConfig writes a registry.yaml declaring one git layer
// with the given source.git.git_provider value (omitted when empty) and
// returns its path.
func declaredGitProviderConfig(t *testing.T, dir, id, repoURL, provider string) string {
	t.Helper()
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: " + id + "\n" +
		"      source:\n" +
		"        git:\n" +
		"          repo: " + repoURL + "\n" +
		"          ref: master\n"
	if provider != "" {
		cfg += "          git_provider: " + provider + "\n"
	}
	cfg += "" +
		"      visibility:\n" +
		"        public: true\n"
	path := filepath.Join(dir, "registry.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	return path
}

// layerGitProvider reads one layer's git_provider from the §7.3.1 layer
// object the list endpoint answers.
func layerGitProvider(t *testing.T, srv *serverProc, id string) string {
	t.Helper()
	var layers struct {
		Layers []struct {
			ID          string `json:"id"`
			GitProvider string `json:"git_provider"`
		} `json:"layers"`
	}
	getJSON(t, srv.BaseURL+"/v1/layers", &layers)
	for _, l := range layers.Layers {
		if l.ID == id {
			return l.GitProvider
		}
	}
	t.Fatalf("layer %q missing from /v1/layers: %+v", id, layers.Layers)
	return ""
}

// rotateWebhookSecret mints a fresh per-layer HMAC secret through the update
// endpoint and returns the advertised URL and secret. A declared layer carries
// no inbound secret until one is minted, and the boot reconcile erases a secret
// rotated before a restart, so an arm rotates after its final start.
func rotateWebhookSecret(t *testing.T, srv *serverProc, id string) (url, secret string) {
	t.Helper()
	st, body := apiDo(t, http.MethodPut, srv.BaseURL+"/v1/layers/update?id="+id,
		map[string]any{"rotate_webhook_secret": true})
	apiWantStatus(t, st, 200, "rotate webhook secret for "+id, body)
	var out struct {
		WebhookURL    string `json:"webhook_url"`
		WebhookSecret string `json:"webhook_secret"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode update response: %v\nbody: %s", err, body)
	}
	if out.WebhookURL == "" || out.WebhookSecret == "" {
		t.Fatalf("rotation returned no webhook url/secret: %s", body)
	}
	return out.WebhookURL, out.WebhookSecret
}

// deliverAs posts a webhook body signed under the named provider's scheme, in
// the header that provider sends the credential in, and returns the status and
// body without asserting either.
func deliverAs(t *testing.T, url, providerID, body, secret string) (int, string) {
	t.Helper()
	sig, err := layerwebhook.Sign(providerID, []byte(body), secret)
	if err != nil {
		t.Fatalf("sign as %s: %v", providerID, err)
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build delivery: %v", err)
	}
	if providerID == "gitlab" {
		req.Header.Set("X-Gitlab-Token", sig)
	} else {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	out := new(bytes.Buffer)
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.String()
}

// Spec: §7.3.1 — a declared layer's source.git.git_provider survives the boot
// reconcile, and the re-seeded value selects the signature scheme the inbound
// delivery is verified under: a GitLab-signed delivery ingests and a
// GitHub-signed one is refused with 401 ingest.webhook_invalid.
func TestDeclarativeLayers_DeclaredGitProviderSurvivesRestart(t *testing.T) {
	t.Parallel()
	repo := newGitJourneyRepo(t)
	repo.commitContextArtifact(t, "finance/ap/pay-invoice", "1.0.0",
		"pay vendor invoices from the declared git layer", "seed")

	home := t.TempDir()
	cfgPath := declaredGitProviderConfig(t, home, "team-finance", repo.url(), "gitlab")
	env := []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_SQLITE_PATH=" + filepath.Join(home, "podium.db"),
	}

	srv := startServerArgs(t, env, "serve", "--standalone")
	if got := layerGitProvider(t, srv, "team-finance"); got != "gitlab" {
		t.Fatalf("git_provider at first boot = %q, want gitlab", got)
	}
	stopProc(srv.cmd)

	// The second boot re-seeds the declared entry against the same store.
	srv2 := startServerArgs(t, env, "serve", "--standalone")
	if got := layerGitProvider(t, srv2, "team-finance"); got != "gitlab" {
		t.Fatalf("git_provider after restart = %q, want gitlab", got)
	}

	url, secret := rotateWebhookSecret(t, srv2, "team-finance")
	payload := `{"ref":"refs/heads/master"}`
	if st, body := deliverAs(t, url, "gitlab", payload, secret); st != 200 {
		t.Errorf("GitLab-signed delivery: HTTP %d, want 200\nbody: %s\nserver log:\n%s", st, body, srv2.log())
	}
	st, body := deliverAs(t, url, "github", payload, secret)
	if st != 401 {
		t.Errorf("GitHub-signed delivery to a gitlab layer: HTTP %d, want 401\nbody: %s", st, body)
	}
	if !strings.Contains(body, "ingest.webhook_invalid") {
		t.Errorf("refusal body missing ingest.webhook_invalid: %s", body)
	}
}

// Spec: §7.3.1 — the declaration is the setter for a declared layer's git
// provider: a value set over HTTP is reverted to the declared value at the
// next start, and a declared entry that omits the key re-seeds the empty
// string that resolves to github.
func TestDeclarativeLayers_HTTPGitProviderRevertsToDeclared(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		declared string
	}{
		{"declared value wins", "gitlab"},
		{"omitted key re-seeds the empty string", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			cfgPath := declaredGitProviderConfig(t, home, "team-finance",
				"git@github.com:acme/finance.git", tc.declared)
			env := []string{
				"HOME=" + home,
				"PODIUM_CONFIG_FILE=" + cfgPath,
				"PODIUM_SQLITE_PATH=" + filepath.Join(home, "podium.db"),
				"PODIUM_INGEST_OFFLINE=true",
			}

			srv := startServerArgs(t, env, "serve", "--standalone")
			if got := layerGitProvider(t, srv, "team-finance"); got != tc.declared {
				t.Fatalf("git_provider at first boot = %q, want %q", got, tc.declared)
			}
			st, body := apiDo(t, http.MethodPut, srv.BaseURL+"/v1/layers/update?id=team-finance",
				map[string]any{"git_provider": "bitbucket"})
			apiWantStatus(t, st, 200, "set git_provider over HTTP", body)
			if got := layerGitProvider(t, srv, "team-finance"); got != "bitbucket" {
				t.Fatalf("git_provider after the HTTP update = %q, want bitbucket", got)
			}
			stopProc(srv.cmd)

			srv2 := startServerArgs(t, env, "serve", "--standalone")
			if got := layerGitProvider(t, srv2, "team-finance"); got != tc.declared {
				t.Errorf("git_provider after restart = %q, want the declared %q", got, tc.declared)
			}
		})
	}
}

// Spec: §7.3.1 — a declared git_provider the registry has not registered
// aborts startup, with an error naming the layer and the value, before any
// listener binds.
func TestDeclarativeLayers_UnknownGitProviderRefusesStart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfgPath := declaredGitProviderConfig(t, home, "team-finance",
		"git@example.com:acme/finance.git", "acme-forge-that-is-not-registered")
	bind := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	res := runPodium(t, "", []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
	}, "serve", "--standalone", "--bind", bind)
	if res.Exit == 0 {
		t.Fatalf("expected a non-zero exit (refuse to start)\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	}
	out := res.Stdout + res.Stderr
	for _, want := range []string{"team-finance", "acme-forge-that-is-not-registered"} {
		if !strings.Contains(out, want) {
			t.Errorf("startup error does not name %q:\n%s", want, out)
		}
	}
}

// Spec: §7.3.1 / §4.6 — the declaration is the setter for a declared layer's
// visibility too: a narrowing applied over HTTP holds until the next start,
// where the boot re-seed restores the declared block. This is the documented
// consequence of the update endpoint gaining the ability to withdraw an axis,
// on the same terms the declared git_provider case already has.
func TestDeclarativeLayers_HTTPVisibilityNarrowingRevertsToDeclared(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	layerRoot := writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: pay vendor invoices\nsensitivity: low\n---\n\nbody\n",
	})
	cfgPath := filepath.Join(home, "registry.yaml")
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: org-defaults\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + layerRoot + "\n" +
		"      visibility:\n" +
		"        public: true\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	env := []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_SQLITE_PATH=" + filepath.Join(home, "podium.db"),
		"PODIUM_INGEST_OFFLINE=true",
	}

	srv := startServerArgs(t, env, "serve", "--standalone")
	if !layerIsPublic(t, srv, "org-defaults") {
		t.Fatalf("the declared layer is not public at the first boot")
	}
	st, body := apiDo(t, http.MethodPut, srv.BaseURL+"/v1/layers/update?id=org-defaults",
		map[string]any{"public": false})
	apiWantStatus(t, st, 200, "withdraw the declared layer's public axis", body)
	if layerIsPublic(t, srv, "org-defaults") {
		t.Fatalf("the withdrawal did not apply before the restart")
	}
	stopProc(srv.cmd)

	srv2 := startServerArgs(t, env, "serve", "--standalone")
	if !layerIsPublic(t, srv2, "org-defaults") {
		t.Errorf("the declared visibility was not restored at the next start")
	}
}

// layerIsPublic reads one layer's public axis from the §7.3.1 layer object
// the list endpoint answers.
func layerIsPublic(t *testing.T, srv *serverProc, id string) bool {
	t.Helper()
	var layers struct {
		Layers []struct {
			ID     string `json:"id"`
			Public bool   `json:"public"`
		} `json:"layers"`
	}
	getJSON(t, srv.BaseURL+"/v1/layers", &layers)
	for _, l := range layers.Layers {
		if l.ID == id {
			return l.Public
		}
	}
	t.Fatalf("layer %q missing from /v1/layers: %+v", id, layers.Layers)
	return false
}
