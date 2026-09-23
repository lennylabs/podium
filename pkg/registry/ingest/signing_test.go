package ingest_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.9 — when ingest is configured with a Signer, the
// resulting ManifestRecord carries the produced envelope so
// downstream consumers can verify against PODIUM_VERIFY_SIGNATURES.
func TestIngest_AttachesSignatureWhenSignerConfigured(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	signer := sign.Noop{}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L",
		Signer: signer.Sign,
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{
				Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: medium\n---\n\nbody\n"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("Accepted = %d, want 1", res.Accepted)
	}
	stored, err := st.GetManifest(context.Background(), "t", "x", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if stored.Signature == "" {
		t.Errorf("Signature is empty; expected envelope from Noop signer")
	}
	if !strings.HasPrefix(stored.Signature, "noop:sha256:") {
		t.Errorf("Signature = %q, expected noop: prefix from Noop signer", stored.Signature)
	}
}

// Spec: §4.7.9, §6.10 — a signing failure rejects the one artifact with the
// ingest.sign_failed code and commits no manifest for it, while the rest of
// the batch is accepted and stored.
// Matrix: §6.10 (ingest.sign_failed)
func TestIngest_SignFailureRejectsArtifact(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	// The signer fails its first call alone, so exactly one artifact of the
	// batch is refused whatever order ingest walks the files in.
	var calls atomic.Int32
	failFirst := func(_ context.Context, hash string) (string, error) {
		if calls.Add(1) == 1 {
			return "", errors.New("provider offline")
		}
		return "env:" + hash, nil
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L",
		Signer: failFirst,
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{
				Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: medium\n---\n\nbody\n"),
			},
			"y/ARTIFACT.md": &fstest.MapFile{
				Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: y\nsensitivity: medium\n---\n\nbody\n"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1 (the rest of the batch)", res.Accepted)
	}
	if len(res.Rejected) != 1 || res.Rejected[0].Code != "ingest.sign_failed" {
		t.Fatalf("Rejected = %v, want one ingest.sign_failed entry", res.Rejected)
	}
	rejected := res.Rejected[0].ArtifactID
	accepted := map[string]string{"x": "y", "y": "x"}[rejected]
	if accepted == "" {
		t.Fatalf("rejected artifact %q is not in the batch", rejected)
	}
	if _, err := st.GetManifest(context.Background(), "t", rejected, "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("manifest %s persisted despite sign failure: %v", rejected, err)
	}
	stored, err := st.GetManifest(context.Background(), "t", accepted, "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest(%s): %v", accepted, err)
	}
	if !strings.HasPrefix(stored.Signature, "env:") {
		t.Errorf("accepted %s Signature = %q, want the signer's envelope", accepted, stored.Signature)
	}
}

// Spec: §4.7.9 — without a Signer the manifest stores no signature;
// the load path returns an empty envelope and PolicyNever consumers
// are unaffected.
func TestIngest_NoSignerProducesEmptySignature(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	_, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L",
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{
				Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	stored, _ := st.GetManifest(context.Background(), "t", "x", "1.0.0")
	if stored.Signature != "" {
		t.Errorf("Signature = %q, want empty (no signer wired)", stored.Signature)
	}
}
