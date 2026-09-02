package e2e

// End-to-end coverage of the §7.3.1 local-source rule through the compiled
// binary: the CLI refusal a non-admin caller meets on `podium layer register
// --local`, and the ingest confinement on the two bootstrap paths, which run
// only inside the spawned process and abort startup before any listener binds.
//
// The authenticated cases use the injected-session-token harness rather than
// the oidc-jwt stack, which skips on darwin.
//
// Spec: §7.3.1 (local-source authorization and ingest confinement), §13.11.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// Spec: §7.3.1 — registering a layer whose source is a filesystem path on the
// registry host is a tenant admin's operation wherever a caller is
// authenticated at all. The CLI is where an operator meets the refusal, so the
// coded envelope and the constraint that names the rule have to survive to its
// stderr; a bare non-zero exit does not tell the operator which rule refused.
func TestLayerCLI_LocalRefusedForNonAdmin(t *testing.T) {
	t.Parallel()
	srv := startAuthServer(t, authServerSpec{
		BootstrapAdmins: []string{"alice@acme.com"},
		Layers: []authLayer{{
			ID:         "seed",
			Files:      map[string]string{"seed/note/ARTIFACT.md": authContext("seed note")},
			Visibility: authVisibility{Public: true},
		}},
	})
	root := writeRegistry(t, map[string]string{
		"ops/runbook/ARTIFACT.md": authContext("an operations runbook"),
	})

	// ---- A verified non-admin is refused, with the rule named ---------------
	bobToken := srv.token(authIdentity{Sub: "bob@acme.com", Email: "bob@acme.com"})
	refused := runPodium(t, "", acliEnv(t, srv, bobToken),
		"layer", "register", "--id", "bob-local", "--local", root)
	cliWantNonZero(t, refused, "non-admin local registration")
	for _, want := range []string{"403", "auth.forbidden", "local_source"} {
		if !strings.Contains(refused.Stderr, want) {
			t.Errorf("non-admin local registration: stderr does not carry %q\nstderr: %s", want, refused.Stderr)
		}
	}

	// ---- The bootstrap admin's identical invocation succeeds ----------------
	adminToken := srv.adminToken("alice@acme.com")
	admitted := runPodium(t, "", acliEnv(t, srv, adminToken),
		"layer", "register", "--id", "ops", "--local", root)
	cliWantExit(t, admitted, 0, "admin local registration")

	// ---- A stack with no identity provider admits every caller -------------
	// It authenticates no one, so no caller holds the admin role and the rule
	// admits the operation rather than closing the local operator out of a
	// deployment that has no way to grant the role.
	std := startServer(t, writeRegistry(t, map[string]string{
		"seed/note/ARTIFACT.md": authContext("seed note"),
	}))
	stdEnv := []string{
		"PODIUM_REGISTRY=" + std.BaseURL,
		"PODIUM_TOKEN_KEYCHAIN_NAME=podium-local-source-test",
		"HOME=" + t.TempDir(),
	}
	anon := runPodium(t, "", stdEnv, "layer", "register", "--id", "anon-local", "--local", root)
	cliWantExit(t, anon, 0, "uncredentialed local registration against a registry with no identity provider")
}

// bootLayerTree writes a local layer directory holding one artifact and one
// bundled resource symbolically linked to a file outside the root, and returns
// the layer root and the path of the link. The root's base name is the layer
// id the filesystem bootstrap derives, so the caller can assert the failure
// names it.
func bootLayerTree(t *testing.T, id string) (root, link string) {
	t.Helper()
	parent := t.TempDir()
	root = filepath.Join(parent, id)
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "ops/runbook/ARTIFACT.md", Content: authContext("an operations runbook")},
	)
	if err := os.WriteFile(filepath.Join(parent, "outside.txt"), []byte(bootEscapedBytes+"\n"), 0o644); err != nil {
		t.Fatalf("write the file outside the root: %v", err)
	}
	link = filepath.Join(root, "ops", "runbook", "leak.txt")
	if err := os.Symlink(filepath.Join("..", "..", "..", "outside.txt"), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	return root, link
}

// bootEscapedBytes is the content of the file the link leaves the layer
// directory to reach. No response body may carry it.
const bootEscapedBytes = "OUTSIDE-SECRET-PAYLOAD"

// serveExpectBootFailure runs `podium serve <args> --bind 127.0.0.1:<free>`
// under a hard deadline, expecting the boot to abort before the listener
// binds, and returns the combined output. It asserts the non-zero exit and the
// unbound port itself, so a caller states only what the output must name.
func serveExpectBootFailure(t *testing.T, env []string, args ...string) string {
	t.Helper()
	bind := localBind(freePort(t))
	full := append(append([]string{}, args...), "--bind", bind)
	res := runBin(t, cmdharness.Bin(t, "podium"), "", append(env, "PODIUM_NO_AUTOSTANDALONE=1"), nil, 30*time.Second, full...)
	out := res.Stdout + res.Stderr
	if res.Exit == 0 {
		t.Fatalf("serve exited 0; want a non-zero abort before the listener binds\noutput:\n%s", out)
	}
	if st := getStatusNoFatal("http://" + bind + "/healthz"); st == 200 {
		t.Fatalf("serve bound %s instead of aborting the boot\noutput:\n%s", bind, out)
	}
	return out
}

// assertNoEscapedBytes reads the artifact the layer holds in its own directory
// and the discovery surface, and fails if either carries what the removed link
// pointed at.
func assertNoEscapedBytes(t *testing.T, srv *serverProc) {
	t.Helper()
	st, body := srv.getMaybeAuth(t, srv.BaseURL+"/v1/load_artifact?id=ops/runbook")
	if st != 200 {
		t.Fatalf("load_artifact = %d, want the in-root artifact served\nbody: %s\nlog:\n%s", st, body, srv.log())
	}
	for _, path := range []string{"/v1/load_artifact?id=ops/runbook", "/v1/search_artifacts?query=runbook"} {
		_, b := srv.getMaybeAuth(t, srv.BaseURL+path)
		if strings.Contains(string(b), bootEscapedBytes) {
			t.Errorf("GET %s carries the bytes from outside the layer directory\nbody: %s", path, b)
		}
	}
}

// Spec: §7.3.1 — the ingest confinement binds the bootstrap paths too. Both
// build the layer's tree in the boot sequence rather than through the source
// provider, so confining the provider alone would leave the same directory
// unconfined when it is bootstrapped, which is a deployment-mode divergence.
// The boot runs the ingest against a background context before any listener
// binds, so a refused read aborts startup.
//
// The failure has to name the layer and carry the rendered sentinel text.
// The bootstrap path writes no HTTP envelope, so the code string
// ingest.source_unreachable never appears; what a reader sees is the wrapped
// message.
func TestBootstrapLayer_IngestIsConfined(t *testing.T) {
	t.Parallel()

	t.Run("layer path", func(t *testing.T) {
		t.Parallel()
		root, link := bootLayerTree(t, "escaping-layer")
		env := []string{"HOME=" + t.TempDir(), "PODIUM_INGEST_OFFLINE=true"}

		out := serveExpectBootFailure(t, env, "serve", "--standalone", "--layer-path", root)
		for _, want := range []string{"escaping-layer", "source: unreachable"} {
			if !strings.Contains(out, want) {
				t.Errorf("boot failure does not name %q\noutput:\n%s", want, out)
			}
		}

		// The control: the same tree with the link removed boots and serves
		// the artifact that lives inside the layer directory.
		if err := os.Remove(link); err != nil {
			t.Fatalf("remove the link: %v", err)
		}
		srv := startServerArgs(t, env, "serve", "--standalone", "--layer-path", root)
		assertNoEscapedBytes(t, srv)
	})

	t.Run("declared local source", func(t *testing.T) {
		t.Parallel()
		root, link := bootLayerTree(t, "declared-layer")
		home := t.TempDir()
		cfgPath := filepath.Join(home, "registry.yaml")
		cfg := "registry:\n  layers:\n    - id: declared-layer\n      source:\n        local:\n          path: " + root + "\n      visibility:\n        public: true\n"
		if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
			t.Fatalf("write registry.yaml: %v", err)
		}
		env := []string{"HOME=" + home, "PODIUM_INGEST_OFFLINE=true"}

		out := serveExpectBootFailure(t, env, "serve", "--standalone", "--config", cfgPath)
		for _, want := range []string{"declared-layer", "source: unreachable"} {
			if !strings.Contains(out, want) {
				t.Errorf("boot failure does not name %q\noutput:\n%s", want, out)
			}
		}

		if err := os.Remove(link); err != nil {
			t.Fatalf("remove the link: %v", err)
		}
		srv := startServerArgs(t, env, "serve", "--standalone", "--config", cfgPath)
		assertNoEscapedBytes(t, srv)
	})
}
