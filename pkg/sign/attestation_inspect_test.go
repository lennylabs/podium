package sign_test

import (
	"testing"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// Spec: §4.7.11, §7.3.5 — the summary reads the statement and the leaf's
// issuer and identity with no verification, so a bundle no root trusts
// still names its signer, and one outside the format is refused.
func TestInspectAttestationBundle(t *testing.T) {
	h := sh.New(t)
	s, err := sign.InspectAttestationBundle(ghBundle(t, h, sh.WithBadSignature()))
	if err != nil {
		t.Fatalf("InspectAttestationBundle: %v", err)
	}
	if s.Issuer != sh.DefaultIssuer || s.Identity != sh.DefaultSAN || s.Statement.PredicateType != slsaProvenance || len(s.Statement.Subjects) != 2 {
		t.Fatalf("summary = %+v", s)
	}
	actions := h.DSSEBundle(statement(t), sh.WithTimestamp(sh.TSAPrimary), sh.WithLeaf(sh.GitHubActionsLeaf(sharedWorkflow, skillsSource())))
	if s, err := sign.InspectAttestationBundle(actions); err != nil || s.Identity != sharedWorkflow || s.Issuer != sh.GitHubActionsIssuer {
		t.Fatalf("actions summary = %+v, %v", s, err)
	}
	if s, err := sign.InspectAttestationBundle(ghBundle(t, h, sh.WithLeaf(sh.LeafWithoutIssuer()))); err != nil || s.Issuer != "" || s.Identity != sh.DefaultSAN {
		t.Fatalf("no issuer: %+v, %v", s, err)
	}
	if s, err := sign.InspectAttestationBundle(ghBundle(t, h, sh.WithoutCertificate())); err != nil || s.Issuer != "" || s.Identity != "" {
		t.Fatalf("no certificate: %+v, %v", s, err)
	}
	for name, raw := range map[string][]byte{
		"not JSON":       []byte("x"),
		"version 0.1":    ghBundle(t, h, sh.WithMediaType("application/vnd.dev.sigstore.bundle+json;version=0.1")),
		"no statement":   h.DSSEBundle([]byte(`{"_type":"x"}`), sh.WithTimestamp(sh.TSAPrimary)),
		"message signed": ghBundle(t, h, sh.WithMessageSignature()),
	} {
		if _, err := sign.InspectAttestationBundle(raw); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

// Spec: §7.3.5 — an imported bundle's signature is checked under its own
// leaf's key with no trusted root: a valid bundle of a root that nothing
// trusts passes, and a bad signature, a bundle with no certificate, and a
// bundle outside the format are refused.
func TestCheckAttestationSelfSignature(t *testing.T) {
	h := sh.New(t)
	if err := sign.CheckAttestationSelfSignature(ghBundle(t, h)); err != nil {
		t.Fatalf("valid bundle: %v", err)
	}
	for name, c := range map[string]struct {
		raw    []byte
		reason sign.AttestationReason
	}{
		"bad signature":  {ghBundle(t, h, sh.WithBadSignature()), sign.ReasonSignature},
		"no certificate": {ghBundle(t, h, sh.WithoutCertificate()), sign.ReasonChain},
		"not a bundle":   {[]byte("x"), sign.ReasonParse},
	} {
		t.Run(name, func(t *testing.T) {
			wantReason(t, sign.CheckAttestationSelfSignature(c.raw), c.reason)
		})
	}
}
