package e2e

// Large bundled-resource delivery across the two data-plane backends (gap
// G-STACK-3).
//
// standard_stack_parity_test.go proves an above-cutoff resource externalizes to
// S3 and comes back under large_resources with a non-empty presigned_url, but it
// never fetches that URL, never forces the §6.2 expiry/403 refresh against the
// live data plane, and never contrasts the S3 presigned URL against the
// filesystem backend's token-bound /objects/{content_hash} route. The MCP
// fetcher's 403 refresh is unit-tested in cmd/podium-mcp/large_resources_test.go
// against an httptest stub. This file exercises both delivery paths end to end
// against the real backends.
//
// Two halves, gated independently:
//
//   - S3 (live Postgres + live S3, msSkipIfNoStack): an author publishes a layer
//     whose artifact bundles one above-cutoff resource and one below-cutoff
//     resource through a standard-mode server with a short presign TTL. A
//     consumer loads the artifact, fetches the presigned URL and asserts the
//     bytes match, waits past the TTL so the same URL returns 403 (the live S3
//     expiry the §6.2 / §6.6 refresh contract is built for), then re-requests
//     load_artifact (the refresh) and fetches the freshly presigned URL
//     successfully. The below-cutoff resource stays inline throughout.
//
//   - Filesystem (no external infra, the G-INFRA-5 authserver harness): a
//     standalone server ingests a group-restricted layer whose artifact bundles
//     the same above-cutoff and below-cutoff resources. An authorized member
//     loads the artifact and the above-cutoff resource comes back as a
//     <baseURL>/objects/<content_hash> link on the registry origin (the §13.11
//     token-bound route) rather than an S3 presigned URL; fetching it with the
//     member's session token returns the bytes, fetching it as a caller who
//     cannot see the layer is refused, and the below-cutoff resource stays
//     inline.
//
// Spec: §4.2 (the inline cutoff), §7.2 (control plane plus data plane object
// storage), §6.2 / §6.6 (presigned URL delivery and the 403/expired refresh
// contract), §13.10 / §13.11 (filesystem backend serves the token-bound
// /objects/{content_hash} route; the S3 backend returns Signature V4 presigned
// URLs). Gap G-STACK-3.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// lrAbove returns a deterministic blob above the §4.2 inline cutoff so the data
// plane externalizes it. The content is fixed so the consumer can compare the
// fetched bytes exactly.
func lrAbove() string { return strings.Repeat("L", 256*1024+4096) }

// lrBelow is a small resource that stays inline on both backends, proving the
// cutoff split is per-resource rather than per-artifact.
const lrBelow = "print('inline-small')\n"

// lrFetch issues a GET against a data-plane URL with an optional bearer token
// and returns the status and body. It is used both for the S3 presigned URL
// (which ignores the bearer and validates its own Signature V4) and for the
// filesystem /objects route (which requires the session token). A short timeout
// keeps a wedged backend from hanging the test.
func lrFetch(t *testing.T, rawURL, token string) (int, []byte) {
	t.Helper()
	return injGet(t, rawURL, token)
}

// TestLargeResourceDataPlane_S3PresignRefresh boots a standard-mode server
// against live Postgres + live S3 with a short presign TTL, publishes a layer
// whose artifact bundles one above-cutoff and one below-cutoff resource, then as
// the authenticated consumer fetches the presigned URL, forces the §6.2 expiry
// 403, refreshes via a fresh load_artifact, and fetches the new URL. The
// below-cutoff resource stays inline.
//
// Spec: §4.2, §7.2, §6.2 / §6.6, §13.12. Gap G-STACK-3.
func TestLargeResourceDataPlane_S3PresignRefresh(t *testing.T) {
	// Not parallel: standard mode keys metadata by the shared "default" org
	// schema (see standard_stack_parity_test.go's header), so the e2e package
	// serializes the live-stack tests. Skips cleanly without a live stack.
	dsn, bucket, region := msSkipIfNoStack(t)

	// A 2-second presign TTL: long enough for an immediate fetch over localhost
	// to land inside the window, short enough that a 3-second wait guarantees the
	// same URL has expired so S3 returns 403. The refresh below re-requests
	// load_artifact for a freshly minted URL whose own 2-second window is fresh.
	priv, pemPath := injKeyPair(t)
	srv := msStartStandardServerEnv(t, dsn, bucket, region, "PODIUM_PRESIGN_TTL_SECONDS=2")
	injRegisterRuntime(t, srv, pemPath)
	token := injSignJWT(t, priv, injClaims("alice@acme.com"))

	above := lrAbove()
	id := "finance/close-reporting/variance-bundle"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":         brSkillArtifact,
		id + "/SKILL.md":            brSkillMD("variance-bundle", brVarianceDesc, "Reference the dataset.\n"),
		id + "/data/big.bin":        above,
		id + "/scripts/variance.py": lrBelow,
	})
	msPublishGitLayer(t, srv.BaseURL, token, "finance-large", reg)

	// load_artifact, polling until the runtime-registered layer is indexed.
	load := func() msLoadResponse {
		t.Helper()
		var loaded msLoadResponse
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			st, body := injGet(t, srv.BaseURL+"/v1/load_artifact?id="+id, token)
			if st == 200 {
				if err := json.Unmarshal(body, &loaded); err != nil {
					t.Fatalf("decode load_artifact: %v\nbody: %s", err, body)
				}
				if _, ok := loaded.LargeResources["data/big.bin"]; ok {
					return loaded
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("large resource never appeared in load_artifact within deadline\nserver log:\n%s", srv.log())
		return loaded
	}

	loaded := load()

	// The below-cutoff resource is inline; the above-cutoff one is not.
	if loaded.Resources["scripts/variance.py"] != lrBelow {
		t.Errorf("below-cutoff resource not inline: %q (resources=%v)", loaded.Resources["scripts/variance.py"], loaded.Resources)
	}
	if _, inline := loaded.Resources["data/big.bin"]; inline {
		t.Error("above-cutoff resource must not be returned inline by the S3 data plane")
	}
	link := loaded.LargeResources["data/big.bin"]
	if link.PresignedURL == "" {
		t.Fatalf("above-cutoff resource has no presigned_url: %+v", loaded.LargeResources)
	}
	if link.Size != int64(len(above)) {
		t.Errorf("large resource size = %d, want %d", link.Size, len(above))
	}
	// The S3 presigned URL carries its own Signature V4; it is not the
	// registry-origin /objects route. This is the backend contrast: an S3
	// deployment hands the consumer a self-authorizing URL.
	if !strings.Contains(link.PresignedURL, "X-Amz-Signature") {
		t.Errorf("S3 presigned_url missing Signature V4 query (got %q); the S3 data plane did not presign", link.PresignedURL)
	}
	if strings.HasPrefix(link.PresignedURL, srv.BaseURL+"/objects/") {
		t.Errorf("S3 deployment returned a registry-origin /objects URL (%q); expected a presigned S3 URL", link.PresignedURL)
	}

	// ---- Fetch the presigned URL: 200 with the exact bytes -----------------
	// §13.11: an S3 presigned URL is self-validating; consumers do not send
	// credentials when following it (an Authorization header alongside the
	// Signature V4 query is rejected as "multiple authentication types"). So the
	// S3 fetch goes out credential-free, unlike the filesystem /objects route.
	st, body := lrFetch(t, link.PresignedURL, "")
	if st != 200 {
		t.Fatalf("fetch fresh presigned URL = HTTP %d\nbody: %s", st, body)
	}
	if string(body) != above {
		t.Errorf("fetched %d bytes from the presigned URL, want %d (content mismatch)", len(body), len(above))
	}

	// ---- Force the §6.2 expiry: wait past the TTL, the URL returns 403 -----
	time.Sleep(3 * time.Second)
	stExpired, _ := lrFetch(t, link.PresignedURL, "")
	if stExpired != 403 {
		t.Fatalf("expired presigned URL = HTTP %d, want 403 (the live S3 expiry the refresh contract is built for)", stExpired)
	}

	// ---- Refresh: re-request load_artifact, fetch the new URL: 200 ---------
	// This is the §6.6 step 1 "request a fresh URL set and retry" contract,
	// exercised against the real data plane: the registry re-presigns from the
	// same backend and the freshly minted URL fetches the bytes.
	refreshed := load()
	freshLink := refreshed.LargeResources["data/big.bin"]
	if freshLink.PresignedURL == "" {
		t.Fatalf("refresh yielded no presigned_url: %+v", refreshed.LargeResources)
	}
	if freshLink.PresignedURL == link.PresignedURL {
		t.Errorf("refresh returned the same (expired) presigned URL; a refresh must re-presign")
	}
	stFresh, freshBody := lrFetch(t, freshLink.PresignedURL, "")
	if stFresh != 200 {
		t.Fatalf("fetch refreshed presigned URL = HTTP %d\nbody: %s", stFresh, freshBody)
	}
	if string(freshBody) != above {
		t.Errorf("refreshed fetch returned %d bytes, want %d (content mismatch)", len(freshBody), len(above))
	}
	if freshLink.ContentHash != link.ContentHash {
		t.Errorf("content hash changed across refresh: %q vs %q (the bytes are immutable)", freshLink.ContentHash, link.ContentHash)
	}
}

// TestLargeResourceDataPlane_FilesystemObjectsRoute boots a standalone server
// (filesystem object store) behind the injected-session-token verifier with a
// group-restricted layer whose artifact bundles the same above-cutoff and
// below-cutoff resources, then asserts the above-cutoff resource is delivered
// via the registry-origin token-bound /objects/{content_hash} route: an
// authorized member fetches the bytes with their session token, a caller who
// cannot see the layer is refused at the route, and the below-cutoff resource
// stays inline.
//
// Spec: §4.2, §7.2, §13.10 / §13.11 (the filesystem backend's token-bound
// /objects route, no embedded signature). Gap G-STACK-3.
func TestLargeResourceDataPlane_FilesystemObjectsRoute(t *testing.T) {
	t.Parallel()

	above := lrAbove()
	id := "finance/close-reporting/variance-bundle"
	// A group-restricted layer proves the route is token-bound: only a caller
	// the §4.6 evaluator admits can resolve the owning artifact and follow the
	// /objects URL. The artifact is a context type so a single ARTIFACT.md plus
	// the two sibling resources is a complete, lint-clean package.
	as := startAuthServer(t, authServerSpec{
		Layers: []authLayer{{
			ID: "finance",
			Files: map[string]string{
				id + "/ARTIFACT.md":         authContext("variance bundle with a large dataset"),
				id + "/data/big.bin":        above,
				id + "/scripts/variance.py": lrBelow,
			},
			Visibility: authVisibility{Groups: []string{"finance-team"}},
		}},
		SCIMToken: "scim-secret",
		SCIMUsers: map[string][]string{
			"alice@acme.com": {"finance-team"},
			// bob is provisioned but not in finance-team, so he is the
			// unauthorized negative control below.
			"bob@acme.com": {},
		},
	})

	member := as.token(authIdentity{Sub: "alice@acme.com", Email: "alice@acme.com", Groups: []string{"finance-team"}})
	outsider := as.token(authIdentity{Sub: "bob@acme.com", Email: "bob@acme.com"})

	// The member can load the artifact; the envelope externalizes the
	// above-cutoff resource to the /objects route and keeps the small one inline.
	st, body := as.get("/v1/load_artifact?id="+id, member)
	if st != 200 {
		t.Fatalf("member load_artifact = HTTP %d\nbody: %s\nlog:\n%s", st, body, as.log())
	}
	var loaded msLoadResponse
	if err := json.Unmarshal(body, &loaded); err != nil {
		t.Fatalf("decode load_artifact: %v\nbody: %s", err, body)
	}
	if loaded.Resources["scripts/variance.py"] != lrBelow {
		t.Errorf("below-cutoff resource not inline: %q (resources=%v)", loaded.Resources["scripts/variance.py"], loaded.Resources)
	}
	if _, inline := loaded.Resources["data/big.bin"]; inline {
		t.Error("above-cutoff resource must not be inline on the filesystem backend either")
	}
	link, ok := loaded.LargeResources["data/big.bin"]
	if !ok || link.PresignedURL == "" {
		t.Fatalf("above-cutoff resource missing from large_resources: %+v", loaded.LargeResources)
	}
	if link.Size != int64(len(above)) {
		t.Errorf("large resource size = %d, want %d", link.Size, len(above))
	}
	// The filesystem backend returns a registry-origin /objects/<content_hash>
	// URL, not an S3 presigned URL. This is the §13.11 contrast: no embedded
	// signature, the consumer authenticates with its session token.
	if !strings.HasPrefix(link.PresignedURL, as.BaseURL+"/objects/") {
		t.Fatalf("filesystem large_resources URL = %q, want a %s/objects/<hash> route", link.PresignedURL, as.BaseURL)
	}
	if strings.Contains(link.PresignedURL, "X-Amz-Signature") {
		t.Errorf("filesystem /objects URL carries an S3 signature (%q); it must be token-bound, not pre-signed", link.PresignedURL)
	}
	// The URL key is the resource content hash (§13.7 content-addressed route).
	wantKey := strings.TrimPrefix(link.ContentHash, "sha256:")
	if !strings.HasSuffix(link.PresignedURL, "/objects/"+wantKey) {
		t.Errorf("/objects URL %q does not end in the content hash key %q", link.PresignedURL, wantKey)
	}

	// ---- Authorized member fetches the bytes with the session token --------
	stObj, objBody := lrFetch(t, link.PresignedURL, member)
	if stObj != 200 {
		t.Fatalf("member /objects fetch = HTTP %d\nbody: %s", stObj, objBody)
	}
	if string(objBody) != above {
		t.Errorf("member fetched %d bytes from /objects, want %d (content mismatch)", len(objBody), len(above))
	}

	// ---- Negative control: a caller who cannot see the layer is refused ----
	// The route re-checks visibility on every fetch (§13.11): an outsider who
	// cannot resolve an owning artifact cannot follow the URL even though it is
	// well-formed. The route reports this as not-found so it leaks nothing.
	stDenied, deniedBody := lrFetch(t, link.PresignedURL, outsider)
	if stDenied == 200 {
		t.Errorf("outsider fetched the /objects bytes (token-bound route bypassed): HTTP %d\n%s", stDenied, deniedBody)
	}
	if stDenied != 404 {
		t.Errorf("outsider /objects fetch = HTTP %d, want 404 (visibility re-check refuses without leaking existence)", stDenied)
	}

	// An anonymous fetch (no token) is likewise refused: the route is not public.
	stAnon, _ := lrFetch(t, link.PresignedURL, "")
	if stAnon == 200 {
		t.Errorf("anonymous fetch of the /objects bytes succeeded (route is not token-bound): HTTP %d", stAnon)
	}
}
