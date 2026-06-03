package e2e

import (
	"testing"
)

// End-to-end tests for spec §14.10 (standalone registry with a Git-source
// layer). `podium serve --standalone` bootstraps ~/.podium/sync.yaml with
// defaults.registry pointing at the local server, and the subsequent
// `podium layer` commands resolve that registry with no explicit --registry
// flag (F-14.10.1). `layer register` prints an absolute webhook URL on its own
// labeled line (F-14.10.2).

// spec §14.10 steps 2-3 (F-14.10.1, F-14.10.2): register and reingest resolve
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
	// URL on its own labeled line, built from the server's public base URL.
	git := runPodium(t, cwd, env, "layer", "register",
		"--id", "community-skills",
		"--repo", "https://github.com/podium-community/skills.git", "--ref", "main")
	cliWantExit(t, git, 0, "git layer register resolves bootstrapped registry")
	wantWebhook := "webhook URL: " + srv.BaseURL + "/v1/ingest/webhook/community-skills"
	cliContains(t, git.Stdout, wantWebhook, "absolute webhook URL on labeled line")
}

// spec §14.10 (F-14.10.1): with no registry configured anywhere (clean HOME,
// empty PODIUM_REGISTRY, no --registry), layer register refuses with exit 2 and
// names the resolution sources.
func TestStandaloneLayer_NoRegistryAnywhereRefuses(t *testing.T) {
	t.Parallel()
	env := []string{"HOME=" + t.TempDir(), "PODIUM_REGISTRY="}
	res := runPodium(t, t.TempDir(), env, "layer", "register", "--id", "x", "--local", t.TempDir())
	cliWantExit(t, res, 2, "no registry configured")
	cliContains(t, res.Stderr, "--registry is required", "missing registry error")
}
