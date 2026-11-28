package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.2 (Attestation members), §7.3.5 (Serving bundles), §4.7.11
// (Key-based signers, Attestation storage) — a layer whose covering
// requirement names a key-based signer, and whose optional requirement
// names a second key that signed the artifacts with no signed time, serves
// each link's signer as the empty instance and issuer with the key's
// identifier, attested_at for the signed bundle alone, the outcome
// verified, and each key-signed bundle through the bundle endpoint.
func TestAttestServing_KeySigners(t *testing.T) {
	t.Parallel()
	const reviewType = "https://acme.com/review/v1"
	build, review := sh.NewSigningKey(t), sh.NewSigningKey(t)
	signedAt := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	var buildBundle, reviewBundle []byte
	for name, open := range storesUnderTest(t) {
		t.Run(name, func(t *testing.T) {
			f := newServingFixtureWith(t, open(t), servingPolicy{mode: "require",
				reqs: []store.TrustRequirement{
					{PredicateType: asPredicateType, CoversLayer: true, Signers: []store.TrustSigner{{PublicKey: sh.PublicKeyPEM(t, build.Public())}}},
					{PredicateType: reviewType, Optional: true, Signers: []store.TrustSigner{{PublicKey: sh.PublicKeyPEM(t, review.Public())}}},
				},
				bundles: func(h *sh.Harness, subjects []sh.Subject) [][]byte {
					all := append([]sh.Subject{subjects[0], sh.SignedTimeSubject(signedAt, subjects[0].SHA256)}, subjects[1:]...)
					buildBundle = h.DSSEBundle(sh.Statement(t, asPredicateType, all...), sh.WithSigningKey(build))
					reviewBundle = h.DSSEBundle(sh.Statement(t, reviewType, subjects[1:]...), sh.WithSigningKey(review))
					return [][]byte{buildBundle, reviewBundle}
				}})
			status, _, raw := f.do(t, http.MethodGet, "/v1/load_artifact?id=finance/pay", "alice", nil)
			var p provenance
			if status != http.StatusOK || json.Unmarshal(raw, &p) != nil {
				t.Fatalf("load = %d: %s", status, raw)
			}
			if p.AttestationOutcome == nil || p.AttestationOutcome.Outcome != "verified" || len(p.Attestations) != 2 {
				t.Fatalf("provenance %s", raw)
			}
			want := map[string][2]string{
				attest.AttestationDigest(buildBundle):  {sh.KeyIdentifier(t, build.Public()), signedAt.Format(time.RFC3339Nano)},
				attest.AttestationDigest(reviewBundle): {sh.KeyIdentifier(t, review.Public()), ""},
			}
			for _, a := range p.Attestations {
				w, ok := want[a.Digest]
				if !ok || a.Signer == nil || a.Signer.Instance != "" || a.Signer.Issuer != "" || a.Signer.Identity != w[0] || a.AttestedAt != w[1] {
					t.Errorf("attestation %+v signer %+v, want identity %s attested_at %q", a, a.Signer, w[0], w[1])
				}
				status, _, body := f.do(t, http.MethodGet, "/v1/attestations/"+a.Digest, "alice", nil)
				if status != http.StatusOK || !strings.Contains(string(body), `"bundle"`) {
					t.Errorf("GET bundle %s = %d: %s", a.Digest, status, body)
				}
			}
			if strings.Count(string(raw), `"attested_at"`) != 1 {
				t.Errorf("served members %s", raw)
			}
		})
	}
}
