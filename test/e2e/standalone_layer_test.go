package e2e

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// End-to-end tests for spec §14.10 (standalone registry with a Git-source
// layer). `podium serve --standalone` bootstraps ~/.podium/sync.yaml with
// defaults.registry pointing at the local server, and the subsequent
// `podium layer` commands resolve that registry with no explicit --registry
// flag. `layer register` prints an absolute webhook URL on its own
// labeled line.

// spec §14.10 steps 2-3: register and reingest resolve
// the bootstrapped registry without --registry, and a git register prints the
// absolute webhook URL.
func TestStandaloneLayer_ResolvesBootstrappedRegistry(t *testing.T) {
	srv := startServer(t, "")

	// A clean working directory (no workspace .podium) so only the bootstrapped
	// ~/.podium/sync.yaml under the server's HOME contributes. Empty
	// PODIUM_REGISTRY forces the merged-config fallback path.
	cwd := t.TempDir()
	env := []string{"HOME=" + srv.Home, "PODIUM_REGISTRY="}

	// A local layer staged with one artifact so reingest has content to load.
	lp := writeRegistry(t, map[string]string{
		"finance/forecast/ARTIFACT.md": contextArtifact("A standalone local layer artifact for the section 14.10 fallback test."),
	})

	// register (no --registry) resolves the registry from the bootstrapped sync.yaml.
	reg := runPodium(t, cwd, env, "layer", "register", "--id", "personal-local", "--local", lp)
	cliWantExit(t, reg, 0, "layer register resolves bootstrapped registry")
	cliContains(t, reg.Stdout, "personal-local", "registered layer id")

	// reingest (no --registry) resolves it too and loads the staged artifact.
	ri := runPodium(t, cwd, env, "layer", "reingest", "personal-local")
	cliWantExit(t, ri, 0, "layer reingest resolves bootstrapped registry")
	cliContains(t, ri.Stdout, "finance/forecast", "reingested artifact id")

	// register a git layer (no --registry): the CLI prints the absolute webhook
	// URL on its own labeled line on stderr, built from the server's public base
	// URL, while stdout stays a single decodable JSON object.
	git := runPodium(t, cwd, env, "layer", "register",
		"--id", "community-skills",
		"--repo", "https://github.com/podium-community/skills.git", "--ref", "main")
	cliWantExit(t, git, 0, "git layer register resolves bootstrapped registry")
	wantWebhook := "webhook URL: " + srv.BaseURL + "/v1/ingest/webhook/community-skills"
	cliContains(t, git.Stderr, wantWebhook, "absolute webhook URL on labeled line")
}

// spec §14.10: with no registry configured anywhere (clean HOME,
// empty PODIUM_REGISTRY, no --registry), layer register refuses with exit 2 and
// names the resolution sources.
func TestStandaloneLayer_NoRegistryAnywhereRefuses(t *testing.T) {
	t.Parallel()
	env := []string{"HOME=" + t.TempDir(), "PODIUM_REGISTRY="}
	res := runPodium(t, t.TempDir(), env, "layer", "register", "--id", "x", "--local", t.TempDir())
	cliWantExit(t, res, 2, "no registry configured")
	cliContains(t, res.Stderr, "--registry is required", "missing registry error")
}

// spec: §7.3.1 — the layer-write authorization rule is not live on a registry
// that configures no identity provider, which is the standalone posture
// §13.10's web UI targets. `podium layer register --user-defined --owner
// alice` stores a user-defined layer whose owner is alice, and the local
// operator, who resolves no subject, still unregisters it. The list read
// confirms the stored class and owner first, so the unregister reaches the
// user-defined branch the carve-out governs rather than the admin-defined
// branch that is permissive on its own. --user-defined is required in the
// invocation: the CLI sends owner only inside that branch, so a bare --owner
// would register an admin-defined layer with an empty owner.
func TestStandaloneLayer_UserDefinedOwnerManagedByLocalOperator(t *testing.T) {
	t.Parallel()
	lp := writeRegistry(t, map[string]string{
		"finance/forecast/ARTIFACT.md": contextArtifact("An artifact in alice's user-defined layer."),
	})
	srv := startServer(t, "")

	reg := runPodium(t, "", nil, "layer", "register",
		"--id", "alice-personal", "--local", lp,
		"--user-defined", "--owner", "alice@acme.com",
		"--registry", srv.BaseURL)
	cliWantExit(t, reg, 0, "user-defined layer register on a standalone registry")

	// The list response marshals store.LayerConfig, whose fields carry no
	// JSON tags, so the class and the owner are spelled Go-side.
	listed := standaloneLayerList(t, srv.BaseURL)
	for _, want := range []string{`"ID":"alice-personal"`, `"UserDefined":true`, `"Owner":"alice@acme.com"`} {
		if !strings.Contains(listed, want) {
			t.Fatalf("layer list missing %s:\n%s", want, listed)
		}
	}

	un := runPodium(t, "", nil, "layer", "unregister", "--registry", srv.BaseURL, "alice-personal")
	cliWantExit(t, un, 0, "local operator unregisters a user-defined layer owned by alice")
	if after := standaloneLayerList(t, srv.BaseURL); strings.Contains(after, "alice-personal") {
		t.Errorf("layer still listed after unregister:\n%s", after)
	}
}

// standaloneLayerList reads GET /v1/layers with the whitespace between JSON
// tokens removed, so a field assertion is one substring.
func standaloneLayerList(t *testing.T, baseURL string) string {
	t.Helper()
	resp, err := http.Get(baseURL + "/v1/layers")
	if err != nil {
		t.Fatalf("GET /v1/layers: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /v1/layers: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/layers = %d: %s", resp.StatusCode, body)
	}
	return strings.Join(strings.Fields(string(body)), "")
}
