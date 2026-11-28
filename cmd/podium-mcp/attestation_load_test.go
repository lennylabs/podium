package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	synccfg "github.com/lennylabs/podium/pkg/sync"
)

// requirePolicy writes a policy file for finance/** under mode with a
// signer of identity, and names it in the user's sync.yaml.
func requirePolicy(t *testing.T, e *consumerEnv, r *attestRegistry, mode, identity string) {
	t.Helper()
	writeFiles(t, e.home, map[string]string{".podium/policy.json": policyJSON(r.rootFile, mode, identity)})
	e.writeUserSync(t, "defaults:\n  attestation:\n    policy_file: policy.json\n")
}

// envelope returns the result as a map and fails when it is not one.
func envelope(t *testing.T, out any) map[string]any {
	t.Helper()
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want a map", out)
	}
	return m
}

// wantRefusal fails unless out is the §6.10 envelope of code with the
// constraint.
func wantRefusal(t *testing.T, out any, code, constraint string) {
	t.Helper()
	m := envelope(t, out)
	if m["code"] != code {
		t.Fatalf("code = %v, want %s: %v", m["code"], code, m)
	}
	d, _ := m["details"].(map[string]any)
	if constraint != "" && (d == nil || d["constraint"] != constraint) {
		t.Fatalf("details = %v, want the constraint %s", d, constraint)
	}
}

// wantDelivered fails unless out is a delivered load of id.
func wantDelivered(t *testing.T, out any, id string) map[string]any {
	t.Helper()
	m := envelope(t, out)
	if _, isErr := m["error"]; isErr || m["id"] != id {
		t.Fatalf("load of %s = %v", id, m)
	}
	return m
}

// Spec: §4.7.11 (Attestation check), §6.6 step 2, §6.5 — a record the
// registry served under the user's require rule is delivered when a bundle
// of the trusted signer lists it; the bundle lands in the attestation
// cache, a delivery from the content cache checks the record again, and
// offline-only verifies from the cached bundle and refuses as missing once
// that bundle is gone.
// Matrix: §6.10 (materialize.attestation_missing)
func TestAttestMCP_RequireDeliversAndCachesBundles(t *testing.T) {
	t.Parallel()
	r := newAttestRegistry(t)
	e := newConsumerEnv(t)
	requirePolicy(t, e, r, "require", sh.DefaultSAN)
	a := e.mustLoad(t)
	cacheDir := t.TempDir()
	s := attestServer(t, r, a, cacheDir, "always-revalidate")

	m := wantDelivered(t, s.loadArtifact(map[string]any{"id": " finance/pay ", "version": "1.0.0 "}), "finance/pay")
	if _, warned := m["attestation_warning"]; warned {
		t.Errorf("a passing load carries a warning: %v", m)
	}
	cached := filepath.Join(cacheDir, "attestations", strings.TrimPrefix(attest.AttestationDigest(r.bundle), "sha256:"))
	if _, err := os.Stat(cached); err != nil {
		t.Fatalf("the bundle is not in the attestation cache: %v", err)
	}
	hits := r.bundleHits.Load()
	wantDelivered(t, s.loadArtifact(map[string]any{"id": "finance/pay", "version": "1.0.0"}), "finance/pay")
	if r.bundleHits.Load() != hits {
		t.Errorf("a cached bundle was fetched again")
	}

	// One process at a time holds the index DB of a cache directory.
	_ = s.resolutions.Close()
	offline := attestServer(t, r, a, cacheDir, "offline-only")
	wantDelivered(t, offline.loadArtifact(map[string]any{"id": "finance/pay", "version": "1.0.0"}), "finance/pay")
	if err := os.RemoveAll(filepath.Join(cacheDir, "attestations")); err != nil {
		t.Fatal(err)
	}
	_ = offline.resolutions.Close()
	offline = attestServer(t, r, a, cacheDir, "offline-only")
	wantRefusal(t, offline.loadArtifact(map[string]any{"id": "finance/pay", "version": "1.0.0"}), attest.CodeAttestationMissing, "")
}

// Spec: §4.7.11 (Attestation check), §6.6 step 2 — a record no bundle of
// a trusted signer lists is refused under require with the code of the
// closest bundle, before anything is cached or written, and delivered under
// warn with its warning text in attestation_warning.
// Matrix: §6.10 (materialize.attestation_untrusted)
func TestAttestMCP_UntrustedSignerRequireAndWarn(t *testing.T) {
	t.Parallel()
	r := newAttestRegistry(t)
	e := newConsumerEnv(t)
	requirePolicy(t, e, r, "require", "bob@acme.com")
	cacheDir := t.TempDir()
	s := attestServer(t, r, e.mustLoad(t), cacheDir, "always-revalidate")
	dest := t.TempDir()
	out := s.loadArtifact(map[string]any{"id": "finance/pay", "destination": dest})
	wantRefusal(t, out, attest.CodeAttestationUntrusted, attest.ConstraintSigner)
	if d, _ := envelope(t, out)["details"].(map[string]any); d["artifact_id"] != "finance/pay" {
		t.Errorf("details = %v, want the requested ID", d)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Errorf("a refused load wrote %d entries", len(entries))
	}
	if s.cache.has(mustHash(t, r, "finance/pay")) {
		t.Error("a refused record was cached")
	}

	w := newConsumerEnv(t)
	requirePolicy(t, w, r, "warn", "bob@acme.com")
	sw := attestServer(t, r, w.mustLoad(t), t.TempDir(), "always-revalidate")
	m := wantDelivered(t, sw.loadArtifact(map[string]any{"id": "finance/pay"}), "finance/pay")
	if got, want := m["attestation_warning"], "finance/pay: materialize.attestation_untrusted (constraint signer)"; got != want {
		t.Errorf("attestation_warning = %v, want %q", got, want)
	}
}

// mustHash returns the content hash the registry serves for id.
func mustHash(t *testing.T, r *attestRegistry, id string) string {
	t.Helper()
	resp, err := http.Get(r.ts.URL + "/v1/load_artifact?id=" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		ContentHash string `json:"content_hash"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.ContentHash
}

// Spec: §4.7.11 (Attestation check), §6.5 — a cached record that fails the
// check is a cache miss: offline-first refetches it from the registry and
// delivers the response that passes.
func TestAttestMCP_CachedRecordThatFailsIsACacheMiss(t *testing.T) {
	t.Parallel()
	r := newAttestRegistry(t)
	e := newConsumerEnv(t)
	requirePolicy(t, e, r, "require", sh.DefaultSAN)
	a := e.mustLoad(t)
	cacheDir := t.TempDir()
	s := attestServer(t, r, a, cacheDir, "offline-first")
	wantDelivered(t, s.loadArtifact(map[string]any{"id": "finance/pay", "version": "1.0.0"}), "finance/pay")
	// A record cached before the cache kept the attestations member.
	hash := mustHash(t, r, "finance/pay")
	if err := os.Remove(filepath.Join(cacheDir, sanitizeHash(hash), "delivery", deliverySegment("finance/pay"), "attestations")); err != nil {
		t.Fatal(err)
	}
	loads := r.loads.Load()
	wantDelivered(t, s.loadArtifact(map[string]any{"id": "finance/pay", "version": "1.0.0"}), "finance/pay")
	if r.loads.Load() != loads+1 {
		t.Errorf("loads = %d, want one refetch after %d", r.loads.Load(), loads)
	}
	// The degraded-network fallback treats the same record as a miss.
	if err := os.Remove(filepath.Join(cacheDir, sanitizeHash(hash), "delivery", deliverySegment("finance/pay"), "attestations")); err != nil {
		t.Fatal(err)
	}
	r.down.Store(true)
	_ = s.resolutions.Close()
	ar := attestServer(t, r, a, cacheDir, "always-revalidate")
	wantRefusal(t, ar.loadArtifact(map[string]any{"id": "finance/pay", "version": "1.0.0"}), "network.registry_unreachable", "")
}

// stubRegistry serves one load_artifact body for every request and counts
// the requests.
func stubRegistry(t *testing.T, body string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts, &hits
}

// Spec: §4.7.11 (Request binding, Well-formed requests and records) — with
// no policy, a record of another ID, and a record that does not meet the
// pin, are refused, and a requested ID that breaks the well-formed rule is
// refused before any request.
// Matrix: §6.10 (materialize.request_mismatch)
func TestAttestMCP_RequestBindingUnderNone(t *testing.T) {
	t.Parallel()
	body := loadArtifactJSON(t, map[string]any{
		"id": "personal/x", "type": "agent", "version": "2.0.0",
		"manifest_body": "body", "frontmatter": "---\ntype: agent\nversion: 2.0.0\n---\n",
	})
	ts, hits := stubRegistry(t, body)
	s := newTestServer(t, &config{registry: ts.URL, harness: "none", cacheMode: "always-revalidate", verifyPolicy: "never"})
	wantRefusal(t, s.loadArtifact(map[string]any{"id": "finance/pay"}), attest.CodeRequestMismatch, attest.ConstraintID)
	wantRefusal(t, s.loadArtifact(map[string]any{"id": "personal/x", "version": "1.0.0"}), attest.CodeRequestMismatch, attest.ConstraintVersion)
	before := hits.Load()
	for _, id := range []string{"GIT~1", "a/../b", "con", ".podium/x"} {
		wantRefusal(t, s.loadArtifact(map[string]any{"id": id}), attest.CodeRequestMismatch, attest.ConstraintIDForm)
	}
	if hits.Load() != before {
		t.Errorf("a malformed ID reached the registry")
	}
	// The resources mirror binds the ID of its URI.
	out := s.handleResourcesRead(json.RawMessage(`{"uri":"podium://artifact/finance/pay"}`))
	wantRefusal(t, out, attest.CodeRequestMismatch, attest.ConstraintID)
	wantRefusal(t, s.handleResourcesRead(json.RawMessage(`{"uri":"podium://artifact/GIT~1"}`)), attest.CodeRequestMismatch, attest.ConstraintIDForm)
}

// Spec: §4.7.11 (Attestation check), §5.0 — the resources mirror runs the
// check: an untrusted record is refused, and a trusted one is returned.
func TestAttestMCP_ResourcesRead(t *testing.T) {
	t.Parallel()
	r := newAttestRegistry(t)
	e := newConsumerEnv(t)
	requirePolicy(t, e, r, "require", "bob@acme.com")
	s := attestServer(t, r, e.mustLoad(t), t.TempDir(), "always-revalidate")
	wantRefusal(t, s.handleResourcesRead(json.RawMessage(`{"uri":"podium://artifact/finance/pay"}`)), attest.CodeAttestationUntrusted, attest.ConstraintSigner)

	w := newConsumerEnv(t)
	requirePolicy(t, w, r, "warn", "bob@acme.com")
	sw := attestServer(t, r, w.mustLoad(t), t.TempDir(), "always-revalidate")
	m := envelope(t, sw.handleResourcesRead(json.RawMessage(`{"uri":"podium://artifact/finance/pay"}`)))
	if _, ok := m["contents"]; !ok || !strings.Contains(m["attestation_warning"].(string), "constraint signer") {
		t.Fatalf("read = %v", m)
	}
}

// Spec: §4.7.11 (User home, Machine file), §6.1.1 — a failed account lookup
// and a machine file that fails its check are reported in the
// attestation_warning member of every load result, a refusal included.
// Matrix: §6.10 (config.attestation_policy_unavailable)
func TestAttestMCP_NoticesInEveryLoadResult(t *testing.T) {
	t.Parallel()
	e := newConsumerEnv(t)
	e.home, e.homeErr = "", errors.New("no entry for uid")
	writeFiles(t, e.machineDir, map[string]string{"sync.yaml": "defaults: {}\n"})
	if err := os.Chmod(filepath.Join(e.machineDir, "sync.yaml"), 0o666); err != nil {
		t.Fatal(err)
	}
	a := e.mustLoad(t)
	if !strings.Contains(e.stderr.String(), "no home directory") || !strings.Contains(e.stderr.String(), "ignoring the machine sync.yaml") {
		t.Errorf("stderr = %q", e.stderr.String())
	}
	ts, _ := stubRegistry(t, loadArtifactJSON(t, map[string]any{
		"id": "x", "type": "agent", "version": "1.0.0", "manifest_body": "b", "frontmatter": "---\ntype: agent\n---\n",
	}))
	s := newTestServer(t, &config{registry: ts.URL, harness: "none", cacheMode: "always-revalidate", verifyPolicy: "never", attestation: a})
	want := "config.attestation_policy_unavailable (constraint user_home); config.attestation_policy_unavailable (constraint machine_file)"
	for _, args := range []map[string]any{{"id": "x"}, {"id": "GIT~1"}} {
		if got := envelope(t, s.loadArtifact(args))["attestation_warning"]; got != want {
			t.Errorf("%v: attestation_warning = %v, want %q", args, got, want)
		}
	}
	if got := envelope(t, s.handleResourcesRead(json.RawMessage(`{"uri":"podium://artifact/x"}`)))["attestation_warning"]; got != want {
		t.Errorf("read: attestation_warning = %v", got)
	}
}

// overlayRecord is an overlay package of an agent.
func overlayRecord(id string, resources map[string][]byte) filesystem.ArtifactRecord {
	fm := []byte("---\ntype: agent\nversion: 1.0.0\n---\n\nOverlay.\n")
	return filesystem.ArtifactRecord{ID: id, ArtifactBytes: fm, AuthoredBytes: fm, Resources: resources}
}

// Spec: §4.7.11 (Workspace overlay), §6.4 — an overlay package whose ID the
// user sources resolve to require is not applied: the registry serves the
// ID under the check and the load reports the skipped package; allow
// applies it, deny applies none, and a package that breaks the well-formed
// rule is skipped under every policy.
func TestAttestMCP_WorkspaceOverlay(t *testing.T) {
	t.Parallel()
	r := newAttestRegistry(t)
	records := []filesystem.ArtifactRecord{
		overlayRecord("finance/pay", nil), overlayRecord("personal/note", nil),
		overlayRecord("GIT~1", map[string][]byte{"config": []byte("x")}),
		overlayRecord("personal/bad", map[string][]byte{`a\b`: []byte("x")}),
	}
	ids := func(s *mcpServer) []string {
		var out []string
		for _, rec := range s.overlaySnapshot() {
			out = append(out, rec.ID)
		}
		return out
	}
	for _, tc := range []struct {
		overlay string
		want    []string
	}{
		{"", []string{"personal/note"}},
		{"allow", []string{"finance/pay", "personal/note"}},
		{"deny", nil},
	} {
		e := newConsumerEnv(t)
		writeFiles(t, e.home, map[string]string{".podium/policy.json": policyJSON(r.rootFile, "require", sh.DefaultSAN)})
		body := "defaults:\n  attestation:\n    policy_file: policy.json\n"
		if tc.overlay != "" {
			body += "    overlay: " + tc.overlay + "\n"
		}
		e.writeUserSync(t, body)
		s := attestServer(t, r, e.mustLoad(t), t.TempDir(), "always-revalidate")
		s.setOverlay(records, nil)
		if got := ids(s); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("overlay %q: applied %v, want %v", tc.overlay, got, tc.want)
		}
		if tc.overlay != "" {
			continue
		}
		m := wantDelivered(t, s.loadArtifact(map[string]any{"id": "finance/pay"}), "finance/pay")
		if m["attestation_warning"] != "finance/pay: materialize.attestation_untrusted (constraint overlay)" {
			t.Errorf("attestation_warning = %v", m["attestation_warning"])
		}
		if !strings.Contains(m["manifest_body"].(string), "Pay.") {
			t.Errorf("the overlay served the protected ID: %v", m["manifest_body"])
		}
	}
}

// Spec: §4.7.11 (Attestation check, step 8) — a bundle fetch carries the
// caller's credential and tenant, as a load does.
func TestAttestMCP_BundleFetchCarriesTheCredential(t *testing.T) {
	t.Parallel()
	var auth, tenant atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		tenant.Store(r.Header.Get("X-Podium-Tenant"))
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)
	s := newTestServer(t, &config{registry: ts.URL, identityProvider: "injected-session-token", sessionToken: "tok-1", tenantID: "acme"})
	c := s.newAttestationChecker()
	if _, err := c.Bundles.Bundle(t.Context(), "sha256:"+strings.Repeat("a", 64)); !errors.Is(err, attest.ErrBundleNotFound) {
		t.Fatalf("Bundle: %v", err)
	}
	if auth.Load() != "Bearer tok-1" || tenant.Load() != "acme" {
		t.Errorf("headers = %v, %v", auth.Load(), tenant.Load())
	}
}

// Spec: §4.7.11 (Root copy of user-source rules) — the server refreshes each
// public-good root its policy verifies under until its context ends.
func TestAttestMCP_StartsTheRootRefresh(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(mirror.Close)
	e := newConsumerEnv(t)
	writeFiles(t, e.home, map[string]string{".podium/p.json": publicGoodPolicy})
	e.writeUserSync(t, "defaults:\n  attestation:\n    policy_file: p.json\n")
	a, err := loadConsumerAttestation(attestationInputs{
		getenv: func(string) string { return "" }, paths: systemPaths{tufMirror: mirror.URL, machine: synccfg.MachineFileOptions{Dir: e.machineDir}},
		home: e.home, cwd: e.cwd, now: time.Now(), stderr: func(string, ...any) {},
	})
	if err != nil || len(a.refreshers) != 1 {
		t.Fatalf("load: %v, %d refreshers", err, len(a.refreshers))
	}
	s := newTestServer(t, &config{attestation: a})
	ctx, cancel := context.WithCancel(t.Context())
	s.startAttestationRefresh(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if hits.Load() == 0 {
		t.Fatal("the refresh sent no request to the mirror")
	}
}
