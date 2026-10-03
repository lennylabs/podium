package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// switchObjects is an object store that counts every Get and fails each one
// while failing is set, standing in for an object-storage outage.
type switchObjects struct {
	*objectstore.Memory
	reads   atomic.Int64
	failing atomic.Bool
}

func (s *switchObjects) Get(ctx context.Context, key string) ([]byte, error) {
	s.reads.Add(1)
	if s.failing.Load() {
		return nil, errors.New("object storage outage")
	}
	return s.Memory.Get(ctx, key)
}

// admEnv is one SQLite-backed registry behind the HTTP server, with admission
// wired to a switchable object store and, optionally, a signer.
type admEnv struct {
	st      *store.SQLite
	db      *sql.DB
	objects *switchObjects
	key     sign.RegistryManagedKey
	other   sign.RegistryManagedKey
	ts      *httptest.Server
	mu      sync.Mutex
	events  []core.AuditEvent
}

// scopeHeader carries the test caller's §6.3.1 scopes; a request without it
// is a public caller.
const scopeHeader = "X-Test-Scopes"

func admEnvKey(t *testing.T) sign.RegistryManagedKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return sign.RegistryManagedKey{PrivateKey: priv}
}

func newAdmEnv(t *testing.T, signing bool) *admEnv {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.db")
	st, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e := &admEnv{st: st, db: db, objects: &switchObjects{Memory: objectstore.NewMemory()}, key: admEnvKey(t), other: admEnvKey(t)}
	var signer sign.Provider
	if signing {
		signer = e.key
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	}).WithAdmission(signer, e.objects, 200*time.Millisecond).WithAudit(e.emit)
	srv := server.New(reg,
		server.WithObjectStore(e.objects, "placeholder", time.Hour),
		server.WithIdentityResolver(func(r *http.Request) layer.Identity {
			scopes := r.Header.Get(scopeHeader)
			if scopes == "" {
				return layer.Identity{IsPublic: true}
			}
			return layer.Identity{Sub: "alice", IsAuthenticated: true, Scopes: strings.Split(scopes, ",")}
		}))
	e.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(e.ts.Close)
	return e
}

func (e *admEnv) emit(_ context.Context, ev core.AuditEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
}

func (e *admEnv) eventsOf(typ string) []core.AuditEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []core.AuditEvent
	for _, ev := range e.events {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

func (e *admEnv) put(t *testing.T, rec store.ManifestRecord) {
	t.Helper()
	if err := e.st.PutManifest(context.Background(), rec); err != nil {
		t.Fatalf("PutManifest %s@%s: %v", rec.ArtifactID, rec.Version, err)
	}
}

// exec edits the stored row directly, as a party with store write access.
func (e *admEnv) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := e.db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// object stores body and returns its "sha256:<hex>" content hash.
func (e *admEnv) object(t *testing.T, body []byte) string {
	t.Helper()
	sum := sha256.Sum256(body)
	key := hex.EncodeToString(sum[:])
	if err := e.objects.Put(context.Background(), key, body, "application/octet-stream"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	return "sha256:" + key
}

func admEnvManifest(ver, extra string) []byte {
	return []byte("---\ntype: context\nversion: " + ver + "\ndescription: admission fixture\n" + extra + "---\n\nbody " + ver + "\n")
}

// row is a sealed row whose manifest declares extends (empty for none) and
// whose stored pin is pin, signed by signer when it is non-nil.
func (e *admEnv) row(t *testing.T, id, extends, pin string, signer sign.Provider, resources ...store.ResourceRef) store.ManifestRecord {
	t.Helper()
	extra := ""
	if extends != "" {
		extra = "extends: " + extends + "\n"
	}
	return storetest.Seal(t, store.ManifestRecord{
		TenantID: "default", ArtifactID: id, Version: "1.0.0", Type: "context", Layer: "L",
		Frontmatter: admEnvManifest("1.0.0", extra), ExtendsPin: pin, Resources: resources,
		IngestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}, e.objects, signer)
}

// request sends one load_artifact request and returns the status, the raw
// body, and the response headers.
func (e *admEnv) request(t *testing.T, method, id string, header map[string]string) (int, []byte, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+"/v1/load_artifact?id="+id, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body, resp.Header
}

// batchItem sends a one-item artifacts:batchLoad and returns the item and the
// raw response body.
func (e *admEnv) batchItem(t *testing.T, id string) (server.BatchLoadEnvelope, []byte) {
	t.Helper()
	resp, err := http.Post(e.ts.URL+"/v1/artifacts:batchLoad", "application/json", strings.NewReader(`{"ids":["`+id+`"]}`))
	if err != nil {
		t.Fatalf("POST batchLoad: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var items []server.BatchLoadEnvelope
	if err := json.Unmarshal(raw, &items); err != nil || len(items) != 1 {
		t.Fatalf("batchLoad body %s: %v", raw, err)
	}
	return items[0], raw
}

// wantCode asserts a single-load error envelope's status, code, and retryable
// flag.
func wantCode(t *testing.T, status int, body []byte, wantStatus int, code string, retryable bool) {
	t.Helper()
	var env server.ErrorResponse
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope %s: %v", body, err)
	}
	if status != wantStatus || env.Code != code || env.Retryable != retryable {
		t.Fatalf("status %d code %q retryable %v, want %d %q %v: %s", status, env.Code, env.Retryable, wantStatus, code, retryable, body)
	}
}

// wantRefusedEverywhere asserts the single load and the batch item carry code,
// and that neither body names any of hidden.
func (e *admEnv) wantRefusedEverywhere(t *testing.T, id, code string, hidden ...string) {
	t.Helper()
	status, body, _ := e.request(t, http.MethodGet, id, nil)
	wantCode(t, status, body, http.StatusInternalServerError, code, false)
	item, raw := e.batchItem(t, id)
	if item.Status != "error" || item.Error == nil || item.Error.Code != code || item.Error.Retryable {
		t.Fatalf("batch item = %s, want error %s", raw, code)
	}
	for _, h := range hidden {
		if strings.Contains(string(body), h) || strings.Contains(string(raw), h) {
			t.Errorf("a refusal body names %q:\n%s\n%s", h, body, raw)
		}
	}
}

// Spec: §13.4 — a load of a row admission refuses answers the refusal's
// §6.10 code on the single load (status 500, not retryable) and on the batch
// item, and names only the requested artifact.
func TestLoadArtifact_InadmissibleRowAnswersItsCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		seed   func(t *testing.T, e *admEnv)
		code   string
		hidden []string
	}{
		{
			name: "bytes altered with the hash unchanged",
			seed: func(t *testing.T, e *admEnv) {
				e.put(t, e.row(t, "team/c", "", "", e.key))
				e.exec(t, `UPDATE manifests SET frontmatter = ? WHERE artifact_id = 'team/c'`, admEnvManifest("1.0.0", "sensitivity: low\n"))
			},
			code: "materialize.content_hash_mismatch",
		},
		{
			name: "envelope under another key",
			seed: func(t *testing.T, e *admEnv) { e.put(t, e.row(t, "team/c", "", "", e.other)) },
			code: "materialize.signature_invalid",
		},
		{
			name: "no envelope",
			seed: func(t *testing.T, e *admEnv) { e.put(t, e.row(t, "team/c", "", "", nil)) },
			code: "materialize.signature_missing",
		},
		{
			// The signature_unverified row a wrong key at upgrade leaves at the
			// pre-framing digest fails the hash first.
			name: "pre-framing digest with another key's envelope",
			seed: func(t *testing.T, e *admEnv) {
				rec := e.row(t, "team/c", "", "", nil)
				sum := sha256.Sum256(rec.Frontmatter)
				rec.ContentHash = "sha256:" + hex.EncodeToString(sum[:])
				sig, err := e.other.Sign(context.Background(), rec.ContentHash)
				if err != nil {
					t.Fatalf("Sign: %v", err)
				}
				rec.Signature = sig
				e.put(t, rec)
			},
			code: "materialize.content_hash_mismatch",
		},
		{
			name: "merged child whose parent row is altered",
			seed: func(t *testing.T, e *admEnv) {
				e.put(t, e.row(t, "base/parent", "", "", e.key))
				e.put(t, e.row(t, "team/c", "base/parent@1.0.0", "base/parent@1.0.0", e.key))
				e.exec(t, `UPDATE manifests SET frontmatter = ? WHERE artifact_id = 'base/parent'`, admEnvManifest("1.0.0", "sensitivity: high\n"))
			},
			code:   "materialize.content_hash_mismatch",
			hidden: []string{"base/parent", "sha256:"},
		},
		{
			name: "child pin cleared",
			seed: func(t *testing.T, e *admEnv) {
				e.put(t, e.row(t, "base/parent", "", "", e.key))
				e.put(t, e.row(t, "team/c", "base/parent@1.0.0", "", e.key))
			},
			code: "materialize.content_hash_mismatch",
		},
		{
			name: "child pin redirected to another artifact",
			seed: func(t *testing.T, e *admEnv) {
				e.put(t, e.row(t, "base/parent", "", "", e.key))
				e.put(t, e.row(t, "base/other", "", "", e.key))
				e.put(t, e.row(t, "team/c", "base/parent@1.0.0", "base/other@1.0.0", e.key))
			},
			code:   "materialize.content_hash_mismatch",
			hidden: []string{"base/other"},
		},
		{
			name: "pin added to a row declaring no extends",
			seed: func(t *testing.T, e *admEnv) {
				e.put(t, e.row(t, "base/parent", "", "", e.key))
				e.put(t, e.row(t, "team/c", "", "base/parent@1.0.0", e.key))
			},
			code:   "materialize.content_hash_mismatch",
			hidden: []string{"base/parent"},
		},
		{
			name: "parent given a pin toward a row the store does not hold",
			seed: func(t *testing.T, e *admEnv) {
				e.put(t, e.row(t, "base/parent", "", "ghost/row@1.0.0", e.key))
				e.put(t, e.row(t, "team/c", "base/parent@1.0.0", "base/parent@1.0.0", e.key))
			},
			code:   "materialize.content_hash_mismatch",
			hidden: []string{"base/parent", "ghost/row"},
		},
		{
			name: "parent given a pin pointed back at the child",
			seed: func(t *testing.T, e *admEnv) {
				e.put(t, e.row(t, "base/parent", "", "team/c@1.0.0", e.key))
				e.put(t, e.row(t, "team/c", "base/parent@1.0.0", "base/parent@1.0.0", e.key))
			},
			code:   "materialize.content_hash_mismatch",
			hidden: []string{"base/parent"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newAdmEnv(t, true)
			tc.seed(t, e)
			e.wantRefusedEverywhere(t, "team/c", tc.code, tc.hidden...)
		})
	}
}

// admIngested ingests one signed artifact with a small resource through the
// ingest path, which keeps the resource inline and also writes it to object
// storage, and returns its stored row.
func admIngested(t *testing.T, e *admEnv) store.ManifestRecord {
	t.Helper()
	if _, err := ingest.Ingest(context.Background(), e.st, ingest.Request{
		TenantID: "default", LayerID: "L",
		Files: fstest.MapFS{
			"ops/run/ARTIFACT.md": &fstest.MapFile{Data: admEnvManifest("1.0.0", "")},
			"ops/run/notes.txt":   &fstest.MapFile{Data: []byte("small notes")},
		},
		Linter:      lint.NewIngestLinter(true),
		ResourcePut: e.objects.Put,
		Signer:      e.key.Sign,
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	rec, err := e.st.GetManifest(context.Background(), "default", "ops/run", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	return rec
}

// Spec: §13.4, §7.6.2 — admission binds every ref's content hash and size to
// its body, and the serve paths take an inline ref's bytes from the admitted
// row, never from the object stored under its key.
func TestLoadArtifact_AdmissionBindsResourceRefs(t *testing.T) {
	t.Parallel()

	t.Run("inline ref content hash pointed at another object", func(t *testing.T) {
		t.Parallel()
		e := newAdmEnv(t, true)
		rec := admIngested(t, e)
		rec.ArtifactID = "ops/forged"
		rec.Resources[0].ContentHash = e.object(t, []byte("another object"))
		e.put(t, rec)
		item, raw := e.batchItem(t, "ops/forged")
		if item.Error == nil || item.Error.Code != "materialize.content_hash_mismatch" {
			t.Fatalf("batch item = %s, want materialize.content_hash_mismatch", raw)
		}
	})

	t.Run("object under an inline ref's key replaced", func(t *testing.T) {
		t.Parallel()
		e := newAdmEnv(t, true)
		rec := admIngested(t, e)
		key := strings.TrimPrefix(rec.Resources[0].ContentHash, "sha256:")
		if err := e.objects.Delete(context.Background(), key); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if err := e.objects.Put(context.Background(), key, []byte("replaced bytes"), ""); err != nil {
			t.Fatalf("Put: %v", err)
		}
		item, raw := e.batchItem(t, "ops/run")
		if item.Status != "ok" || len(item.Resources) != 1 {
			t.Fatalf("batch item = %s, want ok with one resource", raw)
		}
		if r := item.Resources[0]; r.Inline != "small notes" || r.PresignedURL != "" {
			t.Errorf("resource = %+v, want the admitted bytes inline and no presigned_url", r)
		}
	})

	t.Run("inline ref size raised above the cutoff with its hash redirected", func(t *testing.T) {
		t.Parallel()
		e := newAdmEnv(t, true)
		rec := admIngested(t, e)
		big := bytes.Repeat([]byte("b"), objectstore.InlineCutoff+8)
		rec.ArtifactID = "ops/forged"
		rec.Resources[0].ContentHash = e.object(t, big)
		rec.Resources[0].Size = int64(len(big))
		e.put(t, rec)
		status, body, _ := e.request(t, http.MethodGet, "ops/forged", nil)
		wantCode(t, status, body, http.StatusInternalServerError, "materialize.content_hash_mismatch", false)
	})
}

// admObjectRow seeds ops/r, a sealed unsigned row with one object-held body.
func admObjectRow(t *testing.T, e *admEnv) store.ManifestRecord {
	t.Helper()
	big := bytes.Repeat([]byte("o"), objectstore.InlineCutoff+8)
	rec := e.row(t, "ops/r", "", "", nil, store.ResourceRef{Path: "data/big.bin", ContentHash: e.object(t, big)})
	e.put(t, rec)
	return rec
}

// Spec: §13.4 — a HEAD and a conditional GET whose validator matches return
// no content and are answered from the resolved row without admission, so they
// read no object; every other GET is admitted.
func TestLoadArtifact_RevalidationSkipsAdmission(t *testing.T) {
	t.Parallel()
	e := newAdmEnv(t, false)
	rec := admObjectRow(t, e)
	etag := `"` + rec.ContentHash + `"`

	e.objects.failing.Store(true)
	status, body, _ := e.request(t, http.MethodGet, "ops/r", nil)
	wantCode(t, status, body, http.StatusInternalServerError, "registry.unavailable", true)
	status, body, _ = e.request(t, http.MethodGet, "ops/r", map[string]string{"If-None-Match": `"sha256:stale"`})
	wantCode(t, status, body, http.StatusInternalServerError, "registry.unavailable", true)

	e.objects.reads.Store(0)
	status, _, hdr := e.request(t, http.MethodHead, "ops/r", nil)
	if status != http.StatusOK || hdr.Get("X-Podium-Content-Hash") != rec.ContentHash {
		t.Fatalf("HEAD = %d %q, want 200 with the stored hash", status, hdr.Get("X-Podium-Content-Hash"))
	}
	status, _, _ = e.request(t, http.MethodGet, "ops/r", map[string]string{"If-None-Match": etag})
	if status != http.StatusNotModified {
		t.Fatalf("matching conditional GET = %d, want 304", status)
	}
	if n := e.objects.reads.Load(); n != 0 {
		t.Errorf("revalidation read %d object(s), want 0", n)
	}

	e.objects.failing.Store(false)
	e.exec(t, `UPDATE manifests SET frontmatter = ? WHERE artifact_id = 'ops/r'`, admEnvManifest("1.0.0", "sensitivity: high\n"))
	status, _, hdr = e.request(t, http.MethodHead, "ops/r", nil)
	if status != http.StatusOK || hdr.Get("X-Podium-Content-Hash") != rec.ContentHash || e.objects.reads.Load() != 0 {
		t.Fatalf("HEAD after the edit = %d %q reads %d, want 200 with the stored hash and no read", status, hdr.Get("X-Podium-Content-Hash"), e.objects.reads.Load())
	}
	status, body, _ = e.request(t, http.MethodGet, "ops/r", nil)
	wantCode(t, status, body, http.StatusInternalServerError, "materialize.content_hash_mismatch", false)
	if e.objects.reads.Load() == 0 {
		t.Errorf("a full GET read no object")
	}

	// If-None-Match: * matches before the validator is read, so it takes the
	// admitted result, and a row with an empty stored hash is refused.
	e.exec(t, `UPDATE manifests SET content_hash = '' WHERE artifact_id = 'ops/r'`)
	status, body, _ = e.request(t, http.MethodGet, "ops/r", map[string]string{"If-None-Match": "*"})
	wantCode(t, status, body, http.StatusInternalServerError, "materialize.content_hash_mismatch", false)
}

// Spec: §13.4, §6.3.1 — a revalidation is answered only after the load-scope
// check, so a caller outside its scope gets the full GET's answer, never a 304
// and never the row's validator.
func TestLoadArtifact_RevalidationOutsideScopeIsDenied(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{"podium:load:other/*", "podium:load:ops/r@2.x"} {
		t.Run(scope, func(t *testing.T) {
			t.Parallel()
			e := newAdmEnv(t, false)
			rec := e.row(t, "ops/r", "", "", nil)
			e.put(t, rec)
			auth := map[string]string{scopeHeader: scope}
			fullStatus, fullBody, _ := e.request(t, http.MethodGet, "ops/r", auth)
			conditional := map[string]string{scopeHeader: scope, "If-None-Match": `"` + rec.ContentHash + `"`}
			for _, method := range []string{http.MethodHead, http.MethodGet} {
				status, body, hdr := e.request(t, method, "ops/r", conditional)
				if status != fullStatus || status == http.StatusNotModified {
					t.Errorf("%s = %d, want the full GET's %d", method, status, fullStatus)
				}
				if method == http.MethodGet && !bytes.Equal(body, fullBody) {
					t.Errorf("GET body %s, want the full GET's %s", body, fullBody)
				}
				for _, h := range []string{"X-Podium-Content-Hash", "X-Podium-Version", "ETag"} {
					if hdr.Get(h) != "" {
						t.Errorf("%s carries %s", method, h)
					}
				}
			}
			if len(e.eventsOf("visibility.denied")) == 0 {
				t.Errorf("no visibility.denied event recorded")
			}
			for _, ev := range e.eventsOf("artifact.loaded") {
				if _, ok := ev.Context["content_hash"]; ok || ev.ResultSize != 0 {
					t.Errorf("denied read recorded %+v", ev)
				}
			}
		})
	}
}

// Spec: §13.4 — admission reads the object-held bodies of every row in the
// extends: chain, so an outage refuses a full load of a child whose own
// resources are all inline, while a HEAD of it reads nothing.
func TestLoadArtifact_ChainOutageRefusesInlineChild(t *testing.T) {
	t.Parallel()
	e := newAdmEnv(t, false)
	big := bytes.Repeat([]byte("p"), objectstore.InlineCutoff+8)
	e.put(t, e.row(t, "base/parent", "", "", nil, store.ResourceRef{Path: "data/parent.bin", ContentHash: e.object(t, big)}))
	e.put(t, e.row(t, "team/c", "base/parent@1.0.0", "base/parent@1.0.0", nil, store.ResourceRef{Path: "notes.txt", Inline: []byte("child notes")}))

	e.objects.failing.Store(true)
	status, body, _ := e.request(t, http.MethodGet, "team/c", nil)
	wantCode(t, status, body, http.StatusInternalServerError, "registry.unavailable", true)
	e.objects.reads.Store(0)
	status, _, _ = e.request(t, http.MethodHead, "team/c", nil)
	if status != http.StatusOK || e.objects.reads.Load() != 0 {
		t.Errorf("HEAD = %d with %d read(s), want 200 and none", status, e.objects.reads.Load())
	}
}
