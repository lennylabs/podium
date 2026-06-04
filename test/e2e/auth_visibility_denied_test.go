package e2e

// Per-caller visibility denial across the read surface (gap G-AUTH-16).
//
// The standalone e2e harness serves every layer public and resolves every
// caller to system:public, so the visibility-denial path on search_artifacts,
// load_artifact, and the /objects route could not be triggered. The
// authenticated, visibility-capable harness (G-INFRA-5) places a
// query-matching artifact in a restricted (users:) layer and a matching
// artifact in a public layer, then drives the identical read surface as an
// unauthorized caller and as the owner.
//
// The spec model: an artifact the caller cannot see is omitted from search
// (§4.6 the discovery surface is the caller's effective view); a direct
// load_artifact of it returns registry.not_found rather than 403 so the hidden
// layer's existence does not leak (§6.9 "without leaking the layer's
// existence"); the filesystem /objects route re-checks visibility on every
// fetch and refuses a caller who cannot resolve an owning artifact (§13.11).
// The visibility.denied code itself is the registry audit-stream event (§8.1 /
// §8.2 the audit table records visibility.denied for the rejected call), not
// the wire body — the wire body withholds existence. The owner sees the
// artifact in search, loads it, and fetches its bytes.
//
// Spec: §4.6 (the visibility evaluator and the effective-view discovery
// surface), §6.9 (visibility denial returns a structured error without leaking
// the layer's existence; logged as visibility.denied), §8.1 / §8.2 (the
// visibility.denied audit event), §13.10 / §13.11 (the token-bound /objects
// route re-checks visibility per fetch). Gap G-AUTH-16.

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// vdAbove returns a deterministic blob above the §4.2 inline cutoff so the
// restricted artifact externalizes a resource to the /objects route, giving the
// test a token-bound object URL to deny.
func vdAbove() string { return strings.Repeat("V", 256*1024+2048) }

// TestAuthVisibilityDenied_SearchLoadObjects places a query-matching artifact in
// a users:-restricted layer (bundling an above-cutoff resource) and a matching
// artifact in a public layer, then asserts the full read surface for an
// unauthorized caller and for the owner: search omits the restricted artifact,
// load_artifact returns 404 registry.not_found (no existence leak), the
// /objects fetch is refused, and the registry audit stream records
// visibility.denied for the denied load. The owner sees it in search, loads it,
// and fetches its bytes.
func TestAuthVisibilityDenied_SearchLoadObjects(t *testing.T) {
	t.Parallel()

	auditPath := filepath.Join(t.TempDir(), "audit.log")
	above := vdAbove()
	const (
		restrictedID = "finance/ledger/quarterly-ledger"
		publicID     = "finance/handbook/quarterly-overview"
	)
	// Both artifacts carry "quarterly" so one broad search query matches both;
	// the difference in the result set is exactly the restricted layer's
	// membership. The restricted artifact bundles an above-cutoff resource so a
	// token-bound /objects URL exists to deny.
	srv := startAuthServer(t, authServerSpec{
		Layers: []authLayer{
			{
				ID: "restricted",
				Files: map[string]string{
					restrictedID + "/ARTIFACT.md":  authContext("quarterly ledger detail, restricted to finance owners"),
					restrictedID + "/data/big.bin": above,
				},
				Visibility: authVisibility{Users: []string{"bob@acme.com"}},
			},
			{
				ID:         "public",
				Files:      map[string]string{publicID + "/ARTIFACT.md": authContext("quarterly overview, visible to everyone")},
				Visibility: authVisibility{Public: true},
			},
		},
		ExtraEnv: []string{"PODIUM_AUDIT_LOG_PATH=" + auditPath},
	})

	// alice cannot see the restricted layer; bob is its listed owner.
	alice := srv.token(authIdentity{Sub: "alice@acme.com", Email: "alice@acme.com"})
	bob := srv.token(authIdentity{Sub: "bob@acme.com", Email: "bob@acme.com"})

	// ---- search: the restricted artifact is omitted for alice, present for bob.
	aliceIDs := srv.searchIDs(alice)
	assertHas(t, aliceIDs, publicID, "alice search includes the public artifact")
	assertMissing(t, aliceIDs, restrictedID, "alice search omits the restricted artifact")
	bobIDs := srv.searchIDs(bob)
	assertHas(t, bobIDs, publicID, "bob search includes the public artifact")
	assertHas(t, bobIDs, restrictedID, "bob search includes the restricted artifact he owns")

	// ---- load_artifact: alice gets 404 registry.not_found (no existence leak),
	// never 403; bob loads it.
	st, code := srv.loadCode(restrictedID, alice)
	if st != http.StatusNotFound {
		t.Errorf("alice load of restricted artifact = %d, want 404 (no existence leak)\nlog:\n%s", st, srv.log())
	}
	if code != "registry.not_found" {
		t.Errorf("alice load code = %q, want registry.not_found (the hidden layer must not leak as 403)", code)
	}
	if st := srv.loadStatus(restrictedID, bob); st != http.StatusOK {
		t.Errorf("owner bob load of restricted artifact = %d, want 200", st)
	}

	// ---- /objects: resolve the token-bound object URL as the owner, then assert
	// an unauthorized caller and an anonymous caller are both refused at the route.
	objURL := vdObjectURL(t, srv, restrictedID, bob, above)
	// The owner fetches the bytes with the session token.
	if stObj, body := srv.get(strings.TrimPrefix(objURL, srv.BaseURL), bob); stObj != http.StatusOK {
		t.Errorf("owner /objects fetch = %d, want 200\nbody: %s", stObj, body)
	} else if string(body) != above {
		t.Errorf("owner fetched %d object bytes, want %d", len(body), len(above))
	}
	// alice cannot resolve an owning artifact for the content hash, so the route
	// refuses her even though the URL is well-formed (§13.11 re-check).
	if stDenied, _ := srv.get(strings.TrimPrefix(objURL, srv.BaseURL), alice); stDenied == http.StatusOK {
		t.Errorf("unauthorized alice fetched the /objects bytes (token-bound route bypassed): %d", stDenied)
	} else if stDenied != http.StatusNotFound {
		t.Errorf("unauthorized alice /objects fetch = %d, want 404 (refused without leaking existence)", stDenied)
	}
	// An anonymous fetch (no token) is rejected before visibility is consulted.
	if stAnon, _ := srv.get(strings.TrimPrefix(objURL, srv.BaseURL), ""); stAnon == http.StatusOK {
		t.Errorf("anonymous /objects fetch succeeded; the route is not public: %d", stAnon)
	}

	// ---- audit stream: the rejected load recorded visibility.denied. The wire
	// body withheld the layer's existence (registry.not_found above); the audit
	// event is where the denial is named (§8.1 / §8.2).
	if !brPollContains(auditPath, "visibility.denied", 5_000_000_000) {
		t.Errorf("audit stream did not record visibility.denied for the denied load\naudit log:\n%s", brReadOrEmpty(auditPath))
	}
	// The denied artifact id appears alongside the event so a SIEM can attribute
	// the refusal to the target (§8.1 target field).
	if !strings.Contains(brReadOrEmpty(auditPath), restrictedID) {
		t.Errorf("visibility.denied audit event does not name the denied target %q\naudit log:\n%s", restrictedID, brReadOrEmpty(auditPath))
	}
}

// vdObjectURL loads id as the authorized owner and returns the token-bound
// /objects URL of its single above-cutoff resource, asserting the bytes were
// externalized (not inline). The size is checked against want so the test fails
// loudly if the cutoff split changes.
func vdObjectURL(t *testing.T, srv *authServer, id, ownerToken, want string) string {
	t.Helper()
	st, body := srv.get("/v1/load_artifact?id="+id, ownerToken)
	if st != http.StatusOK {
		t.Fatalf("owner load_artifact for object URL = %d\nbody: %s", st, body)
	}
	var loaded struct {
		LargeResources map[string]struct {
			PresignedURL string `json:"presigned_url"`
			Size         int64  `json:"size"`
			ContentHash  string `json:"content_hash"`
		} `json:"large_resources"`
		Resources map[string]string `json:"resources"`
	}
	if err := json.Unmarshal(body, &loaded); err != nil {
		t.Fatalf("decode load_artifact: %v\nbody: %s", err, body)
	}
	link, ok := loaded.LargeResources["data/big.bin"]
	if !ok || link.PresignedURL == "" {
		t.Fatalf("above-cutoff resource missing from large_resources: %+v", loaded.LargeResources)
	}
	if link.Size != int64(len(want)) {
		t.Errorf("object size = %d, want %d", link.Size, len(want))
	}
	if !strings.HasPrefix(link.PresignedURL, srv.BaseURL+"/objects/") {
		t.Fatalf("filesystem object URL = %q, want a %s/objects/<hash> route", link.PresignedURL, srv.BaseURL)
	}
	return link.PresignedURL
}
