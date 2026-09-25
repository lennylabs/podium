package core_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

const admTenant = "t"

// admKey returns a registry-managed signer over a fresh Ed25519 keypair. Every
// key carries the same KeyID, so a wrong-key envelope fails on the signature
// itself rather than on the key identifier.
func admKey(t *testing.T) sign.RegistryManagedKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return sign.RegistryManagedKey{PrivateKey: priv}
}

// admFixture is one isolated store and object store with the registry's key
// and a second key the registry does not hold.
type admFixture struct {
	st      *store.Memory
	objects *objectstore.Memory
	key     sign.RegistryManagedKey
	other   sign.RegistryManagedKey
	events  admEvents
	// failGet and blockGet configure the object store registryWithFaults
	// builds: a Get that fails, or one that blocks until the test ends.
	failGet  error
	blockGet chan struct{}
}

func newAdmFixture(t *testing.T) *admFixture {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: admTenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	return &admFixture{st: st, objects: objectstore.NewMemory(), key: admKey(t), other: admKey(t)}
}

// registry builds a registry that admits under signer against the fixture's
// object store, recording every audit event.
func (f *admFixture) registry(signer sign.Provider) *core.Registry {
	return core.New(f.st, admTenant, nil).
		WithAdmission(signer, f.objects, time.Second).
		WithAudit(f.events.emit)
}

func (f *admFixture) put(t *testing.T, rec store.ManifestRecord) {
	t.Helper()
	if err := f.st.PutManifest(context.Background(), rec); err != nil {
		t.Fatalf("PutManifest %s@%s: %v", rec.ArtifactID, rec.Version, err)
	}
}

// object stores body in the fixture's object store and returns its
// "sha256:<hex>" content hash, the key ingest writes.
func (f *admFixture) object(t *testing.T, body []byte) string {
	t.Helper()
	h := admDigest(body)
	if err := f.objects.Put(context.Background(), strings.TrimPrefix(h, "sha256:"), body, "application/octet-stream"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	return h
}

type admEvents struct {
	mu     sync.Mutex
	events []core.AuditEvent
}

func (e *admEvents) emit(_ context.Context, ev core.AuditEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
}

func (e *admEvents) ofType(typ string) []core.AuditEvent {
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

func admDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// admManifest is an ARTIFACT.md with the given extra frontmatter lines.
func admManifest(ver, extra string) []byte {
	return []byte("---\ntype: context\nversion: " + ver + "\ndescription: admitted context\n" + extra + "---\n\nbody for " + ver + "\n")
}

// admRow is a sealed, signed row with one inline resource and one object-held
// resource above the inline cutoff.
func (f *admFixture) admRow(t *testing.T, id string) store.ManifestRecord {
	t.Helper()
	big := bytes.Repeat([]byte("z"), objectstore.InlineCutoff+16)
	return storetest.Seal(t, store.ManifestRecord{
		TenantID: admTenant, ArtifactID: id, Version: "1.0.0", Layer: "L",
		Type: "context", Frontmatter: admManifest("1.0.0", "sensitivity: medium\n"),
		Resources: []store.ResourceRef{
			{Path: "a.txt", Inline: []byte("small inline bytes")},
			{Path: "big.bin", ContentHash: f.object(t, big)},
		},
	}, f.objects, f.key)
}

// admLoad loads id and returns the result and the error.
func admLoad(reg *core.Registry, id string) (*core.LoadArtifactResult, error) {
	return reg.LoadArtifact(context.Background(), layer.Identity{IsPublic: true}, id, core.LoadArtifactOptions{})
}

// wantRefused asserts err wraps sentinel and that its message names only the
// requested ID.
func wantRefused(t *testing.T, err, sentinel error, requestedID string) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	if want := sentinel.Error() + ": " + requestedID; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// failingObjects is an object store whose Get fails, or, with block set,
// blocks without honoring its context until the test ends.
type failingObjects struct {
	*objectstore.Memory
	block   chan struct{}
	getFail error
}

func (f *failingObjects) Get(ctx context.Context, key string) ([]byte, error) {
	if f.block != nil {
		<-f.block
	}
	if f.getFail != nil {
		return nil, f.getFail
	}
	return f.Memory.Get(ctx, key)
}

// Spec: §13.4 — admission recomputes the §4.7.6 digest from the stored bytes,
// binds each resource ref to its body, reads object-held bodies, and verifies
// the stored signature under the configured signer, refusing each failure
// with its sentinel and a message that names only the requested ID.
func TestAdmit_RefusesAndAdmitsStoredRows(t *testing.T) {
	t.Parallel()

	t.Run("ingested row is admitted with derived fields from its bytes", func(t *testing.T) {
		t.Parallel()
		f := newAdmFixture(t)
		big := strings.Repeat("y", objectstore.InlineCutoff+32)
		art := "---\ntype: context\nversion: 1.0.0\ndescription: ingested\nsensitivity: high\ndeprecated: true\nreplaced_by: other/next\naudit_redact: [version]\n---\n\ningested body\n"
		if _, err := ingest.Ingest(context.Background(), f.st, ingest.Request{
			TenantID: admTenant, LayerID: "L",
			Files: fstest.MapFS{
				"ops/x/ARTIFACT.md":  &fstest.MapFile{Data: []byte(art)},
				"ops/x/data/big.bin": &fstest.MapFile{Data: []byte(big)},
				"ops/x/notes.txt":    &fstest.MapFile{Data: []byte("notes")},
			},
			ResourcePut: f.objects.Put,
			Signer:      f.key.Sign,
		}); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
		res, err := admLoad(f.registry(f.key), "ops/x")
		if err != nil {
			t.Fatalf("LoadArtifact: %v", err)
		}
		if res.Type != "context" || res.Sensitivity != "high" || !res.Deprecated || res.ReplacedBy != "other/next" || res.ManifestBody != "ingested body\n" {
			t.Errorf("derived fields = %q %q %v %q %q", res.Type, res.Sensitivity, res.Deprecated, res.ReplacedBy, res.ManifestBody)
		}
		ev := f.events.ofType("artifact.loaded")
		if len(ev) != 1 || !reflect.DeepEqual(ev[0].RedactKeys, []string{"version"}) {
			t.Errorf("read event RedactKeys = %+v, want [version]", ev)
		}
	})

	refusals := []struct {
		name     string
		mutate   func(t *testing.T, f *admFixture, rec *store.ManifestRecord)
		sentinel error
	}{
		{
			name: "bytes altered with the hash unchanged",
			mutate: func(_ *testing.T, _ *admFixture, rec *store.ManifestRecord) {
				rec.Frontmatter = admManifest("1.0.0", "sensitivity: low\n")
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			// The signature_unverified class the first-start pass leaves at
			// its stored pre-framing hash. The row fails both the hash and
			// the signature, so the hash-first order decides the code.
			name: "pre-framing digest with another key's envelope",
			mutate: func(t *testing.T, f *admFixture, rec *store.ManifestRecord) {
				rec.ContentHash = admDigest(append(append([]byte(nil), rec.Frontmatter...), rec.SkillRaw...))
				sig, err := f.other.Sign(context.Background(), rec.ContentHash)
				if err != nil {
					t.Fatalf("Sign: %v", err)
				}
				rec.Signature = sig
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name:     "empty stored hash",
			mutate:   func(_ *testing.T, _ *admFixture, rec *store.ManifestRecord) { rec.ContentHash = "" },
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "object-held body altered in the object store",
			mutate: func(t *testing.T, f *admFixture, rec *store.ManifestRecord) {
				key := strings.TrimPrefix(rec.Resources[1].ContentHash, "sha256:")
				if err := f.objects.Delete(context.Background(), key); err != nil {
					t.Fatalf("Delete: %v", err)
				}
				if err := f.objects.Put(context.Background(), key, []byte("replaced"), ""); err != nil {
					t.Fatalf("Put: %v", err)
				}
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "object-held body absent",
			mutate: func(t *testing.T, f *admFixture, rec *store.ManifestRecord) {
				if err := f.objects.Delete(context.Background(), strings.TrimPrefix(rec.Resources[1].ContentHash, "sha256:")); err != nil {
					t.Fatalf("Delete: %v", err)
				}
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "inline ref content hash pointed at another stored object",
			mutate: func(t *testing.T, f *admFixture, rec *store.ManifestRecord) {
				rec.Resources[0].ContentHash = f.object(t, []byte("another object"))
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name:     "inline ref size edited",
			mutate:   func(_ *testing.T, _ *admFixture, rec *store.ManifestRecord) { rec.Resources[0].Size++ },
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name:     "object-held ref size edited",
			mutate:   func(_ *testing.T, _ *admFixture, rec *store.ManifestRecord) { rec.Resources[1].Size-- },
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "bytes altered and the hash recomputed with the envelope left in place",
			mutate: func(t *testing.T, f *admFixture, rec *store.ManifestRecord) {
				sig := rec.Signature
				rec.Frontmatter = admManifest("1.0.0", "sensitivity: low\n")
				*rec = storetest.Seal(t, *rec, f.objects, nil)
				rec.Signature = sig
			},
			sentinel: core.ErrStoredSignatureInvalid,
		},
		{
			name: "bytes altered and the hash recomputed with the envelope cleared",
			mutate: func(t *testing.T, f *admFixture, rec *store.ManifestRecord) {
				rec.Frontmatter = admManifest("1.0.0", "sensitivity: low\n")
				*rec = storetest.Seal(t, *rec, f.objects, nil)
			},
			sentinel: core.ErrStoredSignatureMissing,
		},
		{
			name: "another key's envelope over the correct hash",
			mutate: func(t *testing.T, f *admFixture, rec *store.ManifestRecord) {
				*rec = storetest.Seal(t, *rec, f.objects, f.other)
			},
			sentinel: core.ErrStoredSignatureInvalid,
		},
		{
			name: "second ref under an existing path placed first",
			mutate: func(t *testing.T, f *admFixture, rec *store.ManifestRecord) {
				body := []byte("shadow bytes")
				dup := store.ResourceRef{Path: "a.txt", ContentHash: f.object(t, body), Size: int64(len(body))}
				rec.Resources = append([]store.ResourceRef{dup}, rec.Resources...)
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "second ref under an existing path placed last",
			mutate: func(t *testing.T, f *admFixture, rec *store.ManifestRecord) {
				body := []byte("shadow bytes")
				rec.Resources = append(rec.Resources, store.ResourceRef{Path: "a.txt", ContentHash: f.object(t, body), Size: int64(len(body))})
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "an object store whose Get fails",
			mutate: func(_ *testing.T, f *admFixture, _ *store.ManifestRecord) {
				f.failGet = errors.New("backend down")
			},
			sentinel: core.ErrUnavailable,
		},
		{
			name: "an object store whose Get blocks past the deadline",
			mutate: func(t *testing.T, f *admFixture, _ *store.ManifestRecord) {
				f.blockGet = make(chan struct{})
				t.Cleanup(func() { close(f.blockGet) })
			},
			sentinel: core.ErrUnavailable,
		},
		{
			// Assembly reads every body before any ref is bound, so an inline
			// ref with a wrong size ahead of an object read that times out is
			// answered as unavailable rather than as a hash mismatch.
			name: "an inline ref with a wrong size ahead of an object read that times out",
			mutate: func(t *testing.T, f *admFixture, rec *store.ManifestRecord) {
				rec.Resources[0].Size++
				f.blockGet = make(chan struct{})
				t.Cleanup(func() { close(f.blockGet) })
			},
			sentinel: core.ErrUnavailable,
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newAdmFixture(t)
			rec := f.admRow(t, "ops/row")
			tc.mutate(t, f, &rec)
			f.put(t, rec)
			_, err := admLoad(f.registryWithFaults(f.key), "ops/row")
			wantRefused(t, err, tc.sentinel, "ops/row")
		})
	}

	t.Run("a registry with no object store refuses an object-held body", func(t *testing.T) {
		t.Parallel()
		f := newAdmFixture(t)
		f.put(t, f.admRow(t, "ops/row"))
		reg := core.New(f.st, admTenant, nil)
		_, err := admLoad(reg, "ops/row")
		wantRefused(t, err, core.ErrUnavailable, "ops/row")
	})

	t.Run("no signer admits bytes altered with the hash recomputed", func(t *testing.T) {
		t.Parallel()
		f := newAdmFixture(t)
		rec := f.admRow(t, "ops/row")
		rec.Frontmatter = admManifest("1.0.0", "sensitivity: low\n")
		rec = storetest.Seal(t, rec, f.objects, nil)
		f.put(t, rec)
		res, err := admLoad(f.registry(nil), "ops/row")
		if err != nil {
			t.Fatalf("LoadArtifact: %v", err)
		}
		if res.Sensitivity != "low" {
			t.Errorf("Sensitivity = %q, want low", res.Sensitivity)
		}
	})

	t.Run("derived fields edited alone are served from the bytes", func(t *testing.T) {
		t.Parallel()
		f := newAdmFixture(t)
		rec := f.admRow(t, "ops/row")
		rec.Body = []byte("forged body")
		rec.Type = "skill"
		rec.Sensitivity = "low"
		rec.Deprecated = true
		rec.ReplacedBy = "forged/next"
		rec.AuditRedact = []string{"layer"}
		f.put(t, rec)
		res, err := admLoad(f.registry(f.key), "ops/row")
		if err != nil {
			t.Fatalf("LoadArtifact: %v", err)
		}
		if res.ManifestBody != "body for 1.0.0\n" || res.Type != "context" || res.Sensitivity != "medium" || res.Deprecated || res.ReplacedBy != "" || res.DeprecationWarning != "" {
			t.Errorf("served %q %q %q %v %q, want the parsed values", res.ManifestBody, res.Type, res.Sensitivity, res.Deprecated, res.ReplacedBy)
		}
		if ev := f.events.ofType("artifact.loaded"); len(ev) != 1 || len(ev[0].RedactKeys) != 0 {
			t.Errorf("read event RedactKeys = %+v, want none", ev)
		}
	})

	t.Run("identity rewritten with bytes, hash, and envelope unchanged is admitted", func(t *testing.T) {
		t.Parallel()
		f := newAdmFixture(t)
		rec := f.admRow(t, "ops/row")
		rec.ArtifactID = "ops/impostor"
		rec.Version = "9.9.9"
		f.put(t, rec)
		res, err := admLoad(f.registry(f.key), "ops/impostor")
		if err != nil {
			t.Fatalf("LoadArtifact: %v", err)
		}
		if res.Version != "9.9.9" {
			t.Errorf("Version = %q, want the stored row's 9.9.9", res.Version)
		}
	})

	t.Run("empty frontmatter with an empty pin serves its stored fields", func(t *testing.T) {
		t.Parallel()
		f := newAdmFixture(t)
		rec := storetest.Seal(t, store.ManifestRecord{
			TenantID: admTenant, ArtifactID: "ops/bare", Version: "1.0.0", Layer: "L",
			Type: "context", Sensitivity: "high", ReplacedBy: "ops/next", Body: []byte("stored body"),
		}, nil, f.key)
		f.put(t, rec)
		res, err := admLoad(f.registry(f.key), "ops/bare")
		if err != nil {
			t.Fatalf("LoadArtifact: %v", err)
		}
		if res.Sensitivity != "high" || res.ReplacedBy != "ops/next" || res.ManifestBody != "stored body" {
			t.Errorf("served %q %q %q, want the stored values", res.Sensitivity, res.ReplacedBy, res.ManifestBody)
		}
		pinned := rec
		pinned.ArtifactID = "ops/bare-pinned"
		pinned.ExtendsPin = "ops/bare@1.0.0"
		f.put(t, pinned)
		_, err = admLoad(f.registry(f.key), "ops/bare-pinned")
		wantRefused(t, err, core.ErrContentHashMismatch, "ops/bare-pinned")
	})
}

// registryWithFaults builds the fixture's registry over an object store that
// applies the failures an arm configured.
func (f *admFixture) registryWithFaults(signer sign.Provider) *core.Registry {
	objects := &failingObjects{Memory: f.objects, block: f.blockGet, getFail: f.failGet}
	return core.New(f.st, admTenant, nil).
		WithAdmission(signer, objects, 50*time.Millisecond).
		WithAudit(f.events.emit)
}

// admChainRow is a sealed, signed row whose manifest declares extends (empty
// for none) and whose stored pin is pin.
func (f *admFixture) admChainRow(t *testing.T, id, ver, extends, pin, extra string, signer sign.Provider) store.ManifestRecord {
	t.Helper()
	if extends != "" {
		extra += "extends: " + extends + "\n"
	}
	return storetest.Seal(t, store.ManifestRecord{
		TenantID: admTenant, ArtifactID: id, Version: ver, Layer: "L", Type: "context",
		Frontmatter: admManifest(ver, extra), ExtendsPin: pin,
	}, nil, signer)
}

// Spec: §13.4 — admission checks each row's pin against the extends: its
// admitted bytes declare, admits each chain row before it follows that row's
// pin, compares a content-hash reference after the parent is admitted, and
// names only the requested artifact in every error.
func TestAdmit_ChainRefusesTamperedPinsAndRows(t *testing.T) {
	t.Parallel()

	type chainCase struct {
		name     string
		seed     func(t *testing.T, f *admFixture)
		sentinel error
	}
	parent := func(t *testing.T, f *admFixture) store.ManifestRecord {
		p := f.admChainRow(t, "base/p", "1.0.0", "", "", "sensitivity: low\n", f.key)
		f.put(t, p)
		return p
	}
	cases := []chainCase{
		{
			name: "parent bytes altered with its hash unchanged",
			seed: func(t *testing.T, f *admFixture) {
				p := f.admChainRow(t, "base/p", "1.0.0", "", "", "", f.key)
				p.Frontmatter = admManifest("1.0.0", "sensitivity: high\n")
				f.put(t, p)
				f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@1.0.0", "base/p@1.0.0", "", f.key))
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "parent envelope under another key",
			seed: func(t *testing.T, f *admFixture) {
				f.put(t, f.admChainRow(t, "base/p", "1.0.0", "", "", "", f.other))
				f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@1.0.0", "base/p@1.0.0", "", f.key))
			},
			sentinel: core.ErrStoredSignatureInvalid,
		},
		{
			name: "pin cleared on a child declaring extends",
			seed: func(t *testing.T, f *admFixture) {
				parent(t, f)
				f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@1.0.0", "", "", f.key))
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "pin added to a row declaring none",
			seed: func(t *testing.T, f *admFixture) {
				parent(t, f)
				f.put(t, f.admChainRow(t, "team/c", "1.0.0", "", "base/p@1.0.0", "", f.key))
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "pin pointed at another artifact",
			seed: func(t *testing.T, f *admFixture) {
				parent(t, f)
				f.put(t, f.admChainRow(t, "base/q", "1.0.0", "", "", "", f.key))
				f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@1.0.0", "base/q@1.0.0", "", f.key))
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "pin outside a declared exact reference",
			seed: func(t *testing.T, f *admFixture) {
				parent(t, f)
				f.put(t, f.admChainRow(t, "base/p", "2.0.0", "", "", "", f.key))
				f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@1.0.0", "base/p@2.0.0", "", f.key))
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "pin at a parent version whose hash the content-hash reference does not name",
			seed: func(t *testing.T, f *admFixture) {
				p := parent(t, f)
				f.put(t, f.admChainRow(t, "base/p", "2.0.0", "", "", "", f.key))
				f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@"+p.ContentHash, "base/p@2.0.0", "", f.key))
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			// Check (4) runs after the child's own signature check (3), so a
			// child failing both carries the signature code.
			name: "content-hash reference mismatch under a wrong-key child envelope",
			seed: func(t *testing.T, f *admFixture) {
				p := parent(t, f)
				f.put(t, f.admChainRow(t, "base/p", "2.0.0", "", "", "", f.key))
				f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@"+p.ContentHash, "base/p@2.0.0", "", f.other))
			},
			sentinel: core.ErrStoredSignatureInvalid,
		},
		{
			name: "root given a pin toward a row the store does not hold",
			seed: func(t *testing.T, f *admFixture) {
				f.put(t, f.admChainRow(t, "base/root", "1.0.0", "", "ghost/row@1.0.0", "", f.key))
				f.put(t, f.admChainRow(t, "base/mid", "1.0.0", "base/root@1.0.0", "base/root@1.0.0", "", f.key))
				f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/mid@1.0.0", "base/mid@1.0.0", "", f.key))
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "root given a pin pointed back at the requested child",
			seed: func(t *testing.T, f *admFixture) {
				f.put(t, f.admChainRow(t, "base/root", "1.0.0", "", "team/c@1.0.0", "", f.key))
				f.put(t, f.admChainRow(t, "base/mid", "1.0.0", "base/root@1.0.0", "base/root@1.0.0", "", f.key))
				f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/mid@1.0.0", "base/mid@1.0.0", "", f.key))
			},
			sentinel: core.ErrContentHashMismatch,
		},
		{
			name: "pin agreeing with an exact reference the store does not hold",
			seed: func(t *testing.T, f *admFixture) {
				f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@3.0.0", "base/p@3.0.0", "", f.key))
			},
			sentinel: core.ErrNotFound,
		},
		{
			name: "two rows declaring each other with agreeing pins",
			seed: func(t *testing.T, f *admFixture) {
				f.put(t, f.admChainRow(t, "base/p", "1.0.0", "team/c@1.0.0", "team/c@1.0.0", "", f.key))
				f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@1.0.0", "base/p@1.0.0", "", f.key))
			},
			sentinel: core.ErrInvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newAdmFixture(t)
			tc.seed(t, f)
			_, err := admLoad(f.registry(f.key), "team/c")
			wantRefused(t, err, tc.sentinel, "team/c")
		})
	}

	t.Run("pin at another version a minor reference accepts merges that version", func(t *testing.T) {
		t.Parallel()
		f := newAdmFixture(t)
		f.put(t, f.admChainRow(t, "base/p", "1.0.0", "", "", "sensitivity: high\n", f.key))
		f.put(t, f.admChainRow(t, "base/p", "1.0.1", "", "", "sensitivity: low\n", f.key))
		f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@1.0.x", "base/p@1.0.0", "sensitivity: low\n", f.key))
		res, err := admLoad(f.registry(f.key), "team/c")
		if err != nil {
			t.Fatalf("LoadArtifact: %v", err)
		}
		if res.Sensitivity != "high" {
			t.Errorf("Sensitivity = %q; want the merge over base/p@1.0.0 (high)", res.Sensitivity)
		}
	})
}

// admDispositions is CODE-14's column table: the disposition of every field of
// store.ManifestRecord and store.ResourceRef under §13.4 admission.
var admDispositions = map[string]string{
	"ManifestRecord.Frontmatter":      "covered",
	"ManifestRecord.SkillRaw":         "covered",
	"ManifestRecord.Resources":        "covered",
	"ResourceRef.Path":                "covered",
	"ResourceRef.Inline":              "covered",
	"ManifestRecord.ContentHash":      "checked",
	"ResourceRef.ContentHash":         "checked",
	"ResourceRef.Size":                "checked",
	"ManifestRecord.ExtendsPin":       "checked",
	"ManifestRecord.Signature":        "checked",
	"ManifestRecord.Body":             "derived",
	"ManifestRecord.Type":             "derived",
	"ManifestRecord.Sensitivity":      "derived",
	"ManifestRecord.Deprecated":       "derived",
	"ManifestRecord.ReplacedBy":       "derived",
	"ManifestRecord.AuditRedact":      "derived",
	"ManifestRecord.TenantID":         "selection",
	"ManifestRecord.ArtifactID":       "selection",
	"ManifestRecord.Version":          "selection",
	"ManifestRecord.Layer":            "selection",
	"ManifestRecord.IngestedAt":       "selection",
	"ManifestRecord.DeletedAt":        "selection",
	"ManifestRecord.DeprecatedAt":     "selection",
	"ManifestRecord.Name":             "search-only",
	"ManifestRecord.Description":      "search-only",
	"ManifestRecord.WhenToUse":        "search-only",
	"ManifestRecord.Tags":             "search-only",
	"ManifestRecord.SearchVisibility": "search-only",
	"ResourceRef.ContentType":         "served as stored",
}

// Spec: §13.4 — every stored field has one admission disposition, so a field
// added to either record type fails here until it is disposed of.
func TestAdmit_EveryRecordFieldIsDisposed(t *testing.T) {
	t.Parallel()
	var fields []string
	for _, typ := range []reflect.Type{reflect.TypeOf(store.ManifestRecord{}), reflect.TypeOf(store.ResourceRef{})} {
		for _, sf := range reflect.VisibleFields(typ) {
			fields = append(fields, typ.Name()+"."+sf.Name)
		}
	}
	present := map[string]bool{}
	for _, name := range fields {
		present[name] = true
		if _, ok := admDispositions[name]; !ok {
			t.Errorf("%s has no admission disposition", name)
		}
	}
	var stale []string
	for name := range admDispositions {
		if !present[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		t.Errorf("the disposition table names %s, which does not exist", name)
	}
}

// Spec: §8.2, §13.4 — a full load's read event carries the audit_redact key
// set admission derives from the manifest, whatever the stored column says.
func TestLoadArtifact_ReadEventRedactsDerivedKeys(t *testing.T) {
	t.Parallel()
	f := newAdmFixture(t)
	f.put(t, f.admChainRow(t, "ops/pii", "1.0.0", "", "", "ssn: 123-45-6789\naudit_redact: [ssn]\n", nil))
	if _, err := admLoad(f.registry(nil), "ops/pii"); err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	ev := f.events.ofType("artifact.loaded")
	if len(ev) != 1 || !reflect.DeepEqual(ev[0].RedactKeys, []string{"ssn"}) {
		t.Fatalf("read event = %+v, want RedactKeys [ssn]", ev)
	}
}

// revalidateAll reports every result as a bypass, as a HEAD does.
func revalidateAll(*core.LoadArtifactResult) bool { return true }

// Spec: §8.2, §13.4 — a revalidation answered without admission records the
// key set a full load would, folded child-wins over the extends: chain.
func TestLoadArtifact_RevalidatedReadEventRedactsKeys(t *testing.T) {
	t.Parallel()
	revalidate := func(t *testing.T, reg *core.Registry, id string) (*core.LoadArtifactResult, error) {
		t.Helper()
		return reg.LoadArtifact(context.Background(), layer.Identity{IsPublic: true}, id, core.LoadArtifactOptions{Revalidate: revalidateAll})
	}

	t.Run("own directive", func(t *testing.T) {
		t.Parallel()
		f := newAdmFixture(t)
		f.put(t, f.admChainRow(t, "ops/r", "1.0.0", "", "", "audit_redact: [version]\n", nil))
		if _, err := revalidate(t, f.registry(nil), "ops/r"); err != nil {
			t.Fatalf("LoadArtifact: %v", err)
		}
		assertRedactsVersion(t, f)
	})

	t.Run("inherited directive", func(t *testing.T) {
		t.Parallel()
		f := newAdmFixture(t)
		f.put(t, f.admChainRow(t, "base/p", "1.0.0", "", "", "audit_redact: [version]\n", nil))
		f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@1.0.0", "base/p@1.0.0", "", nil))
		res, err := revalidate(t, f.registry(nil), "team/c")
		if err != nil {
			t.Fatalf("LoadArtifact: %v", err)
		}
		if res.ManifestBody != "" || res.Frontmatter != nil || res.ContentHash == "" {
			t.Errorf("revalidation result carries content or no validator: %+v", res)
		}
		assertRedactsVersion(t, f)
	})

	t.Run("unresolvable chain names only the requested ID", func(t *testing.T) {
		t.Parallel()
		f := newAdmFixture(t)
		f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@3.0.0", "base/p@3.0.0", "", nil))
		_, err := revalidate(t, f.registry(nil), "team/c")
		wantRefused(t, err, core.ErrNotFound, "team/c")
	})
}

// Spec: §8.2, §13.4 — a chain root whose manifest is empty is served
// with its stored audit_redact, deprecated, and replaced_by, so a full load and
// a revalidation of a child that declares none record the same key set.
func TestLoadArtifact_EmptyRootKeepsStoredFieldsInChain(t *testing.T) {
	t.Parallel()
	f := newAdmFixture(t)
	root := storetest.Seal(t, store.ManifestRecord{
		TenantID: admTenant, ArtifactID: "base/p", Version: "1.0.0", Layer: "L", Type: "context",
		Frontmatter: nil,
		AuditRedact: []string{"version"}, Deprecated: true, ReplacedBy: "base/q",
	}, nil, nil)
	f.put(t, root)
	f.put(t, f.admChainRow(t, "team/c", "1.0.0", "base/p@1.0.0", "base/p@1.0.0", "", nil))
	reg := f.registry(nil)

	res, err := admLoad(reg, "team/c")
	if err != nil {
		t.Fatalf("full LoadArtifact: %v", err)
	}
	if !res.Deprecated || res.ReplacedBy != "base/q" {
		t.Errorf("full load deprecated=%v replaced_by=%q, want the root's stored true and base/q", res.Deprecated, res.ReplacedBy)
	}
	if _, err := reg.LoadArtifact(context.Background(), layer.Identity{IsPublic: true}, "team/c",
		core.LoadArtifactOptions{Revalidate: revalidateAll}); err != nil {
		t.Fatalf("revalidated LoadArtifact: %v", err)
	}
	ev := f.events.ofType("artifact.loaded")
	if len(ev) != 2 {
		t.Fatalf("artifact.loaded events = %d, want 2", len(ev))
	}
	for i, e := range ev {
		if !reflect.DeepEqual(e.RedactKeys, []string{"version"}) {
			t.Errorf("read event %d RedactKeys = %v, want [version]", i, e.RedactKeys)
		}
	}
}

func assertRedactsVersion(t *testing.T, f *admFixture) {
	t.Helper()
	ev := f.events.ofType("artifact.loaded")
	if len(ev) != 1 {
		t.Fatalf("artifact.loaded events = %d, want 1", len(ev))
	}
	if !reflect.DeepEqual(ev[0].RedactKeys, []string{"version"}) || ev[0].Context["version"] == "" {
		t.Errorf("read event = %+v, want RedactKeys [version] beside a version context key", ev[0])
	}
}

// Spec: §6.3.1, §13.4 — the revalidation bypass is consulted only after the
// load-scope check, so a caller outside its scope is denied as on a full load.
func TestLoadArtifact_RevalidationKeepsLoadScope(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{"podium:load:other/*", "podium:load:ops/r@2.x"} {
		t.Run(scope, func(t *testing.T) {
			t.Parallel()
			f := newAdmFixture(t)
			f.put(t, f.admChainRow(t, "ops/r", "1.0.0", "", "", "", nil))
			id := layer.Identity{Sub: "alice", IsAuthenticated: true, Scopes: []string{scope}}
			_, err := f.registry(nil).LoadArtifact(context.Background(), id, "ops/r", core.LoadArtifactOptions{Revalidate: revalidateAll})
			if !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
			if denied := f.events.ofType("visibility.denied"); len(denied) != 1 {
				t.Errorf("visibility.denied events = %d, want 1", len(denied))
			}
			loaded := f.events.ofType("artifact.loaded")
			if len(loaded) != 1 || loaded[0].ResultSize != 0 {
				t.Fatalf("artifact.loaded = %+v, want one event of size 0", loaded)
			}
			if _, ok := loaded[0].Context["content_hash"]; ok {
				t.Errorf("a denied revalidation recorded a content_hash: %+v", loaded[0].Context)
			}
		})
	}
}

// Spec: §13.4 — the admission sentinels carry their §6.10 codes.
func TestAdmit_SentinelsCarryTheirCodes(t *testing.T) {
	t.Parallel()
	for sentinel, code := range map[error]string{
		core.ErrContentHashMismatch:    "materialize.content_hash_mismatch",
		core.ErrStoredSignatureMissing: "materialize.signature_missing",
		core.ErrStoredSignatureInvalid: "materialize.signature_invalid",
	} {
		if sentinel.Error() != code {
			t.Errorf("sentinel %q, want %q", sentinel.Error(), code)
		}
	}
}
