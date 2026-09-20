package serverboot

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// --- fixtures -------------------------------------------------------------

// testSigner returns a registry-managed provider over a freshly generated
// Ed25519 keypair, which is the provider registrySignerFor hands the pass.
func testSigner(t *testing.T) sign.RegistryManagedKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub, KeyID: keyIDFor(pub)}
}

// rowSeed describes one stored row the test seeds at the digest the previous
// release computed over its bytes.
type rowSeed struct {
	tenant   string
	id       string
	version  string
	fm       []byte
	skill    []byte
	layer    string
	deleted  bool
	resource *seedResource
	// signWith mints the row's envelope over its seeded hash. Nil leaves
	// the row unsigned.
	signWith sign.Provider
	// framed seeds the row at the framed digest instead of the
	// pre-framing one.
	framed bool
	// hashOverride seeds the row at a literal hash, for the
	// unreproducible case.
	hashOverride string
}

// seedResource is one bundled resource, held inline or in the object store.
type seedResource struct {
	path     string
	body     []byte
	external bool
	// withhold keeps the body out of the object store, so its Get reports
	// ErrNotFound.
	withhold bool
}

func defaultFrontmatter(id string) []byte {
	return []byte("---\nid: " + id + "\ntype: skill\nname: " + id + "\ndescription: a test artifact\n---\n# " + id + "\n")
}

// seedRow provisions the tenant, writes the row at its seeded hash, uploads an
// external resource body, and tombstones the row's layer when asked. It returns
// the record as stored.
func seedRow(t *testing.T, st store.Store, objs objectstore.Provider, s rowSeed) store.ManifestRecord {
	t.Helper()
	ctx := context.Background()
	if s.fm == nil {
		s.fm = defaultFrontmatter(s.id)
	}
	if s.layer == "" {
		s.layer = "team"
	}
	if err := st.CreateTenant(ctx, store.Tenant{ID: s.tenant, Name: s.tenant}); err != nil {
		t.Fatalf("create tenant %s: %v", s.tenant, err)
	}
	bodies := map[string][]byte{}
	var refs []store.ResourceRef
	if r := s.resource; r != nil {
		bodies[r.path] = r.body
		ref := store.ResourceRef{
			Path:        r.path,
			ContentHash: "sha256:" + version.CanonicalContentHash(r.body, nil, nil),
			Size:        int64(len(r.body)),
			ContentType: "text/markdown",
		}
		if r.external {
			if objs != nil && !r.withhold {
				if err := objs.Put(ctx, strings.TrimPrefix(ref.ContentHash, "sha256:"), r.body, ref.ContentType); err != nil {
					t.Fatalf("put resource: %v", err)
				}
			}
		} else {
			ref.Inline = r.body
		}
		refs = append(refs, ref)
	}
	hash := "sha256:" + preFramingContentHash(s.fm, s.skill, bodies)
	if s.framed {
		hash = "sha256:" + version.CanonicalContentHash(s.fm, s.skill, bodies)
	}
	if s.hashOverride != "" {
		hash = s.hashOverride
	}
	rec := store.ManifestRecord{
		TenantID:    s.tenant,
		ArtifactID:  s.id,
		Version:     s.version,
		ContentHash: hash,
		Type:        "skill",
		Name:        s.id,
		Description: "a test artifact",
		Layer:       s.layer,
		Frontmatter: s.fm,
		SkillRaw:    s.skill,
		Resources:   refs,
		IngestedAt:  time.Now().UTC(),
	}
	if s.signWith != nil {
		env, err := s.signWith.Sign(ctx, hash)
		if err != nil {
			t.Fatalf("sign seed row: %v", err)
		}
		rec.Signature = env
	}
	if err := st.PutManifest(ctx, rec); err != nil {
		t.Fatalf("put manifest %s: %v", s.id, err)
	}
	if s.deleted {
		if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: s.tenant, ID: s.layer, SourceType: "local", LocalPath: "/tmp"}); err != nil {
			t.Fatalf("put layer config: %v", err)
		}
		if err := st.DeleteLayerConfig(ctx, s.tenant, s.layer); err != nil {
			t.Fatalf("delete layer config: %v", err)
		}
	}
	return rec
}

// framedHashOf is the value the pass must write for a seeded row.
func framedHashOf(s rowSeed) string {
	fm := s.fm
	if fm == nil {
		fm = defaultFrontmatter(s.id)
	}
	bodies := map[string][]byte{}
	if s.resource != nil {
		bodies[s.resource.path] = s.resource.body
	}
	return "sha256:" + version.CanonicalContentHash(fm, s.skill, bodies)
}

func readRow(t *testing.T, st store.Store, s rowSeed) store.ManifestRecord {
	t.Helper()
	recs, err := st.ListManifestsIncludingDeleted(context.Background(), s.tenant)
	if err != nil {
		t.Fatalf("list manifests: %v", err)
	}
	for _, rec := range recs {
		if rec.ArtifactID == s.id && rec.Version == s.version {
			return rec
		}
	}
	t.Fatalf("row %s/%s@%s is gone", s.tenant, s.id, s.version)
	return store.ManifestRecord{}
}

func markerSet(t *testing.T, st store.Store) bool {
	t.Helper()
	applied, err := st.DataMigrationApplied(context.Background(), store.DataMigrationContentHashFraming)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	return applied
}

// --- test doubles ---------------------------------------------------------

// countingStore records the reads and writes the pass makes and can fail any
// of them, so a test drives every arm of the failure-policy table without a
// real backend.
type countingStore struct {
	store.Store
	mu          sync.Mutex
	listTenants int
	listRows    int
	rehashes    int
	markerSets  int

	markerErr   error
	tenantsErr  error
	rowsErr     error
	rehashErr   error
	rehashFor   string // artifact id the rehashErr applies to; empty means all
	setMarkErr  error
	beforeApply func(*countingStore)
}

func (c *countingStore) DataMigrationApplied(ctx context.Context, name string) (bool, error) {
	if c.markerErr != nil {
		return false, c.markerErr
	}
	return c.Store.DataMigrationApplied(ctx, name)
}

func (c *countingStore) SetDataMigrationApplied(ctx context.Context, name string, applied bool) error {
	c.mu.Lock()
	c.markerSets++
	c.mu.Unlock()
	if c.setMarkErr != nil {
		return c.setMarkErr
	}
	return c.Store.SetDataMigrationApplied(ctx, name, applied)
}

func (c *countingStore) ListTenants(ctx context.Context) ([]store.Tenant, error) {
	c.mu.Lock()
	c.listTenants++
	c.mu.Unlock()
	if c.tenantsErr != nil {
		return nil, c.tenantsErr
	}
	return c.Store.ListTenants(ctx)
}

func (c *countingStore) ListManifestsIncludingDeleted(ctx context.Context, tenantID string) ([]store.ManifestRecord, error) {
	c.mu.Lock()
	c.listRows++
	c.mu.Unlock()
	if c.rowsErr != nil {
		return nil, c.rowsErr
	}
	return c.Store.ListManifestsIncludingDeleted(ctx, tenantID)
}

func (c *countingStore) RehashManifest(ctx context.Context, tenantID, artifactID, version, oldHash, newHash, signature string) error {
	c.mu.Lock()
	c.rehashes++
	first := c.rehashes == 1
	c.mu.Unlock()
	if first && c.beforeApply != nil {
		c.beforeApply(c)
	}
	if c.rehashErr != nil && (c.rehashFor == "" || c.rehashFor == artifactID) {
		return c.rehashErr
	}
	return c.Store.RehashManifest(ctx, tenantID, artifactID, version, oldHash, newHash, signature)
}

// failingObjects wraps an object store and fails Get for one key, or for every
// key, with an error the test names.
type failingObjects struct {
	objectstore.Provider
	mu   sync.Mutex
	gets int
	// failKeyFor names the artifact body whose Get fails; an empty value
	// fails every Get.
	failBody []byte
	err      error
}

func (f *failingObjects) Get(ctx context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	f.gets++
	f.mu.Unlock()
	body, err := f.Provider.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if f.err != nil && (f.failBody == nil || string(f.failBody) == string(body)) {
		return nil, f.err
	}
	return body, nil
}

func (f *failingObjects) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

// blockingObjects never answers a Get. honorContext selects whether it returns
// when the read's deadline fires, as S3 does, or blocks on a channel the test
// closes, as Filesystem.Get over a hung mount does.
type blockingObjects struct {
	objectstore.Provider
	mu            sync.Mutex
	gets          int
	honorContext  bool
	release       chan struct{}
	firstKeyBlock bool
}

func (b *blockingObjects) Get(ctx context.Context, key string) ([]byte, error) {
	b.mu.Lock()
	b.gets++
	b.mu.Unlock()
	if b.honorContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	<-b.release
	return nil, errors.New("objectstore: released")
}

func (b *blockingObjects) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.gets
}

// recordingSink collects the events the pass appends and can refuse them.
type recordingSink struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (r *recordingSink) Append(_ context.Context, e audit.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return r.err
}

func (r *recordingSink) Verify(context.Context) error { return nil }

func (r *recordingSink) all() []audit.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]audit.Event(nil), r.events...)
}

// deps is the dependency set every case starts from.
func deps(st store.Store, objs objectstore.Provider) rehashDeps {
	return rehashDeps{Store: st, Objects: objs, ReadTimeout: 5 * time.Second}
}

// --- case 1: the happy path and the gate ----------------------------------

// Spec: §4.7.6, §13.4 — the pass rewrites every stored row from the bytes the
// registry holds, across tenants, including a soft-deleted row a §8.4 restore
// would bring back and an extends: child, and records the completion so a later
// start reads no row at all.
func TestRehashStoredHashes_MigratesEveryTenantsRows(t *testing.T) {
	st := store.NewMemory()
	seeds := []rowSeed{
		{tenant: "acme", id: "alpha", version: "1.0.0", skill: []byte("# Alpha\n")},
		{tenant: "acme", id: "beta", version: "2.1.0", layer: "archived", deleted: true},
		{tenant: "globex", id: "gamma", version: "1.0.0", resource: &seedResource{path: "ref.md", body: []byte("R")}},
	}
	for _, s := range seeds {
		seedRow(t, st, nil, s)
	}
	// An extends: child stores its own pre-merge bytes and an ExtendsPin.
	child := rowSeed{tenant: "globex", id: "delta", version: "1.0.0", skill: []byte("# Delta\n")}
	seedRow(t, st, nil, child)
	seeds = append(seeds, child)

	if err := rehashStoredHashes(context.Background(), deps(st, nil)); err != nil {
		t.Fatalf("rehashStoredHashes: %v", err)
	}
	for _, s := range seeds {
		if got, want := readRow(t, st, s).ContentHash, framedHashOf(s); got != want {
			t.Errorf("%s/%s content_hash = %s, want the framed digest %s", s.tenant, s.id, got, want)
		}
	}
	if !markerSet(t, st) {
		t.Error("marker not set after a pass that rewrote every row")
	}

	// The gate: a second pass reads no tenant and no row.
	counting := &countingStore{Store: st}
	if err := rehashStoredHashes(context.Background(), deps(counting, nil)); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if counting.listTenants != 0 || counting.listRows != 0 {
		t.Errorf("second pass read %d tenant list(s) and %d row list(s), want 0 and 0", counting.listTenants, counting.listRows)
	}
}

// Spec: §4.7.6, §13.4 — a store with no rows runs an empty pass and records the
// completion, so a registry created on this release never pays for a second
// look.
func TestRehashStoredHashes_EmptyStoreSetsTheMarker(t *testing.T) {
	st := store.NewMemory()
	if err := rehashStoredHashes(context.Background(), deps(st, nil)); err != nil {
		t.Fatalf("rehashStoredHashes: %v", err)
	}
	if !markerSet(t, st) {
		t.Error("marker not set after an empty pass")
	}
}

// --- case 2: externally held bodies ---------------------------------------

// Spec: §4.7.6, §13.4 — the digest covers a bundled resource's full bytes, so a
// row whose resource exceeds the inline cutoff is rehashed over the body the
// pass fetches from object storage.
func TestRehashStoredHashes_FetchesExternallyHeldBodies(t *testing.T) {
	st := store.NewMemory()
	objs := objectstore.NewMemory()
	big := []byte(strings.Repeat("x", objectstore.InlineCutoff+1))
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", resource: &seedResource{path: "big.md", body: big, external: true}}
	seedRow(t, st, objs, s)

	if err := rehashStoredHashes(context.Background(), deps(st, objs)); err != nil {
		t.Fatalf("rehashStoredHashes: %v", err)
	}
	if got, want := readRow(t, st, s).ContentHash, framedHashOf(s); got != want {
		t.Errorf("content_hash = %s, want %s (the digest over the fetched body)", got, want)
	}
	if !markerSet(t, st) {
		t.Error("marker not set")
	}
}

// --- case 3: the signing predicate ----------------------------------------

// Spec: §4.7.6, §13.4 — rewriting a signed row while no signer is configured
// would strand its envelope over a hash the row no longer stores, so the pass
// refuses the start before it writes anything.
func TestRehashStoredHashes_RefusesToStrandASignature(t *testing.T) {
	signer := testSigner(t)
	st := store.NewMemory()
	signed := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", signWith: signer}
	unsigned := rowSeed{tenant: "acme", id: "beta", version: "1.0.0"}
	seedRow(t, st, nil, signed)
	before := seedRow(t, st, nil, unsigned)

	err := rehashStoredHashes(context.Background(), deps(st, nil))
	if err == nil {
		t.Fatal("rehashStoredHashes = nil, want a refusal over the signed row")
	}
	msg := err.Error()
	if !strings.Contains(msg, "PODIUM_SIGN") || !strings.Contains(msg, "PODIUM_SIGN_KEY_PATH") || !strings.Contains(msg, "acme/alpha@1.0.0") {
		t.Errorf("error = %q, want PODIUM_SIGN, PODIUM_SIGN_KEY_PATH and the row named", msg)
	}
	if got := readRow(t, st, unsigned).ContentHash; got != before.ContentHash {
		t.Errorf("unsigned row beside the refusal was written: %s", got)
	}
	if markerSet(t, st) {
		t.Error("marker set by a refused pass")
	}

	// With the signer passed, the row is rewritten and its new envelope
	// verifies over the new hash.
	d := deps(st, nil)
	d.Signer = signer
	if err := rehashStoredHashes(context.Background(), d); err != nil {
		t.Fatalf("rehashStoredHashes with a signer: %v", err)
	}
	rec := readRow(t, st, signed)
	if rec.ContentHash != framedHashOf(signed) {
		t.Errorf("content_hash = %s, want %s", rec.ContentHash, framedHashOf(signed))
	}
	if err := signer.Verify(context.Background(), rec.ContentHash, rec.Signature); err != nil {
		t.Errorf("the re-minted envelope does not verify over the new hash: %v", err)
	}
	if !markerSet(t, st) {
		t.Error("marker not set")
	}
}

// Spec: §4.7.6, §13.4 — the envelope is the only evidence that the stored bytes
// are the bytes the registry signed, so a row whose bytes were altered under a
// kept envelope is left as stored rather than re-signed into an accepted load.
func TestRehashStoredHashes_LeavesATamperedRowAlone(t *testing.T) {
	signer := testSigner(t)
	st := store.NewMemory()
	ctx := context.Background()
	untampered := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", skill: []byte("# Alpha\n"), signWith: signer}
	seedRow(t, st, nil, untampered)

	// The tampered row: an actor with store write access altered the bytes,
	// recomputed the previous release's digest over them, and kept the
	// envelope the registry minted over the authored bytes.
	fm := defaultFrontmatter("gamma")
	authored := []byte("# Gamma\n")
	altered := []byte("# Gamma, altered\n")
	env, err := signer.Sign(ctx, "sha256:"+preFramingContentHash(fm, authored, nil))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	forged := store.ManifestRecord{
		TenantID: "acme", ArtifactID: "gamma", Version: "1.0.0",
		ContentHash: "sha256:" + preFramingContentHash(fm, altered, nil),
		Type:        "skill", Name: "gamma", Layer: "team",
		Frontmatter: fm, SkillRaw: altered, Signature: env,
		IngestedAt: time.Now().UTC(),
	}
	if err := st.PutManifest(ctx, forged); err != nil {
		t.Fatalf("put forged row: %v", err)
	}
	sink := &recordingSink{}
	d := deps(st, nil)
	d.Signer, d.Sink = signer, sink

	if err := rehashStoredHashes(ctx, d); err != nil {
		t.Fatalf("rehashStoredHashes: %v", err)
	}
	after := readRow(t, st, rowSeed{tenant: "acme", id: "gamma", version: "1.0.0"})
	if after.ContentHash != forged.ContentHash || after.Signature != forged.Signature {
		t.Errorf("tampered row was rewritten: hash %s, signature changed %v", after.ContentHash, after.Signature != forged.Signature)
	}
	if got := readRow(t, st, untampered).ContentHash; got != framedHashOf(untampered) {
		t.Errorf("untampered row beside it was not rewritten: %s", got)
	}
	for _, ev := range sink.all() {
		if ev.Target == "gamma" {
			t.Error("an event was appended for the tampered row")
		}
	}
	if !markerSet(t, st) {
		t.Error("a signature_unverified row held the marker back")
	}
}

// Spec: §4.7.6, §13.4 — an unsigned row is signed when the pass rewrites it,
// which is the event that gives every stored row an envelope, and a store whose
// rows already carry one is left alone with no signer configured.
func TestRehashStoredHashes_SignsTheRowsItRewrites(t *testing.T) {
	signer := testSigner(t)
	st := store.NewMemory()
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"}
	seedRow(t, st, nil, s)
	d := deps(st, nil)
	d.Signer = signer
	if err := rehashStoredHashes(context.Background(), d); err != nil {
		t.Fatalf("rehashStoredHashes: %v", err)
	}
	rec := readRow(t, st, s)
	if rec.Signature == "" {
		t.Fatal("a rewritten row carries no envelope")
	}
	if err := signer.Verify(context.Background(), rec.ContentHash, rec.Signature); err != nil {
		t.Errorf("envelope does not verify over the new hash: %v", err)
	}

	// Rows already at the new hash, each carrying an envelope, need no
	// rewrite, so no signer is needed to pass.
	done := store.NewMemory()
	already := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true, signWith: signer}
	seedRow(t, done, nil, already)
	before := readRow(t, done, already)
	if err := rehashStoredHashes(context.Background(), deps(done, nil)); err != nil {
		t.Fatalf("rehashStoredHashes over migrated rows: %v", err)
	}
	after := readRow(t, done, already)
	if after.ContentHash != before.ContentHash || after.Signature != before.Signature {
		t.Error("a migrated row was rewritten")
	}
	if !markerSet(t, done) {
		t.Error("marker not set over an already-migrated store")
	}
}

// --- case 4: already migrated and unreproducible --------------------------

// Spec: §4.7.6, §13.4 — a row whose stored bytes reproduce neither digest is
// left as stored, because the bytes the registry holds do not produce the hash
// it serves, and it does not hold the completion record back.
func TestRehashStoredHashes_LeavesAnUnreproducibleRowAlone(t *testing.T) {
	st := store.NewMemory()
	bad := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", hashOverride: "sha256:" + strings.Repeat("ab", 32)}
	good := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", framed: true}
	seedRow(t, st, nil, bad)
	seedRow(t, st, nil, good)

	if err := rehashStoredHashes(context.Background(), deps(st, nil)); err != nil {
		t.Fatalf("rehashStoredHashes: %v", err)
	}
	if got := readRow(t, st, bad).ContentHash; got != bad.hashOverride {
		t.Errorf("unreproducible row was rewritten to %s", got)
	}
	if got := readRow(t, st, good).ContentHash; got != framedHashOf(good) {
		t.Errorf("migrated row moved to %s", got)
	}
	if !markerSet(t, st) {
		t.Error("an unreproducible row held the marker back")
	}
}

// --- case 5: the repair predicate -----------------------------------------

// Spec: §4.7.6, §13.4 — a row at the new hash whose envelope another key minted
// is left as stored, because the pass cannot tell a rotated key from a forged
// envelope; a row at the new hash carrying no envelope is signed when a signer
// is configured and left alone when none is.
func TestRehashStoredHashes_RepairsOnlyWhatItCanVerify(t *testing.T) {
	signer := testSigner(t)
	other := testSigner(t)
	st := store.NewMemory()
	foreign := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true, signWith: other}
	bare := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", framed: true}
	seedRow(t, st, nil, foreign)
	seedRow(t, st, nil, bare)
	before := readRow(t, st, foreign)

	// With no signer, nothing is rewritten and nothing is refused.
	if err := rehashStoredHashes(context.Background(), deps(st, nil)); err != nil {
		t.Fatalf("rehashStoredHashes with no signer: %v", err)
	}
	if got := readRow(t, st, bare); got.Signature != "" {
		t.Error("a bare row gained an envelope with no signer configured")
	}

	// The marker is set by that pass, so clear it before the signed run.
	if err := st.SetDataMigrationApplied(context.Background(), store.DataMigrationContentHashFraming, false); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	d := deps(st, nil)
	d.Signer = signer
	if err := rehashStoredHashes(context.Background(), d); err != nil {
		t.Fatalf("rehashStoredHashes with a signer: %v", err)
	}
	if after := readRow(t, st, foreign); after.Signature != before.Signature || after.ContentHash != before.ContentHash {
		t.Error("a row signed under another key was rewritten")
	}
	rec := readRow(t, st, bare)
	if err := signer.Verify(context.Background(), rec.ContentHash, rec.Signature); err != nil {
		t.Errorf("the bare row's new envelope does not verify: %v", err)
	}
}

// --- case 6: the retry arms and the terminal ones -------------------------

// Spec: §4.7.6, §13.4 — a row whose bytes could not be read, whose signing
// failed, or whose write failed holds the completion record back, and the next
// start tries it again.
func TestRehashStoredHashes_RetryArmsHoldTheMarkerBack(t *testing.T) {
	external := func() rowSeed {
		return rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", resource: &seedResource{path: "big.md", body: []byte("BIG"), external: true}}
	}

	t.Run("no object store", func(t *testing.T) {
		st := store.NewMemory()
		objs := objectstore.NewMemory()
		s := external()
		before := seedRow(t, st, objs, s)
		if err := rehashStoredHashes(context.Background(), deps(st, nil)); err != nil {
			t.Fatalf("rehashStoredHashes: %v", err)
		}
		if got := readRow(t, st, s).ContentHash; got != before.ContentHash {
			t.Errorf("row was rewritten without its body: %s", got)
		}
		if markerSet(t, st) {
			t.Error("marker set with a body_unavailable row")
		}
		// The repair: the next start with the object store passed
		// migrates the row and sets the marker.
		if err := rehashStoredHashes(context.Background(), deps(st, objs)); err != nil {
			t.Fatalf("second pass: %v", err)
		}
		if got, want := readRow(t, st, s).ContentHash, framedHashOf(s); got != want {
			t.Errorf("content_hash = %s, want %s", got, want)
		}
		if !markerSet(t, st) {
			t.Error("marker not set after the repair")
		}
	})

	t.Run("object store error", func(t *testing.T) {
		st := store.NewMemory()
		objs := objectstore.NewMemory()
		s := external()
		before := seedRow(t, st, objs, s)
		failing := &failingObjects{Provider: objs, err: errors.New("AccessDenied")}
		if err := rehashStoredHashes(context.Background(), deps(st, failing)); err != nil {
			t.Fatalf("rehashStoredHashes: %v", err)
		}
		if got := readRow(t, st, s).ContentHash; got != before.ContentHash {
			t.Errorf("row was rewritten: %s", got)
		}
		if markerSet(t, st) {
			t.Error("marker set with a body_unavailable row")
		}
	})

	t.Run("signer error", func(t *testing.T) {
		st := store.NewMemory()
		a := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"}
		b := rowSeed{tenant: "acme", id: "beta", version: "1.0.0"}
		seedRow(t, st, nil, a)
		seedRow(t, st, nil, b)
		d := deps(st, nil)
		d.Signer = failingSigner{inner: testSigner(t), failFor: framedHashOf(a)}
		if err := rehashStoredHashes(context.Background(), d); err != nil {
			t.Fatalf("rehashStoredHashes: %v", err)
		}
		if got := readRow(t, st, a).ContentHash; got == framedHashOf(a) {
			t.Error("a row whose signing failed was rewritten")
		}
		if got := readRow(t, st, b).ContentHash; got != framedHashOf(b) {
			t.Errorf("the other row was not rewritten: %s", got)
		}
		if markerSet(t, st) {
			t.Error("marker set after a signing failure")
		}
	})

	t.Run("write error", func(t *testing.T) {
		st := store.NewMemory()
		a := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"}
		seedRow(t, st, nil, a)
		wrapper := &countingStore{Store: st, rehashErr: errors.New("disk full")}
		if err := rehashStoredHashes(context.Background(), deps(wrapper, nil)); err != nil {
			t.Fatalf("rehashStoredHashes: %v", err)
		}
		if got := readRow(t, st, a).ContentHash; got == framedHashOf(a) {
			t.Error("the row was rewritten despite the write error")
		}
		if markerSet(t, st) {
			t.Error("marker set after a write error")
		}
	})

	t.Run("marker write error", func(t *testing.T) {
		st := store.NewMemory()
		a := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"}
		seedRow(t, st, nil, a)
		wrapper := &countingStore{Store: st, setMarkErr: errors.New("disk full")}
		if err := rehashStoredHashes(context.Background(), deps(wrapper, nil)); err != nil {
			t.Fatalf("rehashStoredHashes: %v", err)
		}
		if got, want := readRow(t, st, a).ContentHash, framedHashOf(a); got != want {
			t.Errorf("the rewritten row did not stand: %s", got)
		}
		if markerSet(t, st) {
			t.Error("marker set after its write failed")
		}
	})
}

// failingSigner fails Sign for one hash and delegates everything else.
type failingSigner struct {
	inner   sign.RegistryManagedKey
	failFor string
}

func (f failingSigner) ID() string { return f.inner.ID() }

func (f failingSigner) Sign(ctx context.Context, contentHash string) (string, error) {
	if contentHash == f.failFor {
		return "", errors.New("key unavailable")
	}
	return f.inner.Sign(ctx, contentHash)
}

func (f failingSigner) Verify(ctx context.Context, contentHash, signature string) error {
	return f.inner.Verify(ctx, contentHash, signature)
}

// Spec: §4.7.6, §13.4 — an object the store reports as absent is terminal while
// another read in the same pass returned a body, so the row is logged and the
// completion record is set; it does not hold every later start to a full
// re-read.
func TestRehashStoredHashes_BodyMissingIsTerminal(t *testing.T) {
	st := store.NewMemory()
	objs := objectstore.NewMemory()
	gone := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", resource: &seedResource{path: "gone.md", body: []byte("GONE"), external: true, withhold: true}}
	kept := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", resource: &seedResource{path: "kept.md", body: []byte("KEPT"), external: true}}
	before := seedRow(t, st, objs, gone)
	seedRow(t, st, objs, kept)

	if err := rehashStoredHashes(context.Background(), deps(st, objs)); err != nil {
		t.Fatalf("rehashStoredHashes: %v", err)
	}
	if got := readRow(t, st, gone).ContentHash; got != before.ContentHash {
		t.Errorf("a row whose object is gone was rewritten: %s", got)
	}
	if got, want := readRow(t, st, kept).ContentHash, framedHashOf(kept); got != want {
		t.Errorf("the readable row was not rewritten: %s", got)
	}
	if !markerSet(t, st) {
		t.Error("a body_missing row held the marker back beside a readable one")
	}
}

// Spec: §4.7.6, §13.4 — an object store opened at the wrong root answers every
// read with not-found, which no single row can tell from one lost object, so a
// pass in which no read returned a body holds the completion record back.
func TestRehashStoredHashes_WrongRootHoldsTheMarkerBack(t *testing.T) {
	st := store.NewMemory()
	real := objectstore.NewMemory()
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", resource: &seedResource{path: "big.md", body: []byte("BIG"), external: true}}
	before := seedRow(t, st, real, s)

	empty := objectstore.NewMemory()
	if err := rehashStoredHashes(context.Background(), deps(st, empty)); err != nil {
		t.Fatalf("rehashStoredHashes: %v", err)
	}
	if got := readRow(t, st, s).ContentHash; got != before.ContentHash {
		t.Errorf("row was rewritten from an empty object store: %s", got)
	}
	if markerSet(t, st) {
		t.Error("marker set when no object-store read returned a body")
	}

	if err := rehashStoredHashes(context.Background(), deps(st, real)); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got, want := readRow(t, st, s).ContentHash, framedHashOf(s); got != want {
		t.Errorf("content_hash = %s, want %s", got, want)
	}
	if !markerSet(t, st) {
		t.Error("marker not set once the objects were reachable")
	}
}

// Spec: §4.7.6, §13.4 — an object store that stops answering costs the plan one
// PODIUM_MIGRATION_OBJECT_READ_TIMEOUT rather than one per row, whether or not
// the provider honors the deadline: Filesystem.Get discards its context and
// calls os.ReadFile, and filesystem is the default object store.
func TestRehashStoredHashes_StopsReadingAfterOneExpiredDeadline(t *testing.T) {
	for _, honors := range []bool{true, false} {
		name := "provider honors the context"
		if !honors {
			name = "provider ignores the context"
		}
		t.Run(name, func(t *testing.T) {
			st := store.NewMemory()
			backing := objectstore.NewMemory()
			var external []rowSeed
			for i := range 3 {
				s := rowSeed{
					tenant: "acme", id: fmt.Sprintf("ext%d", i), version: "1.0.0",
					resource: &seedResource{path: "big.md", body: []byte(fmt.Sprintf("BIG%d", i)), external: true},
				}
				seedRow(t, st, backing, s)
				external = append(external, s)
			}
			inline := rowSeed{tenant: "acme", id: "inline", version: "1.0.0", resource: &seedResource{path: "small.md", body: []byte("S")}}
			seedRow(t, st, backing, inline)

			blocking := &blockingObjects{Provider: backing, honorContext: honors, release: make(chan struct{})}
			if !honors {
				t.Cleanup(func() { close(blocking.release) })
			}
			d := deps(st, blocking)
			d.ReadTimeout = 50 * time.Millisecond

			start := time.Now()
			if err := rehashStoredHashes(context.Background(), d); err != nil {
				t.Fatalf("rehashStoredHashes: %v", err)
			}
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("the pass took %v, want a small multiple of the 50ms deadline", elapsed)
			}
			if got := blocking.count(); got != 1 {
				t.Errorf("Get called %d times, want exactly 1 after the deadline expired", got)
			}
			for _, s := range external {
				if got := readRow(t, st, s).ContentHash; got == framedHashOf(s) {
					t.Errorf("%s was rewritten without its body", s.id)
				}
			}
			if got, want := readRow(t, st, inline).ContentHash, framedHashOf(inline); got != want {
				t.Errorf("the inline row was not rewritten: %s", got)
			}
			if markerSet(t, st) {
				t.Error("marker set with body_unavailable rows")
			}
		})
	}
}

// Spec: §4.7.6, §13.4 — a read that fails promptly costs only its own row, so
// the pass reads every later row's objects, and deleting an object that cannot
// be made readable turns the row into a body_missing row the pass can pass over.
func TestRehashStoredHashes_PromptErrorStopsNothingElse(t *testing.T) {
	st := store.NewMemory()
	backing := objectstore.NewMemory()
	first := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", resource: &seedResource{path: "a.md", body: []byte("A"), external: true}}
	second := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", resource: &seedResource{path: "b.md", body: []byte("B"), external: true}}
	before := seedRow(t, st, backing, first)
	seedRow(t, st, backing, second)

	failing := &failingObjects{Provider: backing, failBody: []byte("A"), err: errors.New("AccessDenied")}
	if err := rehashStoredHashes(context.Background(), deps(st, failing)); err != nil {
		t.Fatalf("rehashStoredHashes: %v", err)
	}
	if got := failing.count(); got != 2 {
		t.Errorf("Get called %d times, want one per externally held object", got)
	}
	if got := readRow(t, st, first).ContentHash; got != before.ContentHash {
		t.Errorf("the unreadable row was rewritten: %s", got)
	}
	if got, want := readRow(t, st, second).ContentHash, framedHashOf(second); got != want {
		t.Errorf("the later row was not rewritten: %s", got)
	}
	if markerSet(t, st) {
		t.Error("marker set with a body_unavailable row")
	}

	// The repair for an object that cannot be made readable: delete it, so
	// its Get reports ErrNotFound and the row classifies body_missing.
	key := strings.TrimPrefix(readRow(t, st, first).Resources[0].ContentHash, "sha256:")
	if err := backing.Delete(context.Background(), key); err != nil {
		t.Fatalf("delete object: %v", err)
	}
	if err := rehashStoredHashes(context.Background(), deps(st, backing)); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got := readRow(t, st, first).ContentHash; got != before.ContentHash {
		t.Errorf("the body_missing row was rewritten: %s", got)
	}
	if !markerSet(t, st) {
		t.Error("marker not set once the unreadable object was deleted")
	}
}

// --- case 7: the conflict arms --------------------------------------------

// Spec: §4.7.6, §13.4 — a row another writer holds is not a failed write: a
// peer replica's rewrite, a concurrent ingest, and a §8.4 purge each leave the
// row as that writer left it and none holds the completion record back.
func TestRehashStoredHashes_ConflictsAreNotFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "immutability", err: store.ErrImmutableViolation},
		{name: "purged", err: store.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.NewMemory()
			held := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"}
			other := rowSeed{tenant: "acme", id: "beta", version: "1.0.0"}
			before := seedRow(t, st, nil, held)
			seedRow(t, st, nil, other)
			wrapper := &countingStore{Store: st, rehashErr: tc.err, rehashFor: "alpha"}
			if err := rehashStoredHashes(context.Background(), deps(wrapper, nil)); err != nil {
				t.Fatalf("rehashStoredHashes: %v", err)
			}
			if got := readRow(t, st, held).ContentHash; got != before.ContentHash {
				t.Errorf("the held row was overwritten: %s", got)
			}
			if got, want := readRow(t, st, other).ContentHash, framedHashOf(other); got != want {
				t.Errorf("the remaining row was not rewritten: %s", got)
			}
			if !markerSet(t, st) {
				t.Error("a conflict held the marker back")
			}
		})
	}

	t.Run("backend compare-and-swap", func(t *testing.T) {
		st := store.NewMemory()
		moved := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"}
		other := rowSeed{tenant: "acme", id: "beta", version: "1.0.0"}
		seedRow(t, st, nil, moved)
		seedRow(t, st, nil, other)
		wrapper := &countingStore{Store: st}
		// A peer replica rewrites the row between the plan and the
		// apply, so the backend's own compare-and-swap refuses it.
		wrapper.beforeApply = func(*countingStore) {
			rec := readRow(t, st, moved)
			if err := st.RehashManifest(context.Background(), rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash, framedHashOf(moved), "peer-envelope"); err != nil {
				t.Errorf("peer rewrite: %v", err)
			}
		}
		if err := rehashStoredHashes(context.Background(), deps(wrapper, nil)); err != nil {
			t.Fatalf("rehashStoredHashes: %v", err)
		}
		if got := readRow(t, st, moved).Signature; got != "peer-envelope" {
			t.Errorf("the peer's write was overwritten: signature %q", got)
		}
		if !markerSet(t, st) {
			t.Error("a compare-and-swap refusal held the marker back")
		}
	})
}

// --- case 8: the fatal arms -----------------------------------------------

// Spec: §4.7.6, §13.4 — a store that cannot be read fails the start, with no
// row written and the completion record unset.
func TestRehashStoredHashes_StoreErrorsFailTheStart(t *testing.T) {
	boom := errors.New("connection refused")
	cases := []struct {
		name string
		with func(*countingStore)
	}{
		{name: "marker", with: func(c *countingStore) { c.markerErr = boom }},
		{name: "tenants", with: func(c *countingStore) { c.tenantsErr = boom }},
		{name: "rows", with: func(c *countingStore) { c.rowsErr = boom }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.NewMemory()
			seedRow(t, st, nil, rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"})
			wrapper := &countingStore{Store: st}
			tc.with(wrapper)
			err := rehashStoredHashes(context.Background(), deps(wrapper, nil))
			if !errors.Is(err, boom) {
				t.Fatalf("rehashStoredHashes = %v, want the store error", err)
			}
			if wrapper.rehashes != 0 {
				t.Errorf("%d rows written after a store error, want 0", wrapper.rehashes)
			}
			if markerSet(t, st) {
				t.Error("marker set after a store error")
			}
		})
	}
}

// --- case 9: the audit events ---------------------------------------------

// Spec: §4.7.6, §8.1, §8.2, §13.4 — the pass appends one artifact.signed event
// per row it re-signs, carrying the system caller and the §8.2 redaction ingest
// applies to that event, and it appends nothing for a row it did not sign or
// did not write.
func TestRehashStoredHashes_AppendsOneSignedEventPerRewrittenRow(t *testing.T) {
	signer := testSigner(t)
	st := store.NewMemory()
	plain := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"}
	signed := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", signWith: signer}
	// A row whose manifest declares audit_redact, seeded with AuditRedact
	// nil, which is what a row read back from the SQL backends carries.
	redactFM := []byte("---\nid: gamma\ntype: skill\nname: gamma\ndescription: a test artifact\naccount: acct-1234\naudit_redact: [version, account]\n---\n# gamma\n")
	redacting := rowSeed{tenant: "acme", id: "gamma", version: "1.0.0", fm: redactFM}
	seedRow(t, st, nil, plain)
	seedRow(t, st, nil, signed)
	redactRec := seedRow(t, st, nil, redacting)

	sink := &recordingSink{}
	d := deps(st, nil)
	d.Signer, d.Sink = signer, sink
	if err := rehashStoredHashes(context.Background(), d); err != nil {
		t.Fatalf("rehashStoredHashes: %v", err)
	}

	events := sink.all()
	if len(events) != 3 {
		t.Fatalf("recorded %d events, want one per rewritten row", len(events))
	}
	byTarget := map[string]audit.Event{}
	for _, ev := range events {
		if ev.Type != audit.EventArtifactSigned {
			t.Errorf("event type = %s, want artifact.signed", ev.Type)
		}
		if ev.Caller != "system" {
			t.Errorf("event caller = %q, want system", ev.Caller)
		}
		byTarget[ev.Target] = ev
	}
	for _, s := range []rowSeed{plain, signed} {
		ev := byTarget[s.id]
		if ev.Context["version"] != s.version || ev.Context["content_hash"] != framedHashOf(s) {
			t.Errorf("%s context = %v, want the version and the new content hash", s.id, ev.Context)
		}
	}
	redactRec.ContentHash = framedHashOf(redacting)
	want := ingest.ArtifactEventRedactor(redactRec)(map[string]string{
		"version":      redacting.version,
		"content_hash": framedHashOf(redacting),
	})
	got := byTarget[redacting.id].Context
	if len(got) != len(want) {
		t.Fatalf("redacted context = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("redacted context[%s] = %q, want %q", k, got[k], v)
		}
	}
}

// Spec: §4.7.6, §8.1, §13.4 — no signer means no event, a refused write means
// no event for that row, and a nil sink still rewrites every row.
func TestRehashStoredHashes_AppendsNothingWithoutASignedWrite(t *testing.T) {
	st := store.NewMemory()
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"}
	seedRow(t, st, nil, s)
	sink := &recordingSink{}
	d := deps(st, nil)
	d.Sink = sink
	if err := rehashStoredHashes(context.Background(), d); err != nil {
		t.Fatalf("rehashStoredHashes with no signer: %v", err)
	}
	if n := len(sink.all()); n != 0 {
		t.Errorf("recorded %d events with no signer, want 0", n)
	}

	refused := store.NewMemory()
	seedRow(t, refused, nil, s)
	wrapper := &countingStore{Store: refused, rehashErr: store.ErrImmutableViolation}
	refusedSink := &recordingSink{}
	rd := deps(wrapper, nil)
	rd.Signer, rd.Sink = testSigner(t), refusedSink
	if err := rehashStoredHashes(context.Background(), rd); err != nil {
		t.Fatalf("rehashStoredHashes over a refused write: %v", err)
	}
	if n := len(refusedSink.all()); n != 0 {
		t.Errorf("recorded %d events for a refused write, want 0", n)
	}

	nilSink := store.NewMemory()
	seedRow(t, nilSink, nil, s)
	nd := deps(nilSink, nil)
	nd.Signer = testSigner(t)
	if err := rehashStoredHashes(context.Background(), nd); err != nil {
		t.Fatalf("rehashStoredHashes with a nil sink: %v", err)
	}
	if got, want := readRow(t, nilSink, s).ContentHash, framedHashOf(s); got != want {
		t.Errorf("content_hash = %s, want %s", got, want)
	}
}

// Spec: §4.7.6, §8.1, §13.4 — a sink that refuses an event is called once and
// no more in that run, because an http(s) destination is a synchronous POST
// with a ten-second timeout and paying it per row would stall the start; every
// row is still rewritten and re-signed.
func TestRehashStoredHashes_StopsAppendingAfterASinkFailure(t *testing.T) {
	signer := testSigner(t)
	st := store.NewMemory()
	var seeds []rowSeed
	for i := range 4 {
		s := rowSeed{tenant: "acme", id: fmt.Sprintf("art%d", i), version: "1.0.0", signWith: signer}
		seedRow(t, st, nil, s)
		seeds = append(seeds, s)
	}
	sink := &recordingSink{err: errors.New("sink unreachable")}
	d := deps(st, nil)
	d.Signer, d.Sink = signer, sink
	if err := rehashStoredHashes(context.Background(), d); err != nil {
		t.Fatalf("rehashStoredHashes: %v", err)
	}
	if n := len(sink.all()); n != 1 {
		t.Errorf("sink called %d times, want exactly 1", n)
	}
	for _, s := range seeds {
		rec := readRow(t, st, s)
		if rec.ContentHash != framedHashOf(s) {
			t.Errorf("%s was not rewritten: %s", s.id, rec.ContentHash)
		}
		if err := signer.Verify(context.Background(), rec.ContentHash, rec.Signature); err != nil {
			t.Errorf("%s envelope does not verify: %v", s.id, err)
		}
	}
	if !markerSet(t, st) {
		t.Error("a sink failure held the marker back")
	}
}

// --- case 10: the generated-key refusal -----------------------------------

// Spec: §4.7.9, §13.4 — a start configured to sign whose key file is absent is
// refused while the rewrite has not completed and any stored row, including a
// soft-deleted one in any tenant, carries a signature, so a refused start
// generates and writes no key.
func TestRefuseGeneratedSigningKey_RefusesBeforeTheLoaderRuns(t *testing.T) {
	ctx := context.Background()
	signer := testSigner(t)
	newStore := func(t *testing.T) *store.Memory {
		st := store.NewMemory()
		seedRow(t, st, nil, rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"})
		seedRow(t, st, nil, rowSeed{tenant: "globex", id: "beta", version: "1.0.0", layer: "archived", deleted: true, signWith: signer})
		return st
	}

	t.Run("absent key path", func(t *testing.T) {
		path := t.TempDir() + "/registry-signing.key"
		t.Setenv("PODIUM_SIGN_KEY_PATH", path)
		st := newStore(t)
		before := readRow(t, st, rowSeed{tenant: "globex", id: "beta", version: "1.0.0"})
		err := refuseGeneratedSigningKey(ctx, st, "registry-key")
		if err == nil || !strings.Contains(err.Error(), "PODIUM_SIGN_KEY_PATH") || !strings.Contains(err.Error(), "globex/beta@1.0.0") {
			t.Fatalf("refuseGeneratedSigningKey = %v, want a refusal naming PODIUM_SIGN_KEY_PATH and the signed row", err)
		}
		if _, statErr := readBytes(path); statErr == nil {
			t.Error("a key file exists after a refused start")
		}
		after := readRow(t, st, rowSeed{tenant: "globex", id: "beta", version: "1.0.0"})
		if after.ContentHash != before.ContentHash || after.Signature != before.Signature {
			t.Error("the refusal wrote a row")
		}
		if markerSet(t, st) {
			t.Error("the refusal set the marker")
		}
	})

	t.Run("symlink to an absent target", func(t *testing.T) {
		dir := t.TempDir()
		link := dir + "/registry-signing.key"
		if err := os.Symlink(dir+"/gone.key", link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		t.Setenv("PODIUM_SIGN_KEY_PATH", link)
		if err := refuseGeneratedSigningKey(ctx, newStore(t), "registry-key"); err == nil {
			t.Error("a symbolic link to an absent target was not treated as absent")
		}
	})

	t.Run("signing off", func(t *testing.T) {
		t.Setenv("PODIUM_SIGN_KEY_PATH", t.TempDir()+"/registry-signing.key")
		if err := refuseGeneratedSigningKey(ctx, newStore(t), ""); err != nil {
			t.Errorf("refuseGeneratedSigningKey with signing off = %v, want nil", err)
		}
	})

	t.Run("key file present", func(t *testing.T) {
		path := t.TempDir() + "/registry-signing.key"
		if err := os.WriteFile(path, []byte("key"), 0o600); err != nil {
			t.Fatalf("write key: %v", err)
		}
		t.Setenv("PODIUM_SIGN_KEY_PATH", path)
		if err := refuseGeneratedSigningKey(ctx, newStore(t), "registry-key"); err != nil {
			t.Errorf("refuseGeneratedSigningKey with a key file = %v, want nil", err)
		}
	})

	t.Run("no signed row", func(t *testing.T) {
		t.Setenv("PODIUM_SIGN_KEY_PATH", t.TempDir()+"/registry-signing.key")
		st := store.NewMemory()
		seedRow(t, st, nil, rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"})
		if err := refuseGeneratedSigningKey(ctx, st, "registry-key"); err != nil {
			t.Errorf("refuseGeneratedSigningKey with no signed row = %v, want nil", err)
		}
	})

	t.Run("marker set is read before any row", func(t *testing.T) {
		t.Setenv("PODIUM_SIGN_KEY_PATH", t.TempDir()+"/registry-signing.key")
		st := newStore(t)
		if err := st.SetDataMigrationApplied(ctx, store.DataMigrationContentHashFraming, true); err != nil {
			t.Fatalf("set marker: %v", err)
		}
		wrapper := &countingStore{Store: st, tenantsErr: errors.New("must not be reached")}
		if err := refuseGeneratedSigningKey(ctx, wrapper, "registry-key"); err != nil {
			t.Errorf("refuseGeneratedSigningKey with the marker set = %v, want nil", err)
		}
	})

	t.Run("store errors are wrapped", func(t *testing.T) {
		t.Setenv("PODIUM_SIGN_KEY_PATH", t.TempDir()+"/registry-signing.key")
		boom := errors.New("connection refused")
		for _, with := range []func(*countingStore){
			func(c *countingStore) { c.markerErr = boom },
			func(c *countingStore) { c.rowsErr = boom },
		} {
			wrapper := &countingStore{Store: newStore(t)}
			with(wrapper)
			err := refuseGeneratedSigningKey(ctx, wrapper, "registry-key")
			if !errors.Is(err, boom) || !strings.Contains(err.Error(), "registry signing key:") {
				t.Errorf("refuseGeneratedSigningKey = %v, want the store error wrapped as registry signing key:", err)
			}
		}
	})
}

// Spec: §4.7.9, §13.4 — the different-key case the generated-key refusal does
// not cover: a key file that exists and holds another key leaves every row it
// signed at its stored hash while the rows beside it are rewritten.
func TestRehashStoredHashes_LeavesRowsSignedUnderAnotherKey(t *testing.T) {
	signer := testSigner(t)
	lost := testSigner(t)
	st := store.NewMemory()
	stranded := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", signWith: lost}
	plain := rowSeed{tenant: "acme", id: "beta", version: "1.0.0"}
	before := seedRow(t, st, nil, stranded)
	seedRow(t, st, nil, plain)

	d := deps(st, nil)
	d.Signer = signer
	if err := rehashStoredHashes(context.Background(), d); err != nil {
		t.Fatalf("rehashStoredHashes: %v", err)
	}
	if got := readRow(t, st, stranded).ContentHash; got != before.ContentHash {
		t.Errorf("a row signed under another key was rewritten: %s", got)
	}
	if got, want := readRow(t, st, plain).ContentHash, framedHashOf(plain); got != want {
		t.Errorf("the row beside it was not rewritten: %s", got)
	}
	if !markerSet(t, st) {
		t.Error("a signature_unverified row held the marker back")
	}
}

// --- case 11: concurrent passes -------------------------------------------

// Spec: §4.7.6, §8.1, §13.4 — replicas that start together each find the
// completion record unset and each run the pass, and the store's
// compare-and-swap makes their writes safe for every class, including the row
// that needs only its first envelope. Run this case under -race.
func TestRehashStoredHashes_ConcurrentPassesSignEachRowOnce(t *testing.T) {
	signer := testSigner(t)
	st := store.NewMemory()
	var seeds []rowSeed
	for i := range 3 {
		s := rowSeed{tenant: "acme", id: fmt.Sprintf("pre%d", i), version: "1.0.0"}
		seedRow(t, st, nil, s)
		seeds = append(seeds, s)
	}
	signedSeed := rowSeed{tenant: "acme", id: "signed", version: "1.0.0", signWith: signer}
	bare := rowSeed{tenant: "acme", id: "bare", version: "1.0.0", framed: true}
	seedRow(t, st, nil, signedSeed)
	seedRow(t, st, nil, bare)
	seeds = append(seeds, signedSeed, bare)

	sink := &recordingSink{}
	d := deps(st, nil)
	d.Signer, d.Sink = signer, sink

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = rehashStoredHashes(context.Background(), d)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("rehashStoredHashes: %v", err)
		}
	}

	counts := map[string]int{}
	for _, ev := range sink.all() {
		counts[ev.Target]++
	}
	for _, s := range seeds {
		rec := readRow(t, st, s)
		if rec.ContentHash != framedHashOf(s) {
			t.Errorf("%s content_hash = %s, want %s", s.id, rec.ContentHash, framedHashOf(s))
		}
		if err := signer.Verify(context.Background(), rec.ContentHash, rec.Signature); err != nil {
			t.Errorf("%s envelope does not verify: %v", s.id, err)
		}
		if counts[s.id] != 1 {
			t.Errorf("%s recorded %d artifact.signed events, want 1", s.id, counts[s.id])
		}
	}
	if !markerSet(t, st) {
		t.Error("marker not set after two concurrent passes")
	}
}

// Spec: §13.4 — the log lines name the object store the pass read from and the
// class of each row it left untouched, so an operator can find the object and
// the row from the summary alone.
func TestRehashLogHelpers_NameTheStoreAndTheClass(t *testing.T) {
	root := t.TempDir()
	fs, err := objectstore.Open(root)
	if err != nil {
		t.Fatalf("new filesystem object store: %v", err)
	}
	cases := []struct {
		provider objectstore.Provider
		want     string
	}{
		{provider: nil, want: "(no object store)"},
		{provider: fs, want: "root " + root},
		{provider: &objectstore.S3{Bucket: "acme-objects"}, want: "bucket acme-objects"},
		{provider: objectstore.NewMemory(), want: "memory"},
	}
	for _, tc := range cases {
		if got := objectStoreLocation(tc.provider); got != tc.want {
			t.Errorf("objectStoreLocation = %q, want %q", got, tc.want)
		}
	}

	classes := map[rehashClass]string{
		classMigrated:            "migrated",
		classUnmigrated:          "unmigrated",
		classUnreproducible:      "unreproducible",
		classSignatureUnverified: "signature_unverified",
		classBodyMissing:         "body_missing",
		classBodyUnavailable:     "body_unavailable",
		rehashClass(99):          "unknown",
	}
	for class, want := range classes {
		if got := class.String(); got != want {
			t.Errorf("rehashClass(%d).String() = %q, want %q", class, got, want)
		}
	}
}

// Spec: §13.10, §4.7.9 — an unset PODIUM_SIGN_KEY_PATH resolves to the
// standalone default, which is the path the loader writes a generated key to
// and the path the generated-key refusal stats.
func TestRegistrySigningKeyPath_DefaultsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	got, err := registrySigningKeyPath("")
	if err != nil {
		t.Fatalf("registrySigningKeyPath: %v", err)
	}
	want := filepath.Join(home, ".podium", "standalone", "registry-signing.key")
	if got != want {
		t.Errorf("registrySigningKeyPath(\"\") = %q, want %q", got, want)
	}
	if got, err := registrySigningKeyPath("/custom/key"); err != nil || got != "/custom/key" {
		t.Errorf("registrySigningKeyPath(\"/custom/key\") = %q, %v, want the value unchanged", got, err)
	}
}
