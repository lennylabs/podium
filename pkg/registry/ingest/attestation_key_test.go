package ingest_test

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/store"
)

// keySigner returns the key-based signer of key.
func keySigner(t *testing.T, key *ecdsa.PrivateKey) store.TrustSigner {
	t.Helper()
	return store.TrustSigner{PublicKey: sh.PublicKeyPEM(t, key.Public())}
}

// newKeyFixture is the attestation fixture under a policy of one key-based
// signer, with the 5-minute default allowance.
func newKeyFixture(t *testing.T, mode string) (*attestFixture, *ecdsa.PrivateKey) {
	t.Helper()
	f := newAttestFixture(t, mode)
	key := sh.NewSigningKey(t)
	f.setPolicy(mode, keySigner(t, key))
	f.cfg.SignedTimeAllowance = 5 * time.Minute
	return f, key
}

// signKey returns a bundle signed with key over the layer's subjects and the
// signed-time subject of at, which follows the layer subject as `podium
// attest --signed-time` writes it.
func (f *attestFixture) signKey(key *ecdsa.PrivateKey, at time.Time, opts ...sh.BundleOpt) []byte {
	f.t.Helper()
	subs := f.subjects()
	all := append([]sh.Subject{subs[0], sh.SignedTimeSubject(at, subs[0].SHA256)}, subs[1:]...)
	return f.h.DSSEBundle(sh.Statement(f.t, slsaType, all...), append([]sh.BundleOpt{sh.WithSigningKey(key)}, opts...)...)
}

// now is the fixture's registry clock.
func (f *attestFixture) now() time.Time { return f.clock.Now().UTC() }

// Spec: §4.7.11 (Key-based signers, Layer step, Attestation storage) — a
// key-signed bundle with no timestamp passes the layer step, its signed
// time becomes the mark, and its links and stored bundle carry the empty
// instance and issuer and the key's identifier, with no log index.
func TestKeySignedAttestation_PassesWithItsSignedTime(t *testing.T) {
	f, key := newKeyFixture(t, "require")
	signedAt := f.now().Add(-time.Minute)
	raw := f.signKey(key, signedAt)
	f.place(raw)
	f.mustCycle()
	rec := f.record()
	if rec.Status != "ok" || rec.Mark == nil || !rec.Mark.Equal(signedAt) || rec.Commit != "" {
		t.Fatalf("record %+v, want status ok and the mark %s", rec, signedAt)
	}
	id := sh.KeyIdentifier(t, key.Public())
	d := short(attest.AttestationDigest(raw))
	want(t, "links", f.links(),
		"finance/close /"+id+" "+d+" verified keyed=true",
		"finance/pay /"+id+" "+d+" verified keyed=true")
	stored, err := f.st.GetAttestation(context.Background(), "tenant-1", attest.AttestationDigest(raw))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Instance != "" || stored.SignerIssuer != "" || stored.SignerIdentity != id || stored.LogIndex != nil ||
		!stored.AttestedAt.Equal(signedAt) || stored.SourceRepository != "" {
		t.Errorf("stored bundle %+v", stored)
	}
	ev, ok := f.lastEvent("layer.attestation_verified")
	if !ok {
		t.Fatal("no layer.attestation_verified event")
	}
	var bundles []map[string]any
	if err := json.Unmarshal([]byte(ev.fields["bundles"]), &bundles); err != nil || len(bundles) != 1 {
		t.Fatalf("bundles %s: %v", ev.fields["bundles"], err)
	}
	if b := bundles[0]; b["identity"] != id || b["issuer"] != "" || b["instance"] != "" || b["log_index"] != float64(-1) {
		t.Errorf("event bundle %v", b)
	}
	if got := f.outcome("finance/pay"); got != "verified" {
		t.Errorf("outcome %s", got)
	}
}

// Spec: §4.7.11 (Layer step), §13.12 — a key-signed candidate passes when
// its signed time is not later than the registry's clock plus the
// allowance, and is otherwise ingest.attestation_stale with the constraint
// signed_time_ahead, whose details hold the signed time, the registry's
// time, and the allowance.
func TestKeySignedAttestation_SignedTimeBound(t *testing.T) {
	for _, tc := range []struct {
		name      string
		allowance time.Duration
		ahead     time.Duration
		pass      bool
	}{
		{"inside the allowance", 5 * time.Minute, 4 * time.Minute, true},
		{"at the allowance", 5 * time.Minute, 5 * time.Minute, true},
		{"outside the allowance", 5 * time.Minute, 6 * time.Minute, false},
		{"no allowance, at the clock", 0, 0, true},
		{"no allowance, a second ahead", 0, time.Second, false},
		{"a negative allowance reads as none", -time.Hour, time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, key := newKeyFixture(t, "require")
			f.cfg.SignedTimeAllowance = tc.allowance
			f.place(f.signKey(key, f.now().Add(tc.ahead)))
			_, err := f.cycle()
			if tc.pass {
				if err != nil {
					t.Fatalf("cycle: %v", err)
				}
				return
			}
			code, details := refusal(t, err)
			if code != "ingest.attestation_stale" || details["constraint"] != "signed_time_ahead" {
				t.Fatalf("refusal %s %v", code, details)
			}
			if details["signed_at"] != f.now().Add(tc.ahead).Format(time.RFC3339Nano) ||
				details["registry_time"] != f.now().Format(time.RFC3339Nano) || details["allowance"] == nil {
				t.Errorf("details %v", details)
			}
			if r := f.record(); r.Mark != nil || r.Status != "ingest.attestation_stale" {
				t.Errorf("record %+v", r)
			}
		})
	}
}

// Spec: §4.7.11 (Layer step) — the candidate with the latest signed time
// reports its failure, and an earlier candidate inside the allowance
// passes, so a bundle ahead of the clock does not refuse a cycle that also
// holds a valid one.
func TestKeySignedAttestation_AheadCandidateBesideAValidOne(t *testing.T) {
	f, key := newKeyFixture(t, "require")
	valid := f.now().Add(-time.Minute)
	f.place(f.signKey(key, valid), f.signKey(key, f.now().Add(time.Hour)))
	f.mustCycle()
	if r := f.record(); r.Mark == nil || !r.Mark.Equal(valid) {
		t.Errorf("mark %v, want %s", r.Mark, valid)
	}
}

// Spec: §4.7.11 (Key-based signers, Refusal reasons) — a key-signed bundle
// that breaks a rule is no accepted bundle, and the layer step reports it
// with its refusal reason.
func TestKeySignedAttestation_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bundle func(f *attestFixture, key *ecdsa.PrivateKey) []byte
		reason string
	}{
		{"no signed-time subject", func(f *attestFixture, key *ecdsa.PrivateKey) []byte {
			return f.h.DSSEBundle(sh.Statement(f.t, slsaType, f.subjects()...), sh.WithSigningKey(key))
		}, "reserved_subject"},
		{"two signed-time subjects", func(f *attestFixture, key *ecdsa.PrivateKey) []byte {
			subs := f.subjects()
			all := append([]sh.Subject{subs[0], sh.SignedTimeSubject(f.now(), subs[0].SHA256), sh.SignedTimeSubject(f.now().Add(-time.Second), subs[0].SHA256)}, subs[1:]...)
			return f.h.DSSEBundle(sh.Statement(f.t, slsaType, all...), sh.WithSigningKey(key))
		}, "reserved_subject"},
		{"a log entry", func(f *attestFixture, key *ecdsa.PrivateKey) []byte {
			return f.signKey(key, f.now(), sh.WithRekorV1Entry())
		}, "log_entry"},
		{"another key's signature", func(f *attestFixture, _ *ecdsa.PrivateKey) []byte {
			return f.signKey(sh.NewSigningKey(f.t), f.now())
		}, "signature"},
		{"another key's signature naming the signer's key", func(f *attestFixture, key *ecdsa.PrivateKey) []byte {
			id := sh.KeyIdentifier(f.t, key.Public())
			return f.signKey(sh.NewSigningKey(f.t), f.now(), sh.WithKeyID(id), sh.WithKeyHint(id))
		}, "signature"},
		{"a certificate-signed bundle", func(f *attestFixture, _ *ecdsa.PrivateKey) []byte {
			return f.sign(f.subjects(), time.Minute)
		}, "signature"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, key := newKeyFixture(t, "require")
			f.place(tc.bundle(f, key))
			_, err := f.cycle()
			code, details := refusal(t, err)
			if code != "ingest.attestation_missing" || details["constraint"] != "no_accepted_bundle" {
				t.Fatalf("refusal %s %v", code, details)
			}
			bundles, _ := details["bundles"].([]map[string]any)
			if len(bundles) != 1 || bundles[0]["reason"] != tc.reason {
				t.Errorf("bundles %v, want the reason %s", bundles, tc.reason)
			}
		})
	}
}

// Spec: §4.7.11 (Key-based signers) — the keyid and the public key hint lie
// outside the signed bytes and select nothing, and a timestamp the bundle
// carries, one that does not verify included, changes nothing.
func TestKeySignedAttestation_HintsAndTimestampsIgnored(t *testing.T) {
	f, key := newKeyFixture(t, "require")
	other := sh.NewSigningKey(t)
	id := sh.KeyIdentifier(t, other.Public())
	f.place(f.signKey(key, f.now(), sh.WithKeyID(id), sh.WithKeyHint(id),
		sh.WithTimestamp(sh.TSAPrimary, sh.WithCorruptTimestamp())))
	f.mustCycle()
	if r := f.record(); r.Status != "ok" || !r.Mark.Equal(f.now()) {
		t.Errorf("record %+v: the timestamp moved the attested time or refused the bundle", r)
	}
}

// Spec: §4.7.11 (Attestation mark, Key-based signers) — a replayed older
// key-signed state carries a signed time earlier than the mark and is
// refused with the constraint mark.
func TestKeySignedAttestation_ReplayRefusedByTheMark(t *testing.T) {
	f, key := newKeyFixture(t, "require")
	older := f.signKey(key, f.now().Add(-10*time.Minute))
	f.place(older)
	f.mustCycle()
	f.write(map[string]string{"finance/plan/ARTIFACT.md": agentArtifact("Plan the quarter")})
	f.place(f.signKey(key, f.now().Add(-5*time.Minute)))
	f.mustCycle()
	if err := os.RemoveAll(filepath.Join(f.dir, "finance", "plan")); err != nil {
		t.Fatal(err)
	}
	f.place(older)
	_, err := f.cycle()
	code, details := refusal(t, err)
	if code != "ingest.attestation_stale" || details["constraint"] != "mark" {
		t.Fatalf("refusal %s %v", code, details)
	}
}

// Spec: §4.7.11 (Key-based signers, Attestation storage) — two keys that
// sign one predicate type have separate links, which a rotation with an
// overlap needs, and a certificate signer and a key-based signer can share a
// requirement.
func TestKeySignedAttestation_RotationAndMixedSigners(t *testing.T) {
	f, oldKey := newKeyFixture(t, "require")
	newKey := sh.NewSigningKey(t)
	f.setPolicy("require", emailSigner(), keySigner(t, oldKey), keySigner(t, newKey))
	a, b := f.signKey(oldKey, f.now().Add(-2*time.Minute)), f.signKey(newKey, f.now().Add(-time.Minute))
	f.place(a, b)
	f.mustCycle()
	oldID, newID := sh.KeyIdentifier(t, oldKey.Public()), sh.KeyIdentifier(t, newKey.Public())
	got := strings.Join(f.links(), "\n")
	for _, line := range []string{
		"finance/pay /" + oldID + " " + short(attest.AttestationDigest(a)) + " verified keyed=true",
		"finance/pay /" + newID + " " + short(attest.AttestationDigest(b)) + " verified keyed=true",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("links lack %q:\n%s", line, got)
		}
	}
	if r := f.record(); !r.Mark.Equal(f.now().Add(-time.Minute)) {
		t.Errorf("mark %v, want the latest signed time", r.Mark)
	}
}

// Spec: §4.7.11 (Re-evaluation, Key-based signers) — a process behind a
// root record verifies a key-signed link in full: it keeps the link of a key
// the policy still lists and downgrades the link of a removed key, whose
// version then fails with link_downgraded.
func TestKeySignedAttestation_ReevaluationBehindTheRootRecord(t *testing.T) {
	f, key := newKeyFixture(t, "require")
	f.setPolicy("require", emailSigner(), keySigner(t, key))
	f.place(f.signKey(key, f.now().Add(-time.Minute)), f.sign(f.subjects(), time.Minute))
	f.mustCycle()
	ctx := context.Background()
	roots, _ := f.st.ListRootRecords(ctx)
	if len(roots) != 1 {
		t.Fatalf("root records %+v", roots)
	}
	newer := store.RootRecord{Instance: "github", RootDigest: "sha256:newer", RootTime: f.h.Clock().Add(time.Hour)}
	if ok, err := f.st.ReplaceRootRecord(ctx, &roots[0], newer); !ok || err != nil {
		t.Fatalf("replace: %v %v", ok, err)
	}
	id := sh.KeyIdentifier(t, key.Public())
	// The key stays: the link verifies under it and keeps its recorded key.
	f.setPolicy("require", keySigner(t, key), emailSigner())
	f.reevaluate()
	for _, l := range f.links() {
		if strings.Contains(l, id) && !strings.HasSuffix(l, " verified keyed=true") {
			t.Errorf("a kept key's link changed: %s", l)
		}
	}
	// The key leaves: a process behind the record still decides the link.
	f.setPolicy("require", emailSigner())
	f.reevaluate()
	for _, l := range f.links() {
		switch {
		case strings.Contains(l, id) && !strings.HasSuffix(l, " unverified keyed=false"):
			t.Errorf("a removed key's link stayed: %s", l)
		case !strings.Contains(l, id) && !strings.HasSuffix(l, " verified keyed=true"):
			t.Errorf("a certificate link changed: %s", l)
		}
	}
	if got := f.outcome("finance/pay"); got != "verified" {
		t.Errorf("outcome %s; the certificate link still covers the requirement", got)
	}
	// With the key alone and its link gone, the version fails.
	f.setPolicy("require", keySigner(t, sh.NewSigningKey(t)))
	f.reevaluate()
	if got := f.outcome("finance/pay"); got != "failed ingest.attestation_missing/artifact/link_downgraded/0" {
		t.Errorf("outcome %s", got)
	}
}

// Spec: §4.7.11 (Re-evaluation) — a version that lost one link keeps its
// outcome when a key-signed link that the re-evaluation did not select
// still covers the requirement, because that link is verified in full.
func TestKeySignedAttestation_RemainingKeyLinkCovers(t *testing.T) {
	f, key := newKeyFixture(t, "require")
	other := store.TrustSigner{Issuer: sh.DefaultIssuer, Identity: "dave@acme.com", Instance: "github"}
	f.setPolicy("require", keySigner(t, key), emailSigner())
	f.place(f.signKey(key, f.now().Add(-time.Minute)), f.sign(f.subjects(), time.Minute))
	f.mustCycle()
	f.setPolicy("require", keySigner(t, key), other)
	f.reevaluate()
	if got := f.outcome("finance/pay"); got != "verified" {
		t.Errorf("outcome %s; the key link still covers the requirement", got)
	}
}

// Spec: §4.7.11 (Unverified links) — a cycle under none links no
// key-signed bundle, because it meets the conditions under no instance.
func TestKeySignedAttestation_NoneLinksNoKeySignedBundle(t *testing.T) {
	f, key := newKeyFixture(t, "none")
	f.place(f.signKey(key, f.now()))
	f.mustCycle()
	if got := f.links(); len(got) != 0 {
		t.Errorf("links under none %v", got)
	}
}

// Spec: §4.7.11 (Policy that became invalid) — a stored key that the rules
// refuse, which only a store edited by hand holds, refuses each cycle as
// policy_rule.
func TestKeySignedAttestation_InvalidStoredKey(t *testing.T) {
	f, _ := newKeyFixture(t, "require")
	f.setPolicy("require", store.TrustSigner{PublicKey: "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n"})
	f.signLayer(time.Minute)
	_, err := f.cycle()
	code, details := refusal(t, err)
	if code != "ingest.attestation_policy_invalid" || details["constraint"] != "policy_rule" {
		t.Fatalf("refusal %s %v", code, details)
	}
}

// Spec: §4.7.11 (Layer step, Key-based signers) — a key-signed bundle that
// verified under the signer's key and carries another predicate type is
// reported with the reason predicate_type and the identifier of the key.
func TestKeySignedAttestation_PredicateTypeReportsTheKey(t *testing.T) {
	f, key := newKeyFixture(t, "require")
	subs := f.subjects()
	all := append([]sh.Subject{subs[0], sh.SignedTimeSubject(f.now(), subs[0].SHA256)}, subs[1:]...)
	f.place(f.h.DSSEBundle(sh.Statement(t, "https://acme.com/other/v1", all...), sh.WithSigningKey(key)))
	_, err := f.cycle()
	_, details := refusal(t, err)
	bundles, _ := details["bundles"].([]map[string]any)
	if len(bundles) != 1 || bundles[0]["reason"] != "predicate_type" || bundles[0]["identity"] != sh.KeyIdentifier(t, key.Public()) {
		t.Errorf("bundles %v", bundles)
	}
	if _, ok := bundles[0]["instance"]; ok {
		t.Errorf("a key-signed entry names an instance: %v", bundles[0])
	}
}
