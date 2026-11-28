package serverboot

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// testPublicKeyPEM returns the PEM block of a fresh key on curve.
func testPublicKeyPEM(t *testing.T, curve elliptic.Curve) string {
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

// yamlBlockScalar indents text as a YAML block scalar under a key at
// indent spaces.
func yamlBlockScalar(text string, indent int) string {
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	b.WriteString("|\n")
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		b.WriteString(pad + line + "\n")
	}
	return b.String()
}

// Spec: §4.7.11 (Key-based signers), §13.12 — a declared layer names a
// key-based signer with a public_key block scalar, which the start seeds as
// written, and a declared key-based signer that names an instance or holds a
// P-384 key refuses the start with config.invalid_layer_trust.
// Matrix: §6.10 (config.invalid_layer_trust)
func TestBootstrapDeclaredLayers_KeySigner(t *testing.T) {
	pub := testPublicKeyPEM(t, elliptic.P256())
	body := trustYAMLLayer("      trust_mode: require\n      trust_requirements:\n        - signers:\n            - public_key: " + yamlBlockScalar(pub, 16))
	c := loadTrustConfig(t, body, "")
	if err := c.validate(); err != nil {
		t.Fatalf("validate = %v", err)
	}
	st := newMemoryStoreWithTenant(t)
	if _, err := bootstrapDeclaredLayers(st, "default", c, nil, nil, false, collocatedVectorIngest{}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	got, err := st.GetLayerConfig(context.Background(), "default", "finance")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.TrustRequirements) != 1 || got.TrustRequirements[0].Signers[0] != (store.TrustSigner{PublicKey: pub}) {
		t.Fatalf("seeded requirements %+v", got.TrustRequirements)
	}
	for name, tc := range map[string]struct{ signer, want string }{
		"instance":   {"            - instance: public-good\n              public_key: " + yamlBlockScalar(pub, 16), "names no instance"},
		"issuer":     {"            - issuer: i\n              public_key: " + yamlBlockScalar(pub, 16), "names no issuer"},
		"P-384":      {"            - public_key: " + yamlBlockScalar(testPublicKeyPEM(t, elliptic.P384()), 16), "P-384"},
		"misspelled": {"            - public_keys: " + yamlBlockScalar(pub, 16), `holds the key "public_keys"`},
	} {
		t.Run(name, func(t *testing.T) {
			err := loadTrustConfig(t, trustYAMLLayer("      trust_mode: require\n      trust_requirements:\n        - signers:\n"+tc.signer), "").validate()
			if err == nil || !strings.HasPrefix(err.Error(), "config.invalid_layer_trust: declared layer finance: ") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate = %v, want config.invalid_layer_trust naming %q", err, tc.want)
			}
		})
	}
}

// Spec: §13.12 — PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE defaults to 5
// minutes, takes a duration from 0s to 24h with surrounding white space
// removed, and any other value refuses the start with config.invalid; the
// value reaches the ingest configuration and `podium config show`.
// Matrix: §6.10 (config.invalid)
func TestResolveAttestation_SignedTimeAllowance(t *testing.T) {
	t.Parallel()
	now := time.Now()
	if a := resolveAttestation(envMap(nil), yamlSigstore{}, "", now); a.allowance.value != 5*time.Minute || a.allowance.source != "default" || len(a.errs) != 0 {
		t.Fatalf("default = %+v %v", a.allowance, a.errs)
	}
	for raw, want := range map[string]time.Duration{"0": 0, " 0s ": 0, "24h": 24 * time.Hour, "90s": 90 * time.Second} {
		a := resolveAttestation(envMap(map[string]string{signedTimeAllowanceEnv: raw}), yamlSigstore{}, "", now)
		if a.allowance.value != want || len(a.errs) != 0 || a.allowance.source != signedTimeAllowanceEnv {
			t.Errorf("%q: allowance %+v errs %v", raw, a.allowance, a.errs)
		}
	}
	for _, raw := range []string{"24h0m1s", "25h", "-1s", "5", "five minutes"} {
		a := resolveAttestation(envMap(map[string]string{signedTimeAllowanceEnv: raw}), yamlSigstore{}, "", now)
		err := a.err()
		if err == nil || !strings.HasPrefix(err.Error(), "config.invalid: PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE must be a duration from 0s to 24h0m0s") {
			t.Errorf("%q: err = %v", raw, err)
		}
		if a.allowance.display() != strings.TrimSpace(raw)+" (invalid)" {
			t.Errorf("%q: display %q", raw, a.allowance.display())
		}
	}
	c := &Config{attestation: resolveAttestation(envMap(map[string]string{signedTimeAllowanceEnv: "2m"}), yamlSigstore{}, "", now)}
	att, stop := c.ingestAttestation(context.Background())
	defer stop()
	if att.SignedTimeAllowance != 2*time.Minute {
		t.Errorf("ingest allowance %s", att.SignedTimeAllowance)
	}
	found := false
	for _, s := range c.attestationSettings(func(env, src string) string { return env }) {
		if s.Name == "attestation.signed_time_allowance" {
			found = s.Value == "2m0s" && s.Source == signedTimeAllowanceEnv
		}
	}
	if !found {
		t.Error("config show lacks attestation.signed_time_allowance 2m0s")
	}
}

// Spec: §4.7.11 (Trusted root refresh, Key-based signers) — a policy whose
// signers are key-based names no instance, so it starts no refresh of the
// public-good root.
func TestVerifiesOnPublicGood_KeySigners(t *testing.T) {
	t.Parallel()
	key := store.TrustSigner{PublicKey: testPublicKeyPEM(t, elliptic.P256())}
	lc := store.LayerConfig{TrustMode: "require", TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{key}}}}
	if verifiesOnPublicGood(lc, "", "") {
		t.Error("a policy of key-based signers starts the public-good refresh")
	}
	lc.TrustRequirements[0].Signers = append(lc.TrustRequirements[0].Signers, store.TrustSigner{Issuer: "i", Identity: "alice@acme.com"})
	if !verifiesOnPublicGood(lc, "", "") {
		t.Error("a certificate signer of public-good starts no refresh")
	}
}
