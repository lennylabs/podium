package sign_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// deliveryFixture returns a record, its delivery hash, and a registry-managed
// key whose envelope over that hash verifies.
func deliveryFixture(t *testing.T) (version.DeliveryRecord, string, sign.RegistryManagedKey, string) {
	t.Helper()
	rec := version.DeliveryRecord{
		ID: "team/a", Version: "1.0.0", Type: "skill", ContentHash: "sha256:00",
		ArtifactRevision: "2025-01-01T00:00:00.000000Z", SkillRaw: "---\nname: a\n---\nbody\n",
		ManifestBody: "body\n", Resources: map[string]string{"r.txt": version.ResourceDigest([]byte("r"))},
	}
	hash := version.DeliveryHash(rec)
	_, privs := genKeys(t, 1)
	key := sign.RegistryManagedKey{PrivateKey: privs[0]}
	sig, err := key.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return rec, hash, key, sig
}

// Spec: §4.7.10, §6.6 step 2 — the shared check refuses a missing or
// differing delivery hash under every policy before it reads the signature,
// then maps a missing required signature to materialize.signature_missing and
// any other signature failure to materialize.signature_invalid.
func TestDeliveryCheck_Verify(t *testing.T) {
	t.Parallel()
	rec, hash, key, sig := deliveryFixture(t)
	_, other := genKeys(t, 1)
	wrongSig, err := sign.RegistryManagedKey{PrivateKey: other[0]}.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	tampered := rec
	tampered.ManifestBody = "altered\n"
	cases := []struct {
		name     string
		check    sign.DeliveryCheck
		rec      version.DeliveryRecord
		hash     string
		sig      string
		wantCode string
	}{
		{"verified", sign.DeliveryCheck{Policy: sign.PolicyAlways, Verifier: key}, rec, hash, sig, ""},
		{"never skips signature", sign.DeliveryCheck{Policy: sign.PolicyNever}, rec, hash, "", ""},
		{"missing hash under never", sign.DeliveryCheck{Policy: sign.PolicyNever}, rec, "", "", "materialize.content_hash_mismatch: the response carries no delivery_hash"},
		{"tampered under never", sign.DeliveryCheck{Policy: sign.PolicyNever}, tampered, hash, sig, "materialize.content_hash_mismatch: recomputed delivery hash"},
		{"uppercase hash", sign.DeliveryCheck{Policy: sign.PolicyNever}, rec, strings.ToUpper(hash), "", "materialize.content_hash_mismatch: recomputed delivery hash"},
		{"missing signature", sign.DeliveryCheck{Policy: sign.PolicyAlways, Verifier: key}, rec, hash, "", "materialize.signature_missing: "},
		{"wrong key", sign.DeliveryCheck{Policy: sign.PolicyAlways, Verifier: key}, rec, hash, wrongSig, "materialize.signature_invalid: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.check.Verify(context.Background(), tc.rec, tc.hash, tc.sig)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("Verify: %v", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tc.wantCode) {
				t.Fatalf("Verify = %v, want prefix %q", err, tc.wantCode)
			}
		})
	}
}

// Spec: §4.7.10 — the signature failure keeps its sentinel in the chain so a
// caller can branch on it with errors.Is.
func TestDeliveryCheck_VerifyWrapsSignatureSentinels(t *testing.T) {
	t.Parallel()
	rec, hash, key, _ := deliveryFixture(t)
	check := sign.DeliveryCheck{Policy: sign.PolicyAlways, Verifier: key}
	if err := check.Verify(context.Background(), rec, hash, ""); !errors.Is(err, sign.ErrSignatureMissing) {
		t.Fatalf("missing signature = %v, want ErrSignatureMissing", err)
	}
	if err := check.Verify(context.Background(), rec, hash, "{}"); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Fatalf("malformed envelope = %v, want ErrSignatureInvalid", err)
	}
}

// Spec: §4.7.10 — a large resource enters the record under its link
// content_hash, so a record built from a differing link hash fails the
// delivery hash comparison.
func TestDeliveryCheck_VerifyFramesLargeResourceLink(t *testing.T) {
	t.Parallel()
	inline := map[string][]byte{"r.txt": []byte("r")}
	linkHash := version.ResourceDigest([]byte("large body"))
	rec, _, key, _ := deliveryFixture(t)
	rec.Resources = version.ResourceHashes(inline, map[string]string{"big.bin": linkHash})
	hash := version.DeliveryHash(rec)
	sig, err := key.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	check := sign.DeliveryCheck{Policy: sign.PolicyAlways, Verifier: key}
	if err := check.Verify(context.Background(), rec, hash, sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	altered := rec
	altered.Resources = version.ResourceHashes(inline, map[string]string{"big.bin": version.ResourceDigest([]byte("other"))})
	err = check.Verify(context.Background(), altered, hash, sig)
	if err == nil || !strings.HasPrefix(err.Error(), "materialize.content_hash_mismatch: ") {
		t.Fatalf("Verify = %v, want materialize.content_hash_mismatch", err)
	}
}

// Spec: §4.7.10 — under always the delivery hash comparison runs before the
// signature check, so a tampered record carrying a wrong-key signature reports
// materialize.content_hash_mismatch rather than materialize.signature_invalid.
func TestDeliveryCheck_VerifyHashBeforeSignatureUnderAlways(t *testing.T) {
	t.Parallel()
	rec, hash, key, _ := deliveryFixture(t)
	_, other := genKeys(t, 1)
	wrongSig, err := sign.RegistryManagedKey{PrivateKey: other[0]}.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	rec.ManifestBody = "altered\n"
	check := sign.DeliveryCheck{Policy: sign.PolicyAlways, Verifier: key}
	err = check.Verify(context.Background(), rec, hash, wrongSig)
	if err == nil || !strings.HasPrefix(err.Error(), "materialize.content_hash_mismatch: ") {
		t.Fatalf("Verify = %v, want materialize.content_hash_mismatch", err)
	}
}
