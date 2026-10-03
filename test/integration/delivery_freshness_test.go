package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/lennylabs/podium/internal/revmark"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
	"github.com/lennylabs/podium/pkg/version"
)

// dfArtifact is the artifact every delivery-freshness step loads.
const dfArtifact = "x"

// dfBase anchors the fixture's ingest times. Each stored version gets its own
// strictly increasing IngestedAt, so every version serves a distinct
// artifact_revision; a record written with a zero IngestedAt would serve the
// epoch for every version and the equality rule would admit any replay.
var dfBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// replayProxy fronts the registry handler. While capturing it records the
// first GET /v1/load_artifact answer; while replaying it answers every GET
// /v1/load_artifact with that captured body, which stands for a party that
// recorded a signed answer and serves it again later. It holds only a GET
// body, so in replay mode it refuses HEAD, and the bridge falls through to the
// full fetch whose answer the §6.5 freshness check compares.
type replayProxy struct {
	next http.Handler

	// mu guards every field below.
	mu       sync.Mutex
	capture  bool
	replay   bool
	captured []byte
	header   http.Header
}

func (p *replayProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/load_artifact" {
		p.next.ServeHTTP(w, r)
		return
	}
	p.mu.Lock()
	replay, capture := p.replay, p.capture && p.captured == nil
	body, header := p.captured, p.header
	p.mu.Unlock()
	switch {
	case replay && r.Method == http.MethodHead:
		w.WriteHeader(http.StatusMethodNotAllowed)
	case replay:
		for k, v := range header {
			w.Header()[k] = v
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	case capture && r.Method == http.MethodGet:
		rec := httptest.NewRecorder()
		p.next.ServeHTTP(rec, r)
		if rec.Code == http.StatusOK {
			p.mu.Lock()
			p.captured, p.header = rec.Body.Bytes(), rec.Header().Clone()
			p.mu.Unlock()
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	default:
		p.next.ServeHTTP(w, r)
	}
}

func (p *replayProxy) setCapture(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.capture = on
}

func (p *replayProxy) setReplay(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.replay = on
}

// dfFixture is a registry over a memory store behind server.New, signing
// delivery hashes with a registry-managed key and fronted by a replayProxy.
type dfFixture struct {
	st    *store.Memory
	proxy *replayProxy
	url   string
	// verifyEnv points the bridge at the fixture's verification key under
	// the always policy, so every delivery is signature-checked.
	verifyEnv []string
}

func newDFFixture(t *testing.T) *dfFixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	st := store.NewMemory()
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	srv := server.New(reg, server.WithDeliverySigner(sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub}))
	proxy := &replayProxy{next: srv.Handler()}
	ts := httptest.NewServer(proxy)
	t.Cleanup(ts.Close)
	return &dfFixture{
		st:    st,
		proxy: proxy,
		url:   ts.URL,
		verifyEnv: []string{
			"PODIUM_VERIFY_SIGNATURES=always",
			"PODIUM_SIGNATURE_PROVIDER=registry-managed",
			"PODIUM_SIGNATURE_VERIFY_KEY=" + base64.StdEncoding.EncodeToString(pub),
		},
	}
}

// put stores version v of dfArtifact ingested at dfBase plus offset seconds,
// born deprecated when deprecated is set, and returns its served revision.
func (f *dfFixture) put(t *testing.T, v string, offset int, deprecated bool) string {
	t.Helper()
	fm := "---\ntype: context\nversion: " + v + "\ndescription: x\n"
	if deprecated {
		fm += "deprecated: true\n"
	}
	fm += "---\n\nbody " + v + "\n"
	at := dfBase.Add(time.Duration(offset) * time.Second)
	rec := storetest.Seal(t, store.ManifestRecord{
		TenantID: "default", ArtifactID: dfArtifact, Version: v, Layer: "L",
		Type: "context", Deprecated: deprecated, IngestedAt: at, Frontmatter: []byte(fm),
	}, nil, nil)
	if err := f.st.PutManifest(context.Background(), rec); err != nil {
		t.Fatalf("PutManifest %s: %v", v, err)
	}
	return version.FormatArtifactRevision(at)
}

// dfIndex reads the resolved version of the id@latest entry and the revision
// mark for dfArtifact from the bridge's index DB under cacheDir. No bridge
// process runs while it reads, so the BoltDB lock is free.
func dfIndex(t *testing.T, cacheDir, registry string) (latest string, mark string) {
	t.Helper()
	db, err := bolt.Open(revmark.IndexPath(cacheDir), 0o600, &bolt.Options{Timeout: 2 * time.Second, ReadOnly: true})
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() { _ = db.Close() }()
	key := revmark.Key{Registry: revmark.NormalizeRegistry(registry), ArtifactID: dfArtifact}.Encode()
	err = db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket([]byte("resolutions")); b != nil {
			var e struct {
				ResolvedVersion string `json:"resolved_version"`
			}
			if raw := b.Get([]byte(dfArtifact + "@latest")); raw != nil {
				if err := json.Unmarshal(raw, &e); err != nil {
					return err
				}
			}
			latest = e.ResolvedVersion
		}
		if b := tx.Bucket(revmark.Bucket()); b != nil {
			if raw := b.Get(key); raw != nil {
				micros, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64)
				if err != nil {
					return err
				}
				mark = version.FormatArtifactRevision(time.UnixMicro(micros))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	return latest, mark
}

// dfWantDelivered fails unless res is a delivery of version want. The host
// result carries no artifact_revision, so the steps read the served revision
// back from the persisted mark.
func dfWantDelivered(t *testing.T, step string, res map[string]any, want string) {
	t.Helper()
	if res["code"] != nil || res["error"] != nil {
		t.Fatalf("%s: load refused: %v", step, res)
	}
	if res["version"] != want {
		t.Fatalf("%s: delivered version = %v, want %s", step, res["version"], want)
	}
}

// Spec: §4.7.10, §4.7.6, §6.5 — honest regressions of `latest` and a replay.
// The registry's `latest` is the most recently ingested non-deprecated
// version (§4.7.6), and the delivery record carries the served version's own
// ingest time (§4.7.10). A version born deprecated leaves `latest` on the
// previous version at its unchanged revision, and a backport ingested later
// becomes `latest` with a newer revision although its semver is lower, so
// neither regression trips the §6.5 freshness check. A replay of the first
// signed `latest` answer verifies against the registry key but carries an
// older revision than the persisted mark, so the bridge refuses it with
// materialize.stale_resolution and leaves id@latest untouched. A pinned
// version is never compared, so the same replayed body answering an explicit
// version: 1.0.0 load is delivered.
//
// Every load is a separate podium-mcp process over one PODIUM_CACHE_DIR: the
// bridge always sends a session_id and the registry pins `latest` per session,
// so a second load in one process would see the pinned version; a new process
// also exercises the persisted mark rather than a session reference. Every
// load reaches the same proxy URL, so every load shares one mark key.
func TestDeliveryFreshness_HonestRegressionsAndReplay(t *testing.T) {
	t.Parallel()
	f := newDFFixture(t)
	bin := buildMCP(t)
	cache := t.TempDir()
	load := func(args map[string]any) map[string]any {
		return loadArtifactWith(t, bin, f.url, cache, args, f.verifyEnv...)
	}
	latestArgs := map[string]any{"id": dfArtifact}

	// Step 1: 1.0.0 is delivered and its signed answer captured; 2.0.0,
	// ingested later, is delivered next.
	rev100 := f.put(t, "1.0.0", 1, false)
	f.proxy.setCapture(true)
	dfWantDelivered(t, "step 1 (1.0.0)", load(latestArgs), "1.0.0")
	f.proxy.setCapture(false)
	rev200 := f.put(t, "2.0.0", 2, false)
	dfWantDelivered(t, "step 1 (2.0.0)", load(latestArgs), "2.0.0")
	if _, mark := dfIndex(t, cache, f.url); mark != rev200 {
		t.Fatalf("step 1: mark = %q, want %q", mark, rev200)
	}

	// Step 2: 3.0.0 born deprecated is skipped by `latest`, which stays on
	// 2.0.0 at its unchanged revision; the mark does not move.
	f.put(t, "3.0.0", 3, true)
	dfWantDelivered(t, "step 2", load(latestArgs), "2.0.0")
	if latest, mark := dfIndex(t, cache, f.url); latest != "2.0.0" || mark != rev200 {
		t.Fatalf("step 2: id@latest = %q, mark = %q; want 2.0.0, %q", latest, mark, rev200)
	}

	// Step 3: the backport 1.0.1 is the most recently ingested non-deprecated
	// version, so `latest` moves down in semver and up in revision.
	rev101 := f.put(t, "1.0.1", 4, false)
	dfWantDelivered(t, "step 3", load(latestArgs), "1.0.1")
	if latest, mark := dfIndex(t, cache, f.url); latest != "1.0.1" || mark != rev101 {
		t.Fatalf("step 3: id@latest = %q, mark = %q; want 1.0.1, %q", latest, mark, rev101)
	}

	// Step 4: the replayed step-1 answer is refused, and the index keeps
	// naming 1.0.1 at its mark.
	f.proxy.setReplay(true)
	refused := load(latestArgs)
	if refused["code"] != "materialize.stale_resolution" {
		t.Fatalf("step 4: code = %v, want materialize.stale_resolution (result=%v)", refused["code"], refused)
	}
	if refused["retryable"] != false {
		t.Errorf("step 4: retryable = %v, want false", refused["retryable"])
	}
	details, _ := refused["details"].(map[string]any)
	if details["served_version"] != "1.0.0" || details["served_revision"] != rev100 || details["reference_revision"] != rev101 {
		t.Errorf("step 4: details = %v, want served 1.0.0 at %s against %s", details, rev100, rev101)
	}
	if latest, mark := dfIndex(t, cache, f.url); latest != "1.0.1" || mark != rev101 {
		t.Fatalf("step 4: id@latest = %q, mark = %q; want 1.0.1, %q", latest, mark, rev101)
	}

	// Step 5: the same replayed body answering a pinned version is delivered,
	// because a non-empty version is never compared.
	pinned := load(map[string]any{"id": dfArtifact, "version": "1.0.0"})
	dfWantDelivered(t, "step 5", pinned, "1.0.0")
	if latest, mark := dfIndex(t, cache, f.url); latest != "1.0.1" || mark != rev101 {
		t.Fatalf("step 5: id@latest = %q, mark = %q; want 1.0.1, %q", latest, mark, rev101)
	}
}
