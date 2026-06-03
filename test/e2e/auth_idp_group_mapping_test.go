package e2e

// End-to-end coverage for the §6.3.1 IdpGroupMapping adapter. For IdPs without
// SCIM, the adapter reads raw OIDC group claims from the verified token and
// maps them to layer group names per a registry-side configuration
// (PODIUM_IDP_GROUP_MAPPING). This test mints a token whose `groups` claim
// carries the raw IdP group value (an opaque Okta/Entra group id), and proves
// the server maps it to the friendly group name before evaluating §4.6
// visibility, so the caller sees a groups-restricted layer. A token whose
// group has no mapping entry passes through unchanged and stays out of the
// restricted layer.
//
// No existing test drives PODIUM_IDP_GROUP_MAPPING through the verifier; the
// mapping's unit behavior is covered in pkg/identity but the wired path was a
// genuine e2e gap. SCIM is intentionally left unset so the mapping path is
// isolated from the SCIM directory path.
//
// Identifiers here are prefixed idpmap* so they do not collide with sibling
// auth tests in package e2e.

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// idpmapRawGroup is an opaque IdP group id of the form an Okta/Entra token
// carries, distinct from the friendly layer group name it maps to.
const (
	idpmapRawGroup  = "00g1financeOID"
	idpmapLayerName = "finance"
)

// Spec: §6.3.1 — "the IdpGroupMapping adapter reads OIDC group claims from the
// token and maps them to group names per a registry-side configuration." A
// verified token carrying the raw IdP group value reaches a layer restricted
// to the mapped friendly name; an unmapped group value does not.
func TestAuthIdpGroupMapping_RawClaimMapsToLayerGroup(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	priv, pemPath := injKeyPair(t)

	// A layer restricted to the friendly group name "finance".
	layerRoot := writeRegistry(t, map[string]string{
		"finance/forecast/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: finance forecast\n---\n\nbody\n",
	})
	cfgPath := filepath.Join(home, "registry.yaml")
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: finance\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + layerRoot + "\n" +
		"      visibility:\n" +
		"        groups: [" + idpmapLayerName + "]\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
		// Map the raw IdP group id to the friendly layer group name. SCIM is
		// deliberately unset so visibility can only come from this mapping.
		"PODIUM_IDP_GROUP_MAPPING=" + idpmapRawGroup + "=" + idpmapLayerName,
	}, "serve", "--standalone")
	injRegisterRuntime(t, srv, pemPath)

	const artifactURL = "/v1/load_artifact?id=finance/forecast"

	// alice's token carries the RAW IdP group id, not the friendly name. The
	// verifier applies the mapping (00g1financeOID -> finance) before
	// visibility evaluation, so the finance layer is visible.
	mappedClaims := injClaims("alice@acme.com")
	mappedClaims["groups"] = []string{idpmapRawGroup}
	mappedToken := injSignJWT(t, priv, mappedClaims)
	if status, body := injGet(t, srv.BaseURL+artifactURL, mappedToken); status != http.StatusOK {
		t.Fatalf("mapped-group caller load = %d, want 200 (raw id should map to %q)\nbody: %s\nlog:\n%s",
			status, idpmapLayerName, body, srv.log())
	}

	// A caller whose group has no mapping entry passes through unchanged. The
	// unmapped value is not the layer's group, so the layer stays invisible.
	unmappedClaims := injClaims("bob@acme.com")
	unmappedClaims["groups"] = []string{"00gUNMAPPEDxyz"}
	unmappedToken := injSignJWT(t, priv, unmappedClaims)
	if status, _ := injGet(t, srv.BaseURL+artifactURL, unmappedToken); status != http.StatusNotFound {
		t.Errorf("unmapped-group caller load = %d, want 404 (no mapping, no visibility)", status)
	}

	// A caller presenting the already-friendly name when only the raw id is
	// mapped: the friendly name has no mapping entry, so it passes through as
	// "finance" and still matches the layer. This confirms pass-through leaves
	// an already-correct value intact rather than dropping it.
	directClaims := injClaims("carol@acme.com")
	directClaims["groups"] = []string{idpmapLayerName}
	directToken := injSignJWT(t, priv, directClaims)
	if status, body := injGet(t, srv.BaseURL+artifactURL, directToken); status != http.StatusOK {
		t.Errorf("friendly-name caller load = %d, want 200 (pass-through preserves %q)\nbody: %s",
			status, idpmapLayerName, body)
	}
}
