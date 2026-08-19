package e2e

// The §6.3.2 trusted runtime signing-key set is a local file the registry
// reads at startup, and `podium admin runtime register --keys-file` is the
// command that writes it. This test closes the loop over the real binary:
// the CLI writes the file, the registry boots from it, and a token signed
// with the matching private key verifies.

import (
	"net/http"
	"path/filepath"
	"testing"
)

// Spec: §6.3.2, §13.12 — a keys file written by the CLI is the trusted set a
// registry boots from, and a token signed by the corresponding private key is
// accepted on the first request.
func TestAdminRuntimeRegister_SeedsABootableKeysFile(t *testing.T) {
	t.Parallel()
	priv, pemPath := injKeyPair(t)
	keysPath := filepath.Join(t.TempDir(), "runtime-keys.json")
	home := t.TempDir()

	res := runPodium(t, t.TempDir(), []string{"HOME=" + home},
		"admin", "runtime", "register",
		"--keys-file", keysPath,
		"--issuer", injIssuer,
		"--algorithm", "RS256",
		"--public-key-file", pemPath)
	if res.Exit != 0 {
		t.Fatalf("admin runtime register exit = %d\nstdout: %s\nstderr: %s", res.Exit, res.Stdout, res.Stderr)
	}

	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
		"PODIUM_RUNTIME_KEYS_PATH=" + keysPath,
		"PODIUM_DEFAULT_LAYER_VISIBILITY=public",
	}, "serve", "--standalone", "--layer-path", apiReg(t))

	token := injSignJWT(t, priv, injClaims("alice"))
	status, body := injGet(t, srv.BaseURL+"/v1/search_artifacts?query=variance", token)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a token signed by the registered key\nbody: %s", status, body)
	}
}
