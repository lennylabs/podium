package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// Seal makes a manifest record written without ingest admissible by the §13.4
// stored-row admission check. It assembles each bundled-resource body as
// admission does, from Inline when it is set and otherwise from objects under
// the ref's content hash; sets each inline ref's content hash and size from its
// bytes and each object-held ref's size from the body it read; sets the
// record's content hash to the §4.7.6 digest over the manifest, the SKILL.md,
// and those bodies; and, for a non-nil signer, sets the signature to the
// signer's envelope over that hash.
//
// It fails the test when a body is object-held and objects is nil, when an
// object read fails, when an object-held body's hash differs from its ref's
// content hash, which names the key the body was read from and so cannot be
// rewritten, and when the signer fails, so a fixture is never sealed over
// bytes admission would not read.
//
// Spec: §13.4.
func Seal(t testing.TB, rec store.ManifestRecord, objects objectstore.Provider, signer sign.Provider) store.ManifestRecord {
	t.Helper()
	ctx := context.Background()
	var bodies map[string][]byte
	if len(rec.Resources) > 0 {
		bodies = make(map[string][]byte, len(rec.Resources))
		refs := make([]store.ResourceRef, len(rec.Resources))
		copy(refs, rec.Resources)
		for i := range refs {
			bodies[refs[i].Path] = sealResource(t, ctx, &refs[i], objects)
		}
		rec.Resources = refs
	}
	rec.ContentHash = "sha256:" + version.CanonicalContentHash(rec.Frontmatter, rec.SkillRaw, bodies)
	rec.Signature = ""
	if signer != nil {
		sig, err := signer.Sign(ctx, rec.ContentHash)
		if err != nil {
			t.Fatalf("storetest.Seal: sign %s: %v", rec.ArtifactID, err)
		}
		rec.Signature = sig
	}
	return rec
}

// sealResource binds one ref's columns to its body and returns the body.
func sealResource(t testing.TB, ctx context.Context, ref *store.ResourceRef, objects objectstore.Provider) []byte {
	t.Helper()
	if ref.Inline != nil {
		ref.ContentHash = digestOf(ref.Inline)
		ref.Size = int64(len(ref.Inline))
		return ref.Inline
	}
	if objects == nil {
		t.Fatalf("storetest.Seal: resource %s is object-held and no object store was given", ref.Path)
	}
	key := strings.TrimPrefix(ref.ContentHash, "sha256:")
	body, err := objects.Get(ctx, key)
	if err != nil {
		t.Fatalf("storetest.Seal: read resource %s (%s): %v", ref.Path, key, err)
	}
	if digestOf(body) != ref.ContentHash {
		t.Fatalf("storetest.Seal: resource %s: the object under %s does not hash to its key", ref.Path, key)
	}
	ref.Size = int64(len(body))
	return body
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
