package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/lennylabs/podium/internal/revmark"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// rev returns the artifact_revision string for n Unix microseconds.
func rev(n int64) string {
	return version.FormatArtifactRevision(time.UnixMicro(n))
}

// freshnessServer returns a bridge over a fresh index DB for registry.
func freshnessServer(t *testing.T) *mcpServer {
	t.Helper()
	r := newResolutionCache(t.TempDir())
	t.Cleanup(func() { _ = r.Close() })
	return &mcpServer{cfg: &config{registry: "http://registry.example"}, resolutions: r, sessionID: "bridge-session"}
}

// TestCheckFreshness_ComparesWithSessionReferenceOrMark pins the §6.5
// comparison: no reference admits any canonical revision, equality is
// admitted, a revision below the session reference or the mark is refused
// with the reference written in details, and a non-canonical revision is
// refused with an empty reference.
//
// Spec: §6.5, §4.7.10
func TestCheckFreshness_ComparesWithSessionReferenceOrMark(t *testing.T) {
	t.Parallel()
	s := freshnessServer(t)
	resp := loadArtifactResponse{ID: "team/x", Version: "1.0.0", ArtifactRevision: rev(100)}

	if got, env := s.checkFreshness(resp, "s1"); env != nil || got != 100 {
		t.Fatalf("no reference: got (%d, %v), want (100, nil)", got, env)
	}
	s.resolutions.PutLatestAdvancing(s.markKey("team/x"), "s1", "team/x", "2.0.0", "sha256:a", 200, time.Now())

	if _, env := s.checkFreshness(loadArtifactResponse{ID: "team/x", ArtifactRevision: rev(200)}, "s2"); env != nil {
		t.Fatalf("equal to the mark: refused with %v", env)
	}
	for name, session := range map[string]string{"session reference": "s1", "mark": "s2"} {
		_, env := s.checkFreshness(resp, session)
		if env == nil {
			t.Fatalf("%s: revision 100 below 200 was admitted", name)
		}
		if env["code"] != "materialize.stale_resolution" || env["retryable"] != false || env["suggested_action"] == "" {
			t.Errorf("%s: envelope = %v", name, env)
		}
		d, _ := env["details"].(map[string]any)
		if d["artifact_id"] != "team/x" || d["served_version"] != "1.0.0" ||
			d["served_revision"] != rev(100) || d["reference_revision"] != rev(200) {
			t.Errorf("%s: details = %v", name, d)
		}
	}

	_, env := freshnessServer(t).checkFreshness(loadArtifactResponse{ID: "team/x", ArtifactRevision: "2026-01-01"}, "s1")
	if env == nil {
		t.Fatal("non-canonical revision was admitted")
	}
	if d, _ := env["details"].(map[string]any); d["served_revision"] != "2026-01-01" || d["reference_revision"] != "" {
		t.Errorf("non-canonical details = %v", d)
	}
}

// TestEffectiveSessionID_PrefersTrimmedHostSession pins that the registry
// request and the §6.5 check share one session: the trimmed host session_id,
// else the bridge's own.
//
// Spec: §6.5
func TestEffectiveSessionID_PrefersTrimmedHostSession(t *testing.T) {
	t.Parallel()
	s := freshnessServer(t)
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"session_id": " host "}, "host"},
		{map[string]any{"session_id": "  "}, "bridge-session"},
		{map[string]any{"session_id": nil}, "bridge-session"},
		{map[string]any{}, "bridge-session"},
	} {
		if got := s.effectiveSessionID(tc.args); got != tc.want {
			t.Errorf("effectiveSessionID(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
	req, err := s.newRegistryRequest("GET", "/v1/load_artifact", map[string]any{"id": "team/x", "session_id": nil})
	if err != nil {
		t.Fatalf("newRegistryRequest: %v", err)
	}
	if got := req.URL.Query().Get("session_id"); got != "bridge-session" {
		t.Errorf("request session_id = %q, want bridge-session", got)
	}
}

// TestNoteCachedSession_RecordsReferenceWithoutAdvancingMark pins the
// revalidated-cache path: it records the session reference from the cached
// revision, leaves the mark absent, and records nothing for a revision that
// does not parse.
//
// Spec: §6.5
func TestNoteCachedSession_RecordsReferenceWithoutAdvancingMark(t *testing.T) {
	t.Parallel()
	s := freshnessServer(t)
	s.noteCachedSession(&resolutionWrite{ID: "team/x", Session: "bad"}, loadArtifactResponse{ID: "team/x", ArtifactRevision: "x"})
	s.noteCachedSession(&resolutionWrite{ID: "team/x", Session: "s1"}, loadArtifactResponse{ID: "team/x", ArtifactRevision: rev(300)})

	if ref, ok := s.resolutions.Reference(s.markKey("team/x"), "s1"); !ok || ref != 300 {
		t.Errorf("session reference = (%d, %v), want (300, true)", ref, ok)
	}
	for _, session := range []string{"bad", "other"} {
		if ref, ok := s.resolutions.Reference(s.markKey("team/x"), session); ok {
			t.Errorf("session %s: reference %d recorded, want none (no mark advanced)", session, ref)
		}
	}
}

// TestWriteResolution_KeysMarkOnServedID pins that the latest write and the
// cached-session note use the key checkFreshness compares under, the served
// record's ID. A record of team/b answering a request for team/a advances
// team/b's mark and leaves team/a's mark absent.
//
// Spec: §6.5
func TestWriteResolution_KeysMarkOnServedID(t *testing.T) {
	t.Parallel()
	s := freshnessServer(t)
	resp := loadArtifactResponse{ID: "team/b", Version: "1.0.0", ContentHash: "sha256:b", ArtifactRevision: rev(500)}
	s.writeResolution(&resolutionWrite{ID: "team/a", Session: "s1", Revision: 500, Now: time.Now()}, resp)
	s.writeResolution(&resolutionWrite{ID: "team/a", Session: "s2", RefreshOnly: true, Now: time.Now()}, resp)

	for _, session := range []string{"s1", "s2", "other"} {
		if ref, ok := s.resolutions.Reference(s.markKey("team/b"), session); !ok || ref != 500 {
			t.Errorf("team/b session %s: reference = (%d, %v), want (500, true)", session, ref, ok)
		}
		if ref, ok := s.resolutions.Reference(s.markKey("team/a"), session); ok {
			t.Errorf("team/a session %s: reference %d recorded, want none", session, ref)
		}
	}
	if _, env := s.checkFreshness(loadArtifactResponse{ID: "team/b", ArtifactRevision: rev(400)}, "other"); env == nil {
		t.Error("team/b revision below its advanced mark was admitted")
	}
}

// ----- TEST-2: the §6.5 check over the bridge's load paths -----------------

// noSession is a session no test load uses, so Reference under it reads the
// persisted (or in-memory) revision mark alone.
const noSession = "freshness-test-no-session"

// freshStub is a registry that serves one record per artifact ID, switchable
// between loads so every load reaches the same URL and the same mark key. A
// record stored under "<id>|<session>" answers that session alone. HEAD
// answers head when set and the served record's content hash otherwise. A
// GET carrying If-None-Match is answered 304 when notModified is set, and
// readOnly adds the §13.2.1 read-only headers to every GET.
type freshStub struct {
	mu          sync.Mutex
	records     map[string]loadArtifactResponse
	head        string
	notModified bool
	readOnly    bool
}

// set serves rec for its ID to every session.
func (f *freshStub) set(rec loadArtifactResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[rec.ID] = rec
}

// setFor serves rec for its ID to session alone.
func (f *freshStub) setFor(session string, rec loadArtifactResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[rec.ID+"|"+session] = rec
}

// configure applies fn to the stub under its lock.
func (f *freshStub) configure(fn func(*freshStub)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *freshStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	rec, ok := f.records[q.Get("id")+"|"+q.Get("session_id")]
	if !ok {
		rec, ok = f.records[q.Get("id")]
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"registry.not_found","message":"not found"}`))
		return
	}
	switch {
	case r.Method == http.MethodHead:
		hash := f.head
		if hash == "" {
			hash = rec.ContentHash
		}
		w.Header().Set("X-Podium-Content-Hash", hash)
		w.WriteHeader(http.StatusOK)
	case f.notModified && r.Header.Get("If-None-Match") != "":
		w.WriteHeader(http.StatusNotModified)
	default:
		if f.readOnly {
			w.Header().Set("X-Podium-Read-Only", "true")
			w.Header().Set("X-Podium-Read-Only-Lag-Seconds", "30")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rec)
	}
}

// newFreshStub starts a freshStub and returns it with its URL.
func newFreshStub(t *testing.T) (*freshStub, *httptest.Server) {
	t.Helper()
	stub := &freshStub{records: map[string]loadArtifactResponse{}}
	ts := httptest.NewServer(stub)
	t.Cleanup(ts.Close)
	return stub, ts
}

// freshBridge returns a bridge over the cache directory dir against registry
// in mode, with its own session ID.
func freshBridge(t *testing.T, dir, registry, mode string) *mcpServer {
	t.Helper()
	s := cacheServer(t, dir, registry, mode)
	s.sessionID = "bridge-session"
	return s
}

// lockIndex holds the index DB under dir open for the rest of the test, as a
// second podium-mcp on one PODIUM_CACHE_DIR sees it. A bridge opened over dir
// afterwards keeps its marks in memory.
func lockIndex(t *testing.T, dir string) {
	t.Helper()
	holder := newResolutionCache(dir)
	if holder.db == nil {
		t.Fatal("the holder could not open the index DB")
	}
	t.Cleanup(func() { _ = holder.Close() })
}

// revRecord returns a sealed context record for id at semver ver served at
// revision n.
func revRecord(id, ver string, n int64) loadArtifactResponse {
	rec := cachedRecord(id, "---\ntype: context\nversion: "+ver+"\n---\n", "body "+ver+"\n", nil)
	rec.ArtifactRevision = rev(n)
	return sealDelivery(rec)
}

// loadArgs returns the load_artifact arguments for a `latest` load of id,
// carrying session as the host session_id when it is non-empty.
func loadArgs(id, session string) map[string]any {
	args := map[string]any{"id": id}
	if session != "" {
		args["session_id"] = session
	}
	return args
}

// serveAndLoad serves rec and runs a `latest` load of its ID under session.
func serveAndLoad(s *mcpServer, stub *freshStub, rec loadArtifactResponse, session string) any {
	stub.set(rec)
	return s.loadArtifact(loadArgs(rec.ID, session))
}

// wantStale fails unless out is a materialize.stale_resolution envelope whose
// reference_revision is ref Unix microseconds, and returns its details.
func wantStale(t *testing.T, out any, ref int64) map[string]any {
	t.Helper()
	m, ok := out.(map[string]any)
	if !ok || m["code"] != "materialize.stale_resolution" {
		t.Fatalf("load = %v, want materialize.stale_resolution", out)
	}
	if !isErrorResult(out) {
		t.Errorf("stale envelope is not an error result: %v", m)
	}
	d, _ := m["details"].(map[string]any)
	if d["reference_revision"] != rev(ref) {
		t.Errorf("reference_revision = %v, want %s", d["reference_revision"], rev(ref))
	}
	return d
}

// wantMark fails unless the persisted or in-memory mark for id is n.
func wantMark(t *testing.T, s *mcpServer, id string, n uint64) {
	t.Helper()
	if got, ok := s.resolutions.Reference(s.markKey(id), noSession); !ok || got != n {
		t.Errorf("mark for %s = (%d, %v), want %d", id, got, ok, n)
	}
}

// hasSessionRef reports whether a session reference exists for (id, session).
func hasSessionRef(s *mcpServer, id, session string) bool {
	r := s.resolutions
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.sessions[sessionMarkKey{mark: string(s.markKey(id).Encode()), session: session}]
	return ok
}

// seedMark writes the persisted mark for id directly, leaving the id@latest
// entry and every session reference untouched.
func seedMark(t *testing.T, s *mcpServer, id string, n uint64) {
	t.Helper()
	if err := s.resolutions.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(revmark.Bucket()).Put(s.markKey(id).Encode(), []byte(strconv.FormatUint(n, 10)))
	}); err != nil {
		t.Fatalf("seed mark: %v", err)
	}
}

// Spec: §6.5, §6.9 — a fresh latest answer below the mark is refused with the
// non-retryable code, its remedy, and its details, and the refusal writes
// nothing: no content-cache entry, no id@latest change, no mark change, no
// materialized file, and no local read event. A refusal records no session
// reference, so a second answer under the same fresh session is compared with
// the mark again.
// Matrix: §6.10 (materialize.stale_resolution)
func TestFreshness_StaleLatestRefusedAndWritesNothing(t *testing.T) {
	t.Parallel()
	stub, ts := newFreshStub(t)
	s := freshBridge(t, t.TempDir(), ts.URL, "")
	auditPath := attachAuditFile(t, s)
	wantServed(t, serveAndLoad(s, stub, revRecord("team/x", "2.0.0", 200), "s0"), "body 2.0.0\n")

	stale := revRecord("team/x", "1.0.0", 100)
	stub.set(stale)
	dest := t.TempDir()
	args := loadArgs("team/x", "S")
	args["destination"] = dest
	out := s.loadArtifact(args)
	d := wantStale(t, out, 200)
	m := out.(map[string]any)
	retryable, suggested := bridgeCodeMeta("materialize.stale_resolution")
	if m["retryable"] != retryable || retryable || m["suggested_action"] != suggested {
		t.Errorf("retryable/suggested_action = %v / %v", m["retryable"], m["suggested_action"])
	}
	if d["artifact_id"] != "team/x" || d["served_version"] != "1.0.0" || d["served_revision"] != rev(100) {
		t.Errorf("details = %v", d)
	}

	if s.cache.has(stale.ContentHash) {
		t.Error("the refused record reached the content cache")
	}
	if v := latestVersion(t, s.resolutions, "team/x"); v != "2.0.0" {
		t.Errorf("id@latest = %q, want 2.0.0", v)
	}
	wantMark(t, s, "team/x", 200)
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Errorf("the refused load materialized %d entries", len(entries))
	}
	if n := loadedEventCount(t, auditPath); n != 1 {
		t.Errorf("artifact.loaded events = %d, want 1 (the accepted load only)", n)
	}
	wantStale(t, s.loadArtifact(loadArgs("team/x", "S")), 200)
}

// Spec: §6.5 — an answer equal to the mark is delivered, and a higher answer
// is delivered and advances the mark.
func TestFreshness_EqualAndHigherDelivered(t *testing.T) {
	t.Parallel()
	stub, ts := newFreshStub(t)
	s := freshBridge(t, t.TempDir(), ts.URL, "")
	wantServed(t, serveAndLoad(s, stub, revRecord("team/x", "2.0.0", 200), "s1"), "body 2.0.0\n")
	wantServed(t, serveAndLoad(s, stub, revRecord("team/x", "2.0.1", 200), "s2"), "body 2.0.1\n")
	wantMark(t, s, "team/x", 200)
	wantServed(t, serveAndLoad(s, stub, revRecord("team/x", "3.0.0", 300), "s3"), "body 3.0.0\n")
	wantMark(t, s, "team/x", 300)
}

// Spec: §6.5 — a load that names a version (exact, range, or content hash) is
// never compared and never moves the mark.
func TestFreshness_PinnedLoadsNotCompared(t *testing.T) {
	t.Parallel()
	stub, ts := newFreshStub(t)
	s := freshBridge(t, t.TempDir(), ts.URL, "")
	wantServed(t, serveAndLoad(s, stub, revRecord("team/x", "2.0.0", 200), "s1"), "body 2.0.0\n")
	old := revRecord("team/x", "1.0.0", 100)
	stub.set(old)
	for _, v := range []string{"1.0.0", "1.x", old.ContentHash} {
		args := loadArgs("team/x", "s2")
		args["version"] = v
		wantServed(t, s.loadArtifact(args), "body 1.0.0\n")
	}
	wantMark(t, s, "team/x", 200)
}

// Spec: §6.5 — the reference is the session's first accepted answer, else the
// mark. Session references are keyed by session: a third session's first
// answer is compared with the mark, never with another session's reference.
func TestFreshness_SessionReference(t *testing.T) {
	t.Parallel()
	stub, ts := newFreshStub(t)
	s := freshBridge(t, t.TempDir(), ts.URL, "")
	r300, r400 := revRecord("team/x", "3.0.0", 300), revRecord("team/x", "4.0.0", 400)

	wantServed(t, serveAndLoad(s, stub, r300, "S1"), "body 3.0.0\n")
	wantMark(t, s, "team/x", 300)
	wantStale(t, serveAndLoad(s, stub, revRecord("team/x", "2.0.0", 200), "S2"), 300)
	wantServed(t, serveAndLoad(s, stub, r300, "S1"), "body 3.0.0\n")
	wantServed(t, serveAndLoad(s, stub, r400, "S4"), "body 4.0.0\n")
	wantServed(t, serveAndLoad(s, stub, r300, "S1"), "body 3.0.0\n")
	wantMark(t, s, "team/x", 400)
	if v := latestVersion(t, s.resolutions, "team/x"); v != "4.0.0" {
		t.Errorf("id@latest = %q, want 4.0.0", v)
	}
	wantStale(t, serveAndLoad(s, stub, r300, "S3"), 400)
	wantStale(t, serveAndLoad(s, stub, revRecord("team/x", "2.5.0", 250), "S1"), 300)
}

// primeLatest loads rec as a fresh `latest` answer in the default mode, so the
// record lands in the content cache and the id@latest entry the way a
// production load writes them, then switches the bridge to mode.
func primeLatest(t *testing.T, s *mcpServer, stub *freshStub, rec loadArtifactResponse, mode string) {
	t.Helper()
	s.cfg.cacheMode = ""
	wantServed(t, serveAndLoad(s, stub, rec, "prime"), rec.ManifestBody)
	s.cfg.cacheMode = mode
}

// Spec: §6.5, §7.4 — a cache-served record is never compared and never
// advances the mark: the HEAD match, the 304, offline-first, offline-only, and
// the degraded-network fallback each deliver a cached record below the mark.
// The registry serves a record below the mark meanwhile, so a path that went
// to the network instead would be refused.
func TestFreshness_CacheServedPathsNotCompared(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, mode string
		stub       func(*freshStub, loadArtifactResponse)
		closeStub  bool
	}{
		{name: "HEAD match", mode: "always-revalidate", stub: func(f *freshStub, c loadArtifactResponse) { f.head = c.ContentHash }},
		{name: "304", mode: "", stub: func(f *freshStub, _ loadArtifactResponse) { f.notModified = true }},
		{name: "offline-first", mode: "offline-first"},
		{name: "offline-only", mode: "offline-only"},
		{name: "degraded fallback", mode: "always-revalidate", closeStub: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub, ts := newFreshStub(t)
			s := freshBridge(t, t.TempDir(), ts.URL, "")
			cached := revRecord("team/x", "1.0.0", 100)
			primeLatest(t, s, stub, cached, tc.mode)
			seedMark(t, s, "team/x", 500)
			stub.set(revRecord("team/x", "0.9.0", 50))
			if tc.stub != nil {
				stub.configure(func(f *freshStub) { tc.stub(f, cached) })
			}
			if tc.closeStub {
				ts.Close()
			}
			wantServed(t, s.loadArtifact(loadArgs("team/x", "S")), "body 1.0.0\n")
			wantMark(t, s, "team/x", 500)
		})
	}
}

// Spec: §6.5, §4.7.9 — the refetch that replaces a cached record whose
// signature fails under the current key set is a fresh answer, so a refetched
// body below the mark is refused.
func TestFreshness_SignatureRefetchCompared(t *testing.T) {
	t.Parallel()
	stub, ts := newFreshStub(t)
	gen := func() sign.RegistryManagedKey {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		return sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub}
	}
	retired, current := gen(), gen()
	s := freshBridge(t, t.TempDir(), ts.URL, "offline-first")
	s.cfg.verifyPolicy = sign.PolicyAlways
	s.cfg.signatureProvider = "registry-managed"
	s.cfg.verifier = sign.RegistryManagedKey{Trusted: []ed25519.PublicKey{current.PublicKey}}

	cached := signDelivery(t, retired, revRecord("team/x", "1.0.0", 100))
	if err := s.cacheVerifiedRecord(cached); err != nil {
		t.Fatalf("cacheVerifiedRecord: %v", err)
	}
	putLatest(s.resolutions, "team/x", "1.0.0", cached.ContentHash, time.Now())
	seedMark(t, s, "team/x", 500)
	stub.set(signDelivery(t, current, revRecord("team/x", "1.0.0", 100)))

	wantStale(t, s.loadArtifact(loadArgs("team/x", "S")), 500)
	wantMark(t, s, "team/x", 500)
}

// Spec: §6.4, §6.5 — an ID the workspace overlay serves never reaches the
// registry and is not compared.
func TestFreshness_OverlayNotCompared(t *testing.T) {
	t.Parallel()
	stub, ts := newFreshStub(t)
	s := freshBridge(t, t.TempDir(), ts.URL, "")
	seedMark(t, s, "team/x", 500)
	stub.set(revRecord("team/x", "0.9.0", 50))
	fm := []byte("---\ntype: context\nversion: 1.0.0\n---\n")
	s.setOverlay([]filesystem.ArtifactRecord{{
		ID:            "team/x",
		ArtifactBytes: fm,
		AuthoredBytes: fm,
		Artifact:      &manifest.Artifact{Type: manifest.TypeContext, Version: "1.0.0", Body: "overlay body"},
	}}, nil)
	out := s.loadArtifact(loadArgs("team/x", "S"))
	m, ok := out.(map[string]any)
	if !ok || m["layer"] != "overlay" {
		t.Fatalf("load = %v, want the overlay record", out)
	}
	wantMark(t, s, "team/x", 500)
}

// Spec: §6.5 — with the index DB locked by another handle, the bridge logs one
// warning naming the cache directory and refuses a stale answer from its
// in-memory mark. The two loads carry different host sessions, so the refusal
// comes from the mark and not from a session reference. The test swaps the
// global log output, so it does not run in parallel.
func TestFreshness_LockedIndexUsesInMemoryMark(t *testing.T) {
	stub, ts := newFreshStub(t)
	dir := t.TempDir()
	lockIndex(t, dir)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	s := freshBridge(t, dir, ts.URL, "")
	log.SetOutput(os.Stderr)
	if s.resolutions.db != nil {
		t.Fatal("the bridge opened a locked index")
	}
	if out := buf.String(); strings.Count(out, "\n") != 1 || !strings.Contains(out, dir) {
		t.Errorf("warning = %q; want one line naming %s", out, dir)
	}
	wantServed(t, serveAndLoad(s, stub, revRecord("team/x", "2.0.0", 200), "S1"), "body 2.0.0\n")
	wantStale(t, serveAndLoad(s, stub, revRecord("team/x", "1.0.0", 100), "S2"), 200)
}

// Spec: §6.5, §4.7.10 — a latest answer whose artifact_revision is absent or
// non-canonical is refused, and a pinned answer carrying the same value is
// delivered. Each record frames the served value, so the delivery check
// passes and the refusal is the freshness check's.
func TestFreshness_NonCanonicalRevisionRefusedOnLatest(t *testing.T) {
	t.Parallel()
	for _, served := range []string{"", "100", "2025-01-01T00:00:00Z"} {
		stub, ts := newFreshStub(t)
		s := freshBridge(t, t.TempDir(), ts.URL, "")
		rec := revRecord("team/x", "1.0.0", 0)
		rec.ArtifactRevision = served
		rec.DeliveryHash = servedDeliveryHashOf(rec)
		stub.set(rec)

		out := s.loadArtifact(loadArgs("team/x", "S"))
		m, _ := out.(map[string]any)
		if m["code"] != "materialize.stale_resolution" {
			t.Errorf("latest with %q: load = %v, want materialize.stale_resolution", served, out)
		}
		args := loadArgs("team/x", "S")
		args["version"] = "1.0.0"
		wantServed(t, s.loadArtifact(args), "body 1.0.0\n")
	}
}

// Spec: §6.5, §4.4.1 — an answer that passes the check and is refused by a
// §4.4.1 gate advances no mark and records no session reference.
func TestFreshness_GateRefusalRecordsNothing(t *testing.T) {
	t.Parallel()
	stub, ts := newFreshStub(t)
	s := freshBridge(t, t.TempDir(), ts.URL, "")
	wantServed(t, serveAndLoad(s, stub, revRecord("team/x", "2.0.0", 200), "S1"), "body 2.0.0\n")

	gated := cachedRecord("team/x", sandboxedFM, "body\n", nil)
	gated.ArtifactRevision = rev(300)
	out := serveAndLoad(s, stub, sealDelivery(gated), "S2")
	wantRefused(t, out, "materialize.sandbox_unsupported")
	wantMark(t, s, "team/x", 200)
	if hasSessionRef(s, "team/x", "S2") {
		t.Error("a gate refusal recorded a session reference")
	}
}

// readResource runs resources/read for id.
func readResource(s *mcpServer, id string) any {
	return s.handleResourcesRead(json.RawMessage(`{"uri":"` + resourceURIPrefix + id + `"}`))
}

// Spec: §5.0, §6.5 — the resources mirror compares its answer, never advances
// the mark, and records a session reference for the bridge's own session only
// on a pass. A refused mirror answer records none, so a later load_artifact
// with no session_id is compared with the mark.
func TestFreshness_ResourcesMirror(t *testing.T) {
	t.Parallel()
	stub, ts := newFreshStub(t)
	s := freshBridge(t, t.TempDir(), ts.URL, "")
	wantServed(t, serveAndLoad(s, stub, revRecord("team/x", "2.0.0", 200), "S0"), "body 2.0.0\n")

	stub.set(revRecord("team/x", "1.0.0", 100))
	wantStale(t, readResource(s, "team/x"), 200)
	wantMark(t, s, "team/x", 200)
	wantStale(t, s.loadArtifact(loadArgs("team/x", "")), 200)

	stub.set(revRecord("team/x", "3.0.0", 300))
	out, ok := readResource(s, "team/x").(map[string]any)
	if !ok || out["contents"] == nil {
		t.Fatalf("resources/read = %v, want contents", out)
	}
	wantMark(t, s, "team/x", 200)
	if ref, ok := s.resolutions.Reference(s.markKey("team/x"), s.sessionID); !ok || ref != 300 || !hasSessionRef(s, "team/x", s.sessionID) {
		t.Errorf("bridge session reference = (%d, %v), want 300", ref, ok)
	}
}

// Spec: §6.5 — run under -race: two latest loads at revisions 100 and 200,
// under different host sessions, run concurrently. In every interleaving the
// 200 load delivers, the 100 load delivers or is refused with
// materialize.stale_resolution, the mark ends at 200, and id@latest names the
// 200 version. TestFreshness_StaleLatestRefusedAndWritesNothing and
// TestFreshness_EqualAndHigherDelivered cover the two sequential orders.
func TestFreshness_ConcurrentLoads(t *testing.T) {
	t.Parallel()
	stub, ts := newFreshStub(t)
	s := freshBridge(t, t.TempDir(), ts.URL, "")
	stub.setFor("low", revRecord("team/x", "1.0.0", 100))
	stub.setFor("high", revRecord("team/x", "2.0.0", 200))

	var wg sync.WaitGroup
	outs := map[string]any{}
	var mu sync.Mutex
	for _, session := range []string{"low", "high"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := s.loadArtifact(loadArgs("team/x", session))
			mu.Lock()
			outs[session] = out
			mu.Unlock()
		}()
	}
	wg.Wait()

	wantServed(t, outs["high"], "body 2.0.0\n")
	if m, _ := outs["low"].(map[string]any); m["error"] != nil && m["code"] != "materialize.stale_resolution" {
		t.Errorf("low load = %v, want delivered or materialize.stale_resolution", m)
	}
	wantMark(t, s, "team/x", 200)
	if v := latestVersion(t, s.resolutions, "team/x"); v != "2.0.0" {
		t.Errorf("id@latest = %q, want 2.0.0", v)
	}
}

// Spec: §6.5, §4.7.6 — a cached record delivered on a HEAD match or a 304
// records the session reference the registry's pin implies, so the session's
// later pinned answer below an advanced mark is delivered. The mirror path
// does the same for the bridge's own session when its answer equals the mark.
func TestFreshness_RevalidatedAndMirrorPathsRecordSession(t *testing.T) {
	t.Parallel()
	r300, r400 := revRecord("team/x", "3.0.0", 300), revRecord("team/x", "4.0.0", 400)
	for name, revalidate := range map[string]func(*mcpServer, *freshStub){
		"HEAD match": func(s *mcpServer, f *freshStub) {
			s.cfg.cacheMode = "always-revalidate"
			f.configure(func(f *freshStub) { f.head = r300.ContentHash })
		},
		"304": func(_ *mcpServer, f *freshStub) {
			f.configure(func(f *freshStub) { f.notModified = true })
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			stub, ts := newFreshStub(t)
			s := freshBridge(t, t.TempDir(), ts.URL, "")
			primeLatest(t, s, stub, r300, "")
			wantMark(t, s, "team/x", 300)

			revalidate(s, stub)
			wantServed(t, s.loadArtifact(loadArgs("team/x", "S")), "body 3.0.0\n")
			stub.configure(func(f *freshStub) { f.head, f.notModified = "", false })
			wantServed(t, serveAndLoad(s, stub, r400, "O"), "body 4.0.0\n")
			wantMark(t, s, "team/x", 400)
			wantServed(t, serveAndLoad(s, stub, r300, "S"), "body 3.0.0\n")
			wantMark(t, s, "team/x", 400)
		})
	}
	t.Run("mirror", func(t *testing.T) {
		t.Parallel()
		stub, ts := newFreshStub(t)
		s := freshBridge(t, t.TempDir(), ts.URL, "")
		primeLatest(t, s, stub, r300, "")
		wantMark(t, s, "team/x", 300)

		stub.set(r300)
		if out, ok := readResource(s, "team/x").(map[string]any); !ok || out["contents"] == nil {
			t.Fatalf("resources/read = %v, want contents", out)
		}
		wantServed(t, serveAndLoad(s, stub, r400, "H"), "body 4.0.0\n")
		wantMark(t, s, "team/x", 400)
		wantServed(t, serveAndLoad(s, stub, r300, ""), "body 3.0.0\n")
		wantMark(t, s, "team/x", 400)
	})
}

// Spec: §6.5 — session references live in process memory. After a restart
// over the same cache directory, a session's pinned answer below the
// persisted mark is refused, and a new session's current answer is delivered.
func TestFreshness_RestartComparesWithPersistedMark(t *testing.T) {
	t.Parallel()
	stub, ts := newFreshStub(t)
	dir := t.TempDir()
	first := freshBridge(t, dir, ts.URL, "")
	wantServed(t, serveAndLoad(first, stub, revRecord("team/x", "3.0.0", 300), "S"), "body 3.0.0\n")
	wantServed(t, serveAndLoad(first, stub, revRecord("team/x", "4.0.0", 400), "O"), "body 4.0.0\n")
	if err := first.resolutions.Close(); err != nil {
		t.Fatal(err)
	}

	second := freshBridge(t, dir, ts.URL, "")
	wantStale(t, serveAndLoad(second, stub, revRecord("team/x", "3.0.0", 300), "S"), 400)
	wantServed(t, serveAndLoad(second, stub, revRecord("team/x", "4.0.0", 400), "S2"), "body 4.0.0\n")
}

// Spec: §6.5, §13.2.1 — the X-Podium-Read-Only headers grant no exemption: a
// stale answer that carries them is refused with the same envelope as one
// that does not.
func TestFreshness_ReadOnlyHeaderGrantsNoExemption(t *testing.T) {
	t.Parallel()
	envelopes := map[bool]map[string]any{}
	for _, readOnly := range []bool{false, true} {
		stub, ts := newFreshStub(t)
		s := freshBridge(t, t.TempDir(), ts.URL, "")
		wantServed(t, serveAndLoad(s, stub, revRecord("team/x", "2.0.0", 200), "s0"), "body 2.0.0\n")
		stub.configure(func(f *freshStub) { f.readOnly = readOnly })
		out := serveAndLoad(s, stub, revRecord("team/x", "1.0.0", 100), "S")
		wantStale(t, out, 200)
		envelopes[readOnly] = out.(map[string]any)
	}
	if !reflect.DeepEqual(envelopes[false], envelopes[true]) {
		t.Errorf("read-only envelope = %v, want %v", envelopes[true], envelopes[false])
	}
}

// Spec: §6.5, §13.2.1 — read-only lag under the bridge's own session. The
// registry pins the session to the lagging replica's answer and keeps that
// pin after read-only mode ends (pkg/registry/core/core.go:1715), so the stub
// keeps serving 100 without the read-only headers. The same bridge refuses it
// again; a new bridge over the same cache directory, with a new session,
// receives the current answer and delivers it.
func TestFreshness_ReadOnlyLagNeedsNewSession(t *testing.T) {
	t.Parallel()
	stub, ts := newFreshStub(t)
	dir := t.TempDir()
	s := freshBridge(t, dir, ts.URL, "")
	r200, r100 := revRecord("team/x", "2.0.0", 200), revRecord("team/x", "1.0.0", 100)
	wantServed(t, serveAndLoad(s, stub, r200, ""), "body 2.0.0\n")

	stub.configure(func(f *freshStub) { f.readOnly = true })
	wantStale(t, serveAndLoad(s, stub, r100, ""), 200)
	stub.configure(func(f *freshStub) { f.readOnly = false })
	wantStale(t, serveAndLoad(s, stub, r100, ""), 200)
	if err := s.resolutions.Close(); err != nil {
		t.Fatal(err)
	}

	next := freshBridge(t, dir, ts.URL, "")
	next.sessionID = "bridge-session-2"
	wantServed(t, serveAndLoad(next, stub, r200, ""), "body 2.0.0\n")
	wantMark(t, next, "team/x", 200)
}

// Spec: §6.5 — the session's first accepted answer is the reference, rather
// than the session maximum or its last accepted answer. An implementation
// that raised the reference on each accepted answer would refuse the second
// 300 answer with reference_revision 400.
func TestFreshness_FirstAcceptedAnswerIsTheReference(t *testing.T) {
	t.Parallel()
	stub, ts := newFreshStub(t)
	s := freshBridge(t, t.TempDir(), ts.URL, "")
	r300 := revRecord("team/x", "3.0.0", 300)
	wantServed(t, serveAndLoad(s, stub, r300, "S"), "body 3.0.0\n")
	wantServed(t, serveAndLoad(s, stub, revRecord("team/x", "4.0.0", 400), "S"), "body 4.0.0\n")
	wantMark(t, s, "team/x", 400)
	wantServed(t, serveAndLoad(s, stub, r300, "S"), "body 3.0.0\n")
	wantMark(t, s, "team/x", 400)
	if v := latestVersion(t, s.resolutions, "team/x"); v != "4.0.0" {
		t.Errorf("id@latest = %q, want 4.0.0", v)
	}
	wantStale(t, serveAndLoad(s, stub, revRecord("team/x", "2.5.0", 250), "S"), 300)
}

// Spec: §6.5 — session references and marks are per artifact ID, in the index
// DB and in memory alike. A session's reference for A does not admit a
// replayed B below B's mark, and A's reference does not refuse an honest
// first answer for B that has no mark.
func TestFreshness_PerArtifactIsolation(t *testing.T) {
	t.Parallel()
	for name, locked := range map[string]bool{"index DB": false, "in memory": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			bridge := func() (*mcpServer, *freshStub) {
				stub, ts := newFreshStub(t)
				dir := t.TempDir()
				if locked {
					lockIndex(t, dir)
				}
				return freshBridge(t, dir, ts.URL, ""), stub
			}
			s, stub := bridge()
			wantServed(t, serveAndLoad(s, stub, revRecord("team/a", "3.0.0", 300), "S"), "body 3.0.0\n")
			wantServed(t, serveAndLoad(s, stub, revRecord("team/b", "5.0.0", 500), "S2"), "body 5.0.0\n")
			wantStale(t, serveAndLoad(s, stub, revRecord("team/b", "3.0.0", 300), "S"), 500)

			s, stub = bridge()
			wantServed(t, serveAndLoad(s, stub, revRecord("team/a", "3.0.0", 300), "S"), "body 3.0.0\n")
			wantServed(t, serveAndLoad(s, stub, revRecord("team/b", "1.0.0", 100), "S"), "body 1.0.0\n")
		})
	}
}
