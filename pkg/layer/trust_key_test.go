package layer_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// publicKeyPEM returns a fresh public key of the curve as a PEM block, as
// cosign.pub holds it.
func publicKeyPEM(t *testing.T, curve elliptic.Curve) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// keySigner is a key-based signer of a fresh P-256 key.
func keySigner(t *testing.T) store.TrustSigner {
	return store.TrustSigner{PublicKey: publicKeyPEM(t, elliptic.P256())}
}

// Spec: §4.7.11 (Key-based signers, Policy rules) — a key-based signer names
// a P-256 public_key and no other member; an instance, an issuer, an
// identity, a binding, another curve, an RSA key, and a value with a second
// block or other text are refused where the policy is loaded.
func TestCheckTrustPolicy_KeySigners(t *testing.T) {
	t.Parallel()
	ks := keySigner(t)
	if err := layer.CheckTrustPolicy(policy("require", ks), nil, ""); err != nil {
		t.Fatalf("a key-based signer: %v", err)
	}
	mixed := policy("require", boundSigner(), ks)
	if err := layer.CheckTrustPolicy(mixed, nil, ""); err != nil {
		t.Fatalf("a requirement with a certificate signer and a key-based signer: %v", err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(rsaKey.Public())
	rsaPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	for name, tc := range map[string]struct {
		mut  func(*store.TrustSigner)
		text string
	}{
		"instance":      {func(s *store.TrustSigner) { s.Instance = "github" }, "names no instance"},
		"public-good":   {func(s *store.TrustSigner) { s.Instance = "public-good" }, "names no instance"},
		"issuer":        {func(s *store.TrustSigner) { s.Issuer = ghIssuer }, "names no issuer"},
		"identity":      {func(s *store.TrustSigner) { s.Identity = "alice@acme.com" }, "names no issuer"},
		"binding":       {func(s *store.TrustSigner) { s.SourceRepositoryOwnerID = "42" }, "names no issuer"},
		"any_source":    {func(s *store.TrustSigner) { s.AnySource = true }, "names no issuer"},
		"ref":           {func(s *store.TrustSigner) { s.SourceRepositoryRef = "refs/heads/main" }, "names no issuer"},
		"any_ref":       {func(s *store.TrustSigner) { s.AnyRef = true }, "names no issuer"},
		"P-384":         {func(s *store.TrustSigner) { s.PublicKey = publicKeyPEM(t, elliptic.P384()) }, "P-384"},
		"RSA":           {func(s *store.TrustSigner) { s.PublicKey = rsaPEM }, "RSA"},
		"second block":  {func(s *store.TrustSigner) { s.PublicKey += s.PublicKey }, "second block"},
		"trailing data": {func(s *store.TrustSigner) { s.PublicKey += "x" }, "after the PEM block"},
	} {
		s := ks
		tc.mut(&s)
		err := layer.CheckTrustPolicy(policy("require", s), nil, "")
		if !errors.Is(err, layer.ErrTrustPolicy) || !strings.Contains(err.Error(), tc.text) {
			t.Errorf("%s: CheckTrustPolicy = %v, want a refusal naming %q", name, err, tc.text)
		}
	}
}

// Spec: §4.7.11 (Sigstore instances, Key-based signers) — a key-based signer
// names no instance, and a certificate signer with no instance names
// public-good.
func TestSignerInstance(t *testing.T) {
	t.Parallel()
	if got := layer.SignerInstance(keySigner(t)); got != "" {
		t.Errorf("key-based signer instance %q", got)
	}
	if got := layer.SignerInstance(signer()); got != "public-good" {
		t.Errorf("certificate signer instance %q", got)
	}
	gh := signer()
	gh.Instance = "github"
	if got := layer.SignerInstance(gh); got != "github" {
		t.Errorf("github signer instance %q", got)
	}
	ks := keySigner(t)
	if v := layer.VerifierSigner(ks); v.PublicKey != ks.PublicKey || v.Issuer != "" {
		t.Errorf("VerifierSigner = %+v", v)
	}
	if layer.TrustPolicyEqual(policy("require", ks), policy("require", keySigner(t))) {
		t.Error("two policies of different keys compare equal")
	}
}

// Spec: §4.7.11 (Tenant trust floor, Key-based signers) — a floor entry
// names an issuer, so it matches no key-based signer, and a layer under a
// floor whose signer list is not empty cannot name one.
func TestCheckFloor_KeySigner(t *testing.T) {
	t.Parallel()
	ks := keySigner(t)
	entry := store.TrustFloorSigner{Issuer: ghIssuer}
	if layer.FloorSignerMatches(ks, entry) {
		t.Error("a key-based signer matched a floor entry")
	}
	r := layer.CheckFloor(policy("require", boundSigner(), ks), store.TrustFloor{Signers: []store.TrustFloorSigner{entry}}, "")
	if r == nil || r.Constraint != layer.ConstraintSignerOutsideFloor || !strings.Contains(r.Message, "key-based signer") {
		t.Errorf("CheckFloor = %v, want signer_outside_floor naming the key-based signer", r)
	}
	if r := layer.CheckFloor(policy("require", ks), store.TrustFloor{Mode: "require"}, ""); r != nil {
		t.Errorf("a floor with no signer list refused a key-based signer: %v", r)
	}
}

// Spec: §4.7.11 (Requirements file, Key-based signers) — a requirements file
// carries a key-based signer's public_key, with its line breaks as \n, and
// reads back to the same requirement.
func TestRequirementsFile_KeySigner(t *testing.T) {
	t.Parallel()
	reqs := []store.TrustRequirement{{Signers: []store.TrustSigner{keySigner(t)}}}
	b, err := layer.MarshalRequirementsFile(reqs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"public_key": "-----BEGIN PUBLIC KEY-----\n`) {
		t.Fatalf("file %s", b)
	}
	file, err := layer.ParseRequirementsFile(b)
	if err != nil {
		t.Fatalf("ParseRequirementsFile: %v", err)
	}
	if file.TrustRequirements[0].Signers[0] != reqs[0].Signers[0] {
		t.Errorf("round trip %+v", file.TrustRequirements)
	}
	if err := layer.CheckRequirements(file.TrustRequirements, nil); err != nil {
		t.Errorf("CheckRequirements: %v", err)
	}
}
