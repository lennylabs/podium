package ingest_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/ingest"
)

// putRecorder is a fake ingest.ResourcePut that records every uploaded
// blob so tests can assert what reached the §7.2 object store.
type putRecorder struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newPutRecorder() *putRecorder { return &putRecorder{objects: map[string][]byte{}} }

func (p *putRecorder) put(_ context.Context, key string, body []byte, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.objects[key] = append([]byte(nil), body...)
	return nil
}

func hashKey(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

// Spec: §7.2 / §4.4 — with an object store wired, ingest persists a
// resource ref per bundled file, uploads every blob keyed by content
// hash, keeps small resources inline on the record, and drops the inline
// bytes of large resources (delivered from object storage).
func TestIngest_PersistsResourcesWithObjectStore(t *testing.T) {
	t.Parallel()
	small := []byte("print('inline')\n")
	large := []byte(strings.Repeat("A", objectstore.InlineCutoff+1024))
	files := fstest.MapFS{
		"finance/run/ARTIFACT.md":    &fstest.MapFile{Data: []byte(skillArtifact())},
		"finance/run/SKILL.md":       &fstest.MapFile{Data: []byte(skillBody("run"))},
		"finance/run/scripts/run.py": &fstest.MapFile{Data: small},
		"finance/run/data/big.bin":   &fstest.MapFile{Data: large},
	}
	rec := newPutRecorder()
	st := newStore(t)
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: files, ResourcePut: rec.put,
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mr, err := st.GetManifest(context.Background(), "t", "finance/run", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if len(mr.Resources) != 2 {
		t.Fatalf("Resources len = %d, want 2: %+v", len(mr.Resources), mr.Resources)
	}
	byPath := map[string]int{}
	for i, r := range mr.Resources {
		byPath[r.Path] = i
	}
	smallRef := mr.Resources[byPath["scripts/run.py"]]
	if string(smallRef.Inline) != string(small) {
		t.Errorf("small resource should stay inline, got %q", smallRef.Inline)
	}
	if smallRef.ContentHash != "sha256:"+hashKey(small) {
		t.Errorf("small ContentHash = %q", smallRef.ContentHash)
	}
	largeRef := mr.Resources[byPath["data/big.bin"]]
	if largeRef.Inline != nil {
		t.Errorf("large resource inline bytes should be dropped, got %d bytes", len(largeRef.Inline))
	}
	if largeRef.Size != int64(len(large)) {
		t.Errorf("large Size = %d, want %d", largeRef.Size, len(large))
	}

	// Both blobs uploaded to the object store keyed by content hash.
	if got := rec.objects[hashKey(small)]; string(got) != string(small) {
		t.Errorf("small blob not uploaded under its hash")
	}
	if got := rec.objects[hashKey(large)]; string(got) != string(large) {
		t.Errorf("large blob not uploaded under its hash")
	}
}

// Spec: §7.2 — without an object store (PODIUM_OBJECT_STORE=none), every
// resource keeps its bytes inline on the record regardless of size, so
// load_artifact can still serve them.
func TestIngest_PersistsResourcesInlineWithoutObjectStore(t *testing.T) {
	t.Parallel()
	large := []byte(strings.Repeat("B", objectstore.InlineCutoff+512))
	files := fstest.MapFS{
		"finance/run/ARTIFACT.md":  &fstest.MapFile{Data: []byte(skillArtifact())},
		"finance/run/SKILL.md":     &fstest.MapFile{Data: []byte(skillBody("run"))},
		"finance/run/data/big.bin": &fstest.MapFile{Data: large},
	}
	st := newStore(t)
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: files, // ResourcePut nil
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mr, err := st.GetManifest(context.Background(), "t", "finance/run", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if len(mr.Resources) != 1 {
		t.Fatalf("Resources len = %d, want 1", len(mr.Resources))
	}
	if string(mr.Resources[0].Inline) != string(large) {
		t.Errorf("without an object store a large resource must stay inline")
	}
}

// Spec: §4.4 — an artifact with no bundled files has no resource refs.
func TestIngest_NoResourcesYieldsNoRefs(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{
		"finance/glossary/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("g"))},
	}
	st := newStore(t)
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: files, ResourcePut: newPutRecorder().put,
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mr, err := st.GetManifest(context.Background(), "t", "finance/glossary", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if len(mr.Resources) != 0 {
		t.Errorf("Resources = %+v, want none", mr.Resources)
	}
}
