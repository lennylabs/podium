package ingest_test

import (
	"context"
	"errors"
	"strings"
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

// Spec: §4.7.9 — a signing failure surfaces as a Rejected entry
// with the ingest.sign_failed code; the manifest is not committed.
func TestIngest_SignFailureRejectsArtifact(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	failSigner := func(string) (string, error) {
		return "", errors.New("provider offline")
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L",
		Signer: failSigner,
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{
				Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: medium\n---\n\nbody\n"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 0 {
		t.Errorf("Accepted = %d, want 0 (sign failure should reject)", res.Accepted)
	}
	if len(res.Rejected) != 1 || res.Rejected[0].Code != "ingest.sign_failed" {
		t.Errorf("Rejected = %v, want one ingest.sign_failed entry", res.Rejected)
	}
	if _, err := st.GetManifest(context.Background(), "t", "x", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("manifest persisted despite sign failure: %v", err)
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
