package e2e

// End-to-end coverage for the §4.6 declarative `layers:` registry-config list.
// A registry.yaml that declares a local-source admin layer with a
// visibility block boots a standalone server that ingests the layer and
// exposes it through the §7.3.1 layer-management API.

import (
	"bytes"
	"encoding/json"
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
	var keys []string
	if provider != "" {
		keys = append(keys, "git_provider: "+provider)
	}
	return declaredGitConfig(t, dir, id, repoURL, keys...)
}

// declaredGitConfig writes a registry.yaml declaring one public git layer on
// repoURL at ref master, with each of gitKeys written as one more line of its
// source.git block, and returns its path.
func declaredGitConfig(t *testing.T, dir, id, repoURL string, gitKeys ...string) string {
	t.Helper()
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: " + id + "\n" +
		"      source:\n" +
		"        git:\n" +
		"          repo: " + repoURL + "\n" +
		"          ref: master\n"
	for _, key := range gitKeys {
		cfg += "          " + key + "\n"
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
	defer func() { _ = resp.Body.Close() }()
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
		"PODIUM_SIGN=none",
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

// declaredCredentialEnv is the server environment the declared-credential
// tests start the binary with. The layer is never cloned, because the tests
// read what the registry reports and logs rather than what a clone sends.
func declaredCredentialEnv(home, cfgPath string, extra ...string) []string {
	return append([]string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_SQLITE_PATH=" + filepath.Join(home, "podium.db"),
		"PODIUM_SIGN=none",
		"PODIUM_INGEST_OFFLINE=true",
	}, extra...)
}

// declaredCredentialLayer reads the list endpoint and the server log of srv,
// fails the test when either carries one of parts or when the log lacks the
// seed line of id, and returns the layer object of id as the list reports it.
func declaredCredentialLayer(t *testing.T, srv *serverProc, id string, parts ...string) declaredLayerObject {
	t.Helper()
	st, body := getRaw(t, srv.BaseURL+"/v1/layers")
	if st != 200 {
		t.Fatalf("GET /v1/layers = HTTP %d, want 200\nbody: %s", st, body)
	}
	// The seed line is written before the server answers, so its presence
	// shows the absence checks below read a log that covers the boot.
	serverLog := srv.log()
	seeded := "seeded declared git layer " + id + " (repo=https://git.acme.com/acme/finance.git"
	if !strings.Contains(serverLog, seeded) {
		t.Errorf("server log missing %q:\n%s", seeded, serverLog)
	}
	for _, part := range parts {
		if strings.Contains(string(body), part) {
			t.Errorf("list response carries the credential part %q: %s", part, body)
		}
		if strings.Contains(serverLog, part) {
			t.Errorf("server log carries the credential part %q:\n%s", part, serverLog)
		}
	}
	var layers struct {
		Layers []declaredLayerObject `json:"layers"`
	}
	if err := json.Unmarshal(body, &layers); err != nil {
		t.Fatalf("decode list response: %v\nbody: %s", err, body)
	}
	for _, l := range layers.Layers {
		if l.ID == id {
			return l
		}
	}
	t.Fatalf("layer %s missing from /v1/layers: %s", id, body)
	return declaredLayerObject{}
}

// declaredLayerObject is the part of the §7.3.1 layer object the
// declared-credential tests read. RepoCredential is nil when the object omits
// the member.
type declaredLayerObject struct {
	ID             string `json:"id"`
	Repo           string `json:"repo"`
	RepoCredential *bool  `json:"repo_credential"`
}

// urlUserinfoLogLine is the start log line for a declaration that carries a
// credential in the userinfo of source.git.repo rather than in the keys.
const urlUserinfoLogLine = "declared git layer team-finance carries URL userinfo in source.git.repo"

// Spec: §7.3.1 (Declared repository credentials) — a declared git layer whose
// repo carries a credential in its URL userinfo and that sets neither
// credential key is seeded with the full value, the list endpoint of the
// running binary reports the repo with the userinfo removed and the stored
// credential through repo_credential, and the server log names the seeded
// layer, tells the operator to move the credential to the keys, and carries
// neither credential part.
func TestDeclarativeLayers_RepoCredentialNotReported(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfgPath := declaredGitProviderConfig(t, home, "team-finance",
		"https://alice-user:s3cr3tpw@git.acme.com/acme/finance.git", "")
	srv := startServerArgs(t, declaredCredentialEnv(home, cfgPath), "serve", "--standalone")

	l := declaredCredentialLayer(t, srv, "team-finance", "alice-user", "s3cr3tpw")
	if !strings.Contains(srv.log(), urlUserinfoLogLine) {
		t.Errorf("server log missing %q:\n%s", urlUserinfoLogLine, srv.log())
	}
	if want := "https://git.acme.com/acme/finance.git"; l.Repo != want {
		t.Errorf("repo = %q, want %q", l.Repo, want)
	}
	if l.RepoCredential == nil || !*l.RepoCredential {
		t.Errorf("repo_credential = %v, want true", l.RepoCredential)
	}
}

// Spec: §7.3.1 (Declared repository credentials) — a declared git layer that
// names its credential in source.git.username and source.git.token, the token
// interpolated from the server environment, is seeded with the credential
// joined into its repo. The list endpoint reports the repo without userinfo and
// the stored credential through repo_credential, and the server log carries
// neither credential part and no line about URL userinfo.
func TestDeclarativeLayers_RepoCredentialKeys(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfgPath := declaredGitConfig(t, home, "team-finance", "https://git.acme.com/acme/finance.git",
		"username: alice-user", "token: '${PODIUM_E2E_GIT_TOKEN}'")
	srv := startServerArgs(t, declaredCredentialEnv(home, cfgPath, "PODIUM_E2E_GIT_TOKEN=s3cr3tpw"),
		"serve", "--standalone")

	l := declaredCredentialLayer(t, srv, "team-finance", "alice-user", "s3cr3tpw")
	if strings.Contains(srv.log(), "carries URL userinfo") {
		t.Errorf("server log carries the URL userinfo line for a declaration that uses the keys:\n%s", srv.log())
	}
	if want := "https://git.acme.com/acme/finance.git"; l.Repo != want {
		t.Errorf("repo = %q, want %q", l.Repo, want)
	}
	if l.RepoCredential == nil || !*l.RepoCredential {
		t.Errorf("repo_credential = %v, want true", l.RepoCredential)
	}
}

// Spec: §7.3.1 (Declared repository credentials) — a declaration that sets a
// credential key aborts startup when source.git.repo carries userinfo of its
// own and when source.git.token is empty, which an unset ${ENV_VAR} yields.
// The error names the layer and the key and carries no credential value.
func TestDeclarativeLayers_RepoCredentialRefusesStart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		repo    string
		keys    []string
		env     []string
		want    string
		secrets []string
	}{
		{
			name:    "userinfo beside the token key",
			repo:    "https://alice-user:s3cr3tpw@git.acme.com/acme/finance.git",
			keys:    []string{"token: '${PODIUM_E2E_GIT_TOKEN}'"},
			env:     []string{"PODIUM_E2E_GIT_TOKEN=tok-e2e-fixture"},
			want:    "remove the userinfo from source.git.repo",
			secrets: []string{"alice-user", "s3cr3tpw", "tok-e2e-fixture"},
		},
		{
			name:    "token from an unset variable",
			repo:    "https://git.acme.com/acme/finance.git",
			keys:    []string{"username: alice-user", "token: '${PODIUM_E2E_UNSET_TOKEN}'"},
			want:    "source.git.token",
			secrets: []string{"alice-user"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			cfgPath := declaredGitConfig(t, home, "team-finance", tc.repo, tc.keys...)
			// The boot aborts before the listener binds, so port 0 suffices.
			res := runPodium(t, "", declaredCredentialEnv(home, cfgPath, tc.env...),
				"serve", "--standalone", "--bind", "127.0.0.1:0")
			if res.Exit == 0 {
				t.Fatalf("expected a non-zero exit (refuse to start)\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			}
			out := res.Stdout + res.Stderr
			for _, want := range []string{"team-finance", tc.want} {
				if !strings.Contains(out, want) {
					t.Errorf("startup error does not carry %q:\n%s", want, out)
				}
			}
			for _, secret := range tc.secrets {
				if strings.Contains(out, secret) {
					t.Errorf("startup output carries the credential fixture %q:\n%s", secret, out)
				}
			}
		})
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
				"PODIUM_SIGN=none",
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
	// The boot aborts before the listener binds, so port 0 suffices.
	res := runPodium(t, "", []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
	}, "serve", "--standalone", "--bind", "127.0.0.1:0")
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
		"PODIUM_SIGN=none",
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
