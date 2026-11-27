package e2e

// End-to-end coverage for the warnings a registry that verifies no caller
// gives about the layer visibility it stores and does not apply: the §13.10
// startup log line and the §7.3.1 warning that `podium layer register`
// writes to standard error.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unappliedVisibilityLine is the fragment the §13.10 startup warning carries.
const unappliedVisibilityLine = "applies no layer visibility because it verifies no caller"

// writeDeclaredLayers writes a registry.yaml under home that declares a
// public layer and a layer whose visibility is engVisibility, and returns
// its path.
func writeDeclaredLayers(t *testing.T, home, engVisibility string) string {
	t.Helper()
	pubRoot := writeRegistry(t, map[string]string{"welcome/ARTIFACT.md": agentArtifact("public welcome")})
	engRoot := writeRegistry(t, map[string]string{"secret/ARTIFACT.md": agentArtifact("engineering secret")})
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: public-layer\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + pubRoot + "\n" +
		"      visibility:\n" +
		"        public: true\n" +
		"    - id: eng-layer\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + engRoot + "\n" +
		"      visibility:\n" +
		"        " + engVisibility + "\n"
	cfgPath := filepath.Join(home, "registry.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	return cfgPath
}

// unappliedVisibilityWarningLine returns the server log's §13.10 startup
// warning line, or "" when the log carries none.
func unappliedVisibilityWarningLine(log string) string {
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, unappliedVisibilityLine) {
			return line
		}
	}
	return ""
}

// Spec: §13.10 (Startup warning for unapplied visibility) — a standalone
// registry with no identity provider logs one warning at startup naming the
// stored layers that set no public grant, and the line follows the boot seed
// of the declared layers: once the declaration is changed to public and the
// registry restarted, the line is gone.
// Spec: §4.6 (No-identity bypass)
func TestUnappliedVisibility_StartupLogFollowsTheDeclaredLayers(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	env := []string{"HOME=" + home, "PODIUM_INGEST_OFFLINE=true"}

	cfgPath := writeDeclaredLayers(t, home, "groups: [engineering]")
	srv := startServerArgs(t, append(env, "PODIUM_CONFIG_FILE="+cfgPath), "serve", "--standalone")
	line := unappliedVisibilityWarningLine(srv.log())
	if line == "" {
		t.Fatalf("server log carries no unapplied-visibility warning\nlog:\n%s", srv.log())
	}
	for _, want := range []string{"warning: 1 stored layer(s)", "every caller sees them", "once an identity provider is configured", ": eng-layer"} {
		if !strings.Contains(line, want) {
			t.Errorf("warning line %q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "public-layer") {
		t.Errorf("warning line %q lists the public layer", line)
	}
	stopProc(srv.cmd)

	cfgPath = writeDeclaredLayers(t, home, "public: true")
	srv = startServerArgs(t, append(env, "PODIUM_CONFIG_FILE="+cfgPath), "serve", "--standalone")
	if line := unappliedVisibilityWarningLine(srv.log()); line != "" {
		t.Errorf("after the declaration became public the log still warns: %q", line)
	}
}

// Spec: §13.10 (Startup warning for unapplied visibility) — public mode
// logs the line in addition to its banner.
func TestUnappliedVisibility_PublicModeLogsTheList(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfgPath := writeDeclaredLayers(t, home, "organization: true")
	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_PUBLIC_MODE=true",
	}, "serve", "--standalone")
	log := srv.log()
	if !strings.Contains(log, "PUBLIC MODE") {
		t.Errorf("server log lacks the public-mode banner\nlog:\n%s", log)
	}
	if line := unappliedVisibilityWarningLine(log); !strings.Contains(line, "eng-layer") {
		t.Errorf("public-mode warning line = %q, want it to list eng-layer\nlog:\n%s", line, log)
	}
}

// Spec: §13.10 (Startup warning for unapplied visibility) — a registry that
// wires a request-time verifier applies visibility and logs no such line,
// although it stores a group-restricted layer.
func TestUnappliedVisibility_VerifierLogsNoWarning(t *testing.T) {
	t.Parallel()
	srv := gwTrustedHeadersServer(t, "", "")
	if line := unappliedVisibilityWarningLine(srv.log()); line != "" {
		t.Errorf("trusted-headers registry logged %q, want no unapplied-visibility warning", line)
	}
}

// Spec: §7.3.1 (Unapplied-visibility warning) — on a registry with no
// identity provider, `podium layer register --user-defined --owner` succeeds,
// and the CLI writes the response's warning to standard error: the layer's
// users entry is not applied, so every caller sees it. On the docker-compose
// default PODIUM_DEFAULT_LAYER_VISIBILITY=private, a registration that
// declares nothing is warned as private, and a public registration carries
// no warning.
// Spec: §4.6 (No-identity bypass)
func TestUnappliedVisibility_RegisterWarnsOnStderr(t *testing.T) {
	t.Parallel()
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_DEFAULT_LAYER_VISIBILITY=private",
	}, "serve", "--standalone")

	register := func(args ...string) cliResult {
		t.Helper()
		res := runPodium(t, "", nil, append([]string{"layer", "register", "--registry", srv.BaseURL}, args...)...)
		if res.Exit != 0 {
			t.Fatalf("layer register %v exit = %d\nstdout:\n%s\nstderr:\n%s", args, res.Exit, res.Stdout, res.Stderr)
		}
		return res
	}

	res := register("--id", "alice-personal", "--local", t.TempDir(), "--user-defined", "--owner", "alice@acme.com")
	want := `warning: layer "alice-personal" declares visibility users: [alice@acme.com], but this registry applies no layer visibility because it verifies no caller, so every caller sees the layer; the declaration takes effect once an identity provider is configured`
	if !strings.Contains(res.Stderr, want) {
		t.Errorf("user-defined register stderr = %q, want %q", res.Stderr, want)
	}
	if strings.Contains(res.Stdout, "warning: ") {
		t.Errorf("stdout = %q, want the response body alone", res.Stdout)
	}

	res = register("--id", "team-default", "--local", t.TempDir())
	if !strings.Contains(res.Stderr, `warning: layer "team-default" is private`) ||
		!strings.Contains(res.Stderr, "the private declaration applies once an identity provider is added") {
		t.Errorf("private-default register stderr = %q, want the private warning", res.Stderr)
	}

	res = register("--id", "open", "--local", t.TempDir(), "--public")
	if strings.Contains(res.Stderr, "warning: ") {
		t.Errorf("public register stderr = %q, want no warning", res.Stderr)
	}
}
