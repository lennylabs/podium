package e2e

// End-to-end coverage for the §4.7.2 admin diagnostic visibility override:
// "View any layer's contents for diagnostic purposes (override visibility;
// the override is itself audited)." The standalone server runs in
// injected-session-token mode so callers carry verified identities; a
// bootstrap admin grant makes alice an admin. A declared layer restricted to
// bob holds one artifact that alice cannot see through her own effective
// view. Passing as_admin=1 overrides that for the admin and is rejected for
// everyone else.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestAdminVisibilityOverride_E2E(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	priv, pemPath := injKeyPair(t)

	// A restricted layer visible only to bob, holding one artifact.
	layerRoot := writeRegistry(t, map[string]string{
		"finance/secret/ledger/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: restricted ledger\nsensitivity: low\n---\n\nbody\n",
	})
	cfgPath := filepath.Join(home, "registry.yaml")
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: restricted\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + layerRoot + "\n" +
		"      visibility:\n" +
		"        users: [bob@acme.com]\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
		"PODIUM_BOOTSTRAP_ADMINS=alice@acme.com",
	}, "serve", "--standalone")
	injRegisterRuntime(t, srv, pemPath)

	aliceToken := injSignJWT(t, priv, injClaims("alice@acme.com")) // bootstrap admin
	carolToken := injSignJWT(t, priv, injClaims("carol@acme.com")) // authenticated non-admin

	const id = "finance/secret/ledger"
	loadURL := srv.BaseURL + "/v1/load_artifact?id=" + id

	// alice (admin) is not in the restricted layer, so a normal load is filtered.
	if st, body := injGet(t, loadURL, aliceToken); st != http.StatusNotFound {
		t.Errorf("alice normal load = %d, want 404 (body=%s)", st, body)
	}

	// as_admin=1 overrides visibility for the admin.
	if st, body := injGet(t, loadURL+"&as_admin=1", aliceToken); st != http.StatusOK {
		t.Errorf("alice override load = %d, want 200 (body=%s)", st, body)
	}

	// A verified non-admin cannot use the override.
	if st, _ := injGet(t, loadURL+"&as_admin=1", carolToken); st != http.StatusForbidden {
		t.Errorf("carol override load = %d, want 403", st)
	}

	// An unauthenticated override request never succeeds.
	if st, _ := injGet(t, loadURL+"&as_admin=1", ""); st == http.StatusOK {
		t.Errorf("unauthenticated override load = 200, want a rejection")
	}

	// The search override surfaces the otherwise-invisible artifact for the admin.
	st, body := injGet(t, srv.BaseURL+"/v1/search_artifacts?query=&as_admin=1", aliceToken)
	if st != http.StatusOK {
		t.Fatalf("alice override search = %d, want 200 (body=%s)", st, body)
	}
	var search struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &search); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	found := false
	for _, r := range search.Results {
		if r.ID == id {
			found = true
		}
	}
	if !found {
		t.Errorf("override search missing %s: %+v", id, search.Results)
	}

	// Without the override, alice's search excludes the restricted artifact.
	st, body = injGet(t, srv.BaseURL+"/v1/search_artifacts?query=", aliceToken)
	if st != http.StatusOK {
		t.Fatalf("alice normal search = %d, want 200 (body=%s)", st, body)
	}
	search.Results = nil
	if err := json.Unmarshal(body, &search); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	for _, r := range search.Results {
		if r.ID == id {
			t.Errorf("normal search leaked %s: %+v", id, search.Results)
		}
	}
}
