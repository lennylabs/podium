package serverboot

import (
	"bytes"
	"context"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// bootFixture is one standalone boot the rehash tests drive: an isolated home,
// a sqlite store, a one-artifact layer path, and a free bind address.
type bootFixture struct {
	home       string
	sqlitePath string
	layerPath  string
	keyPath    string
	auditPath  string
	addr       string
}

func newBootFixture(t *testing.T) *bootFixture {
	t.Helper()
	home := t.TempDir()
	layerPath := filepath.Join(home, "registry")
	testharness.WriteTree(t, layerPath, testharness.WriteTreeOption{
		Path:    "alpha/ARTIFACT.md",
		Content: artifactBody,
	})
	f := &bootFixture{
		home:       home,
		sqlitePath: filepath.Join(home, "podium.db"),
		layerPath:  layerPath,
		keyPath:    filepath.Join(home, "registry-signing.key"),
		auditPath:  filepath.Join(home, "audit.log"),
		addr:       freeAddr(t),
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PODIUM_CONFIG_FILE", "")
	t.Setenv("PODIUM_POSTGRES_DSN", "")
	t.Setenv("PODIUM_S3_ENDPOINT", "")
	t.Setenv("PODIUM_REGISTRY_STORE", "sqlite")
	t.Setenv("PODIUM_SQLITE_PATH", f.sqlitePath)
	t.Setenv("PODIUM_LAYER_PATH", layerPath)
	t.Setenv("PODIUM_AUDIT_LOG_PATH", f.auditPath)
	t.Setenv("PODIUM_BIND", f.addr)
	// The registry signs by default (§13.10), so a fixture that is not about
	// signing turns it off; a case that signs sets registry-key and its key.
	t.Setenv("PODIUM_SIGN", "none")
	t.Setenv("PODIUM_SIGN_KEY_PATH", "")
	return f
}

// syncBuffer collects log output from run's own goroutine and the background
// daemons it starts, which write concurrently.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// boot runs one full start and returns everything the standard logger emitted
// during it together with run's error. A start that is refused returns its
// error without waiting for a listener that never answers.
func (f *bootFixture) boot(t *testing.T) (string, error) {
	t.Helper()
	logs := &syncBuffer{}
	prev := log.Writer()
	log.SetOutput(logs)
	defer log.SetOutput(prev)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- run(ctx, func() {}) }()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errc:
			return logs.String(), err
		default:
		}
		// healthOK's own readiness rule: the listener is open long
		// before the boot finishes, so readiness is /healthz answering
		// rather than a dial succeeding.
		if healthOK(f.addr, 200*time.Millisecond) {
			cancel()
			select {
			case err := <-errc:
				return logs.String(), err
			case <-time.After(20 * time.Second):
				t.Fatal("run did not return after the context was cancelled")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run neither bound its listener nor returned")
	return "", nil
}

// openStoreDirect opens the fixture's sqlite store outside any server process,
// which is where the tests seed the previous release's state.
func (f *bootFixture) openStoreDirect(t *testing.T) *store.SQLite {
	t.Helper()
	st, err := store.OpenSQLite(f.sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// downgradeRows moves every stored row back to the digest the previous release
// computed over its bytes, through RehashManifest with the framed hash as the
// compare value, and clears the completion record. sign re-mints each signed
// row's envelope over the pre-framing hash, as the previous release's ingest
// did. It runs while no server process holds the store.
func downgradeRows(t *testing.T, st store.Store, keyPath string) []store.ManifestRecord {
	t.Helper()
	ctx := context.Background()
	var signer interface {
		Sign(context.Context, string) (string, error)
	}
	if keyPath != "" {
		p, err := loadOrGenerateRegistrySigner(keyPath)
		if err != nil {
			t.Fatalf("load signing key: %v", err)
		}
		signer = p
	}
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	var out []store.ManifestRecord
	for _, tenant := range tenants {
		recs, err := st.ListManifestsIncludingDeleted(ctx, tenant.ID)
		if err != nil {
			t.Fatalf("list manifests: %v", err)
		}
		for _, rec := range recs {
			resources := map[string][]byte{}
			for _, ref := range rec.Resources {
				resources[ref.Path] = ref.Inline
			}
			old := "sha256:" + preFramingContentHash(rec.Frontmatter, rec.SkillRaw, resources)
			sig := ""
			if rec.Signature != "" && signer != nil {
				if sig, err = signer.Sign(ctx, old); err != nil {
					t.Fatalf("re-sign: %v", err)
				}
			}
			if err := st.RehashManifest(ctx, rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash, old, sig); err != nil {
				t.Fatalf("downgrade %s: %v", rec.ArtifactID, err)
			}
			rec.ContentHash, rec.Signature = old, sig
			out = append(out, rec)
		}
	}
	if err := st.SetDataMigrationApplied(ctx, store.DataMigrationContentHashFraming, false); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("the first start stored no row to downgrade")
	}
	return out
}

func framedHashOfRecord(rec store.ManifestRecord) string {
	resources := map[string][]byte{}
	for _, ref := range rec.Resources {
		resources[ref.Path] = ref.Inline
	}
	return "sha256:" + version.CanonicalContentHash(rec.Frontmatter, rec.SkillRaw, resources)
}

// Spec: §4.7.6, §8.1, §13.4 — run rewrites every stored row on the first start
// of this version that binds, before the bootstrap ingest, and signs and audits
// each row it rewrites. The ingest's own line is the assertion that the pass
// precedes it: an ingest that met a row still at the previous digest would
// report a conflict for it rather than counting it idempotent.
func TestRun_RehashesStoredRowsBeforeTheBootstrapIngest(t *testing.T) {
	f := newBootFixture(t)
	t.Setenv("PODIUM_SIGN", "registry-key")
	t.Setenv("PODIUM_SIGN_KEY_PATH", f.keyPath)

	if _, err := f.boot(t); err != nil {
		t.Fatalf("first start: %v", err)
	}
	st := f.openStoreDirect(t)
	rows := downgradeRows(t, st, f.keyPath)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	logs, err := f.boot(t)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	after := f.openStoreDirect(t)
	ctx := context.Background()
	signer, serr := loadOrGenerateRegistrySigner(f.keyPath)
	if serr != nil {
		t.Fatalf("load signing key: %v", serr)
	}
	for _, seeded := range rows {
		rec, err := after.GetManifest(ctx, seeded.TenantID, seeded.ArtifactID, seeded.Version)
		if err != nil {
			t.Fatalf("get manifest: %v", err)
		}
		if got, want := rec.ContentHash, framedHashOfRecord(seeded); got != want {
			t.Errorf("%s content_hash = %s, want %s", rec.ArtifactID, got, want)
		}
		if err := signer.Verify(ctx, rec.ContentHash, rec.Signature); err != nil {
			t.Errorf("%s envelope does not verify under the configured key: %v", rec.ArtifactID, err)
		}
	}
	applied, err := after.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)
	if err != nil || !applied {
		t.Errorf("marker applied = %v (err %v), want true", applied, err)
	}
	auditLog, err := os.ReadFile(f.auditPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var systemSigned int
	for _, line := range strings.Split(string(auditLog), "\n") {
		if strings.Contains(line, "artifact.signed") && strings.Contains(line, `"identity":"system"`) {
			systemSigned++
		}
	}
	if systemSigned != len(rows) {
		t.Errorf("audit log holds %d system-caller artifact.signed lines, want %d", systemSigned, len(rows))
	}
	if !strings.Contains(logs, "idempotent="+strconv.Itoa(len(rows))) {
		t.Errorf("bootstrap ingest line does not count %d idempotent artifact(s); logs:\n%s", len(rows), logs)
	}
}

// Spec: §4.7.6, §13.4 — rewriting a signed row while no signer is configured
// would strand its envelope, so run refuses the start with the store as it was
// and the completion record unset.
func TestRun_RefusesToStrandAStoredSignature(t *testing.T) {
	f := newBootFixture(t)
	t.Setenv("PODIUM_SIGN", "registry-key")
	t.Setenv("PODIUM_SIGN_KEY_PATH", f.keyPath)
	if _, err := f.boot(t); err != nil {
		t.Fatalf("first start: %v", err)
	}
	st := f.openStoreDirect(t)
	rows := downgradeRows(t, st, f.keyPath)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	t.Setenv("PODIUM_SIGN", "none")
	_, err := f.boot(t)
	if err == nil || !strings.Contains(err.Error(), "PODIUM_SIGN=none") {
		t.Fatalf("run = %v, want a refusal naming PODIUM_SIGN=none", err)
	}
	after := f.openStoreDirect(t)
	ctx := context.Background()
	for _, seeded := range rows {
		rec, gerr := after.GetManifest(ctx, seeded.TenantID, seeded.ArtifactID, seeded.Version)
		if gerr != nil {
			t.Fatalf("get manifest: %v", gerr)
		}
		if rec.ContentHash != seeded.ContentHash || rec.Signature != seeded.Signature {
			t.Errorf("%s was written by a refused start", rec.ArtifactID)
		}
	}
	applied, aerr := after.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)
	if aerr != nil || applied {
		t.Errorf("marker applied = %v (err %v), want false", applied, aerr)
	}
}

// Spec: §4.7.6, §13.4 — a start whose listener could not bind does not run the
// pass. Rewriting and re-signing the store another process is still serving,
// only to exit on the bind error, is the one failure the guard prevents.
func TestRun_BindFailureDoesNotRewriteTheStore(t *testing.T) {
	f := newBootFixture(t)
	if _, err := f.boot(t); err != nil {
		t.Fatalf("first start: %v", err)
	}
	st := f.openStoreDirect(t)
	rows := downgradeRows(t, st, "")
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	held, err := net.Listen("tcp", f.addr)
	if err != nil {
		t.Fatalf("hold the bind address: %v", err)
	}
	defer func() { _ = held.Close() }()

	_, runErr := f.boot(t)
	if runErr == nil {
		t.Fatal("run = nil, want the bind error")
	}
	after := f.openStoreDirect(t)
	ctx := context.Background()
	for _, seeded := range rows {
		rec, gerr := after.GetManifest(ctx, seeded.TenantID, seeded.ArtifactID, seeded.Version)
		if gerr != nil {
			t.Fatalf("get manifest: %v", gerr)
		}
		if rec.ContentHash != seeded.ContentHash {
			t.Errorf("%s was rewritten by a start that never bound", rec.ArtifactID)
		}
	}
	applied, aerr := after.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)
	if aerr != nil || applied {
		t.Errorf("marker applied = %v (err %v), want false", applied, aerr)
	}
}

// Spec: §4.7.9, §13.4 — the generated-key refusal precedes both the signing-key
// loader and the bind error, and a refused start leaves no key file that would
// disarm it on the next start.
func TestRun_BindFailureStillRefusesAGeneratedKey(t *testing.T) {
	f := newBootFixture(t)
	t.Setenv("PODIUM_SIGN", "registry-key")
	t.Setenv("PODIUM_SIGN_KEY_PATH", f.keyPath)
	if _, err := f.boot(t); err != nil {
		t.Fatalf("first start: %v", err)
	}
	st := f.openStoreDirect(t)
	rows := downgradeRows(t, st, f.keyPath)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	absent := filepath.Join(f.home, "absent-signing.key")
	t.Setenv("PODIUM_SIGN_KEY_PATH", absent)

	assert := func(t *testing.T, runErr error) {
		t.Helper()
		if runErr == nil || !strings.Contains(runErr.Error(), "PODIUM_SIGN_KEY_PATH") {
			t.Fatalf("run = %v, want a refusal naming PODIUM_SIGN_KEY_PATH", runErr)
		}
		if _, err := os.Stat(absent); err == nil {
			t.Error("a key file exists after a refused start")
		}
		after := f.openStoreDirect(t)
		ctx := context.Background()
		for _, seeded := range rows {
			rec, gerr := after.GetManifest(ctx, seeded.TenantID, seeded.ArtifactID, seeded.Version)
			if gerr != nil {
				t.Fatalf("get manifest: %v", gerr)
			}
			if rec.ContentHash != seeded.ContentHash || rec.Signature != seeded.Signature {
				t.Errorf("%s was written by a refused start", rec.ArtifactID)
			}
		}
		applied, aerr := after.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)
		if aerr != nil || applied {
			t.Errorf("marker applied = %v (err %v), want false", applied, aerr)
		}
	}

	held, err := net.Listen("tcp", f.addr)
	if err != nil {
		t.Fatalf("hold the bind address: %v", err)
	}
	_, runErr := f.boot(t)
	assert(t, runErr)
	if err := held.Close(); err != nil {
		t.Fatalf("release the bind address: %v", err)
	}

	_, runErr = f.boot(t)
	assert(t, runErr)
}

// defaultKeyDirStore points the fixture's store at <home>/.podium/standalone,
// the directory the default signing key resolves to, so the §13.12
// persistence refusal passes a start with PODIUM_SIGN_KEY_PATH unset.
func (f *bootFixture) defaultKeyDirStore(t *testing.T) {
	t.Helper()
	f.sqlitePath = filepath.Join(f.home, ".podium", "standalone", "podium.db")
	t.Setenv("PODIUM_SQLITE_PATH", f.sqlitePath)
}

// Spec: §4.7.9, §13.4, §13.10 — the default signing mode reaches the
// generated-key refusal: a start with no signing variable set, no key file,
// and a signed stored row while the rewrite has not completed is refused.
// The store sits beside the default key so the persistence refusal, which
// run calls first, passes and the start reaches refuseGeneratedSigningKey.
func TestRun_DefaultSigningRefusesAGeneratedKey(t *testing.T) {
	f := newBootFixture(t)
	f.defaultKeyDirStore(t)
	t.Setenv("PODIUM_SIGN", "registry-key")
	t.Setenv("PODIUM_SIGN_KEY_PATH", f.keyPath)
	if _, err := f.boot(t); err != nil {
		t.Fatalf("first start: %v", err)
	}
	st := f.openStoreDirect(t)
	rows := downgradeRows(t, st, f.keyPath)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	t.Setenv("PODIUM_SIGN", "")
	t.Setenv("PODIUM_SIGN_KEY_PATH", "")
	_, err := f.boot(t)
	if err == nil || !strings.Contains(err.Error(), "names no file") {
		t.Fatalf("run = %v, want the generated-key refusal", err)
	}
	defaultKey := filepath.Join(f.home, ".podium", "standalone", "registry-signing.key")
	if _, serr := os.Stat(defaultKey); serr == nil {
		t.Error("a key file exists at the default path after a refused start")
	}
	after := f.openStoreDirect(t)
	for _, seeded := range rows {
		rec, gerr := after.GetManifest(context.Background(), seeded.TenantID, seeded.ArtifactID, seeded.Version)
		if gerr != nil {
			t.Fatalf("get manifest: %v", gerr)
		}
		if rec.ContentHash != seeded.ContentHash || rec.Signature != seeded.Signature {
			t.Errorf("%s was written by a refused start", rec.ArtifactID)
		}
	}
}

// Spec: §4.7.9, §13.4 — a key file that is present but did not sign the stored
// rows passes the generated-key refusal. The start is not refused, the pass
// classifies those rows signature_unverified and records completion, and the
// rows keep their stored hash: the accepted failure mode §4.7.9 records.
func TestRun_WrongKeyPresentLeavesSignedRowsUnverified(t *testing.T) {
	f := newBootFixture(t)
	t.Setenv("PODIUM_SIGN", "registry-key")
	t.Setenv("PODIUM_SIGN_KEY_PATH", f.keyPath)
	if _, err := f.boot(t); err != nil {
		t.Fatalf("first start: %v", err)
	}
	st := f.openStoreDirect(t)
	rows := downgradeRows(t, st, f.keyPath)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	other := filepath.Join(f.home, "other-signing.key")
	if _, err := loadOrGenerateRegistrySigner(other); err != nil {
		t.Fatalf("generate the other key: %v", err)
	}
	t.Setenv("PODIUM_SIGN_KEY_PATH", other)
	logs, err := f.boot(t)
	if err != nil {
		t.Fatalf("start with another key present = %v, want no refusal", err)
	}
	if !strings.Contains(logs, "signature_unverified") {
		t.Errorf("logs do not classify the rows signature_unverified:\n%s", logs)
	}
	after := f.openStoreDirect(t)
	ctx := context.Background()
	for _, seeded := range rows {
		rec, gerr := after.GetManifest(ctx, seeded.TenantID, seeded.ArtifactID, seeded.Version)
		if gerr != nil {
			t.Fatalf("get manifest: %v", gerr)
		}
		if rec.ContentHash != seeded.ContentHash {
			t.Errorf("%s content_hash = %s, want the stored %s", rec.ArtifactID, rec.ContentHash, seeded.ContentHash)
		}
	}
	applied, aerr := after.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)
	if aerr != nil || !applied {
		t.Errorf("marker applied = %v (err %v), want true", applied, aerr)
	}
}

// Spec: §13.12, §4.7.9 — a store outside the directory the default key
// resolves to needs PODIUM_SIGN_KEY_PATH. A first start on home A with the
// default SQLite store generates A's key and signs its row. A second start on a
// fresh home B pointing at A's store is refused by the persistence refusal and
// generates no key under B. A third start on B with PODIUM_SIGN_KEY_PATH naming
// A's key starts, and the row's signature verifies under that key.
// onlyRow returns the one manifest row the fixture's single-artifact layer
// stores, across every tenant.
func onlyRow(t *testing.T, st store.Store) store.ManifestRecord {
	t.Helper()
	ctx := context.Background()
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	var out []store.ManifestRecord
	for _, tenant := range tenants {
		recs, err := st.ListManifestsIncludingDeleted(ctx, tenant.ID)
		if err != nil {
			t.Fatalf("list manifests: %v", err)
		}
		out = append(out, recs...)
	}
	if len(out) != 1 {
		t.Fatalf("store holds %d row(s), want 1", len(out))
	}
	return out[0]
}

func TestRun_StoreOutsideTheKeyDirectoryNeedsAKeyPath(t *testing.T) {
	f := newBootFixture(t)
	f.defaultKeyDirStore(t)
	t.Setenv("PODIUM_SIGN", "")
	if _, err := f.boot(t); err != nil {
		t.Fatalf("first start on home A: %v", err)
	}
	keyA := filepath.Join(f.home, ".podium", "standalone", "registry-signing.key")
	if _, err := os.Stat(keyA); err != nil {
		t.Fatalf("the first start generated no key under home A: %v", err)
	}
	st := f.openStoreDirect(t)
	ctx := context.Background()
	rec := onlyRow(t, st)
	if rec.Signature == "" {
		t.Fatal("the first start stored the row unsigned")
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	homeB := t.TempDir()
	t.Setenv("HOME", homeB)
	t.Setenv("USERPROFILE", homeB)
	_, err := f.boot(t)
	if err == nil || !strings.Contains(err.Error(), "PODIUM_SIGN_KEY_PATH") || !strings.Contains(err.Error(), "PODIUM_SIGN=none") {
		t.Fatalf("second start on home B = %v, want the persistence refusal naming PODIUM_SIGN_KEY_PATH and PODIUM_SIGN=none", err)
	}
	if _, serr := os.Stat(filepath.Join(homeB, ".podium", "standalone", "registry-signing.key")); serr == nil {
		t.Error("a refused start generated a key under home B")
	}
	after := f.openStoreDirect(t)
	got := onlyRow(t, after)
	if got.ContentHash != rec.ContentHash || got.Signature != rec.Signature {
		t.Error("the refused start wrote the stored row")
	}
	if err := after.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	t.Setenv("PODIUM_SIGN_KEY_PATH", keyA)
	if _, err := f.boot(t); err != nil {
		t.Fatalf("third start on home B with A's key: %v", err)
	}
	final := f.openStoreDirect(t)
	row := onlyRow(t, final)
	signer, err := loadOrGenerateRegistrySigner(keyA)
	if err != nil {
		t.Fatalf("load A's key: %v", err)
	}
	if err := signer.Verify(ctx, row.ContentHash, row.Signature); err != nil {
		t.Errorf("the row's signature does not verify under A's key: %v", err)
	}
}
