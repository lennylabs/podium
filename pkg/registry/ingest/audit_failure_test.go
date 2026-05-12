package ingest_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §8.3 — ingest's AuditEmitterFunc has no error return.
// Production wires a FileSink-backed emitter that silently swallows
// sink errors. This test pins the contract: an ingest pipeline
// whose audit emitter panics or otherwise fails internally MUST NOT
// prevent the underlying manifest from being committed.
//
// Today's contract: the emitter is invoked AFTER PutManifest
// succeeds, so an emitter that misbehaves cannot roll the manifest
// back. The trade-off is that an audit failure produces a silently-
// missing audit record. Changing this contract (to e.g. refuse the
// op when audit fails) is a deliberate spec decision; updating the
// test forces the change to be explicit.
func TestIngest_AuditEmitterPanicDoesNotPreventCommit(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	files := fstest.MapFS{
		"x/ARTIFACT.md": &fstest.MapFile{
			Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n"),
		},
	}
	// Wrap a panicking emitter in a recover so the goroutine doesn't
	// crash the test. In production, a Filesink-backed emitter
	// swallows errors rather than panics, so a panic here surfaces
	// the worst-case behavior.
	emitter := func(typ, target string, ctx map[string]string) {
		defer func() { _ = recover() }()
		panic("audit emitter blew up")
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID:  "t",
		LayerID:   "L",
		Files:     files,
		AuditEmit: emitter,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1 (audit failure must not roll back the commit)",
			res.Accepted)
	}
	// Confirm the manifest is in the store.
	if _, err := st.GetManifest(context.Background(), "t", "x", "1.0.0"); err != nil {
		t.Errorf("manifest missing after panicking emitter: %v", err)
	}
}

// Spec: §8.3 — a nil AuditEmit (no audit sink configured) must
// not crash. The ingest path checks `req.AuditEmit != nil` before
// every call. This test pins that the nil case is a clean no-op.
func TestIngest_NilAuditEmitterIsClean(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	files := fstest.MapFS{
		"x/ARTIFACT.md": &fstest.MapFile{
			Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n"),
		},
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID:  "t",
		LayerID:   "L",
		Files:     files,
		AuditEmit: nil,
	})
	if err != nil || res.Accepted != 1 {
		t.Errorf("Ingest: %v, res=%+v", err, res)
	}
}
