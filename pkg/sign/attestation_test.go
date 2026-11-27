package sign_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// The identity values of the GitHub Actions fixtures: a shared signing
// workflow in acme/workflows called by acme/skills on main.
const (
	sharedWorkflow = "https://github.com/acme/workflows/.github/workflows/attest.yml@refs/heads/main"
	skillsRepo     = "https://github.com/acme/skills"
	skillsRepoID   = "101"
	acmeOwner      = "https://github.com/acme"
	acmeOwnerID    = "201"
	mainRef        = "refs/heads/main"
	sourceCommit   = "0123456789abcdef0123456789abcdef01234567"
	slsaProvenance = "https://slsa.dev/provenance/v1"
	layerSubject   = ".podium/layer"
	digestA        = "9f2c000000000000000000000000000000000000000000000000000000000001"
)

// skillsSource is the source of a run of acme/skills on main.
func skillsSource() sh.Source {
	return sh.Source{
		Repository: skillsRepo, Digest: sourceCommit, Ref: mainRef,
		ID: skillsRepoID, Owner: acmeOwner, OwnerID: acmeOwnerID,
	}
}

// actionsSigner is a signer bound to acme/skills on main by identifier.
func actionsSigner() sign.AttestationSigner {
	return sign.AttestationSigner{
		Issuer: sh.GitHubActionsIssuer, Identity: sharedWorkflow,
		SourceRepositoryID: skillsRepoID, SourceRepositoryRef: mainRef,
	}
}

// emailSigner matches the default harness leaf.
func emailSigner() sign.AttestationSigner {
	return sign.AttestationSigner{Issuer: sh.DefaultIssuer, Identity: sh.DefaultSAN}
}

// statement is a statement of the layer subject and one artifact.
func statement(t testing.TB) []byte {
	return sh.Statement(t, slsaProvenance,
		sh.Subject{Name: layerSubject, SHA256: digestA},
		sh.Subject{Name: "finance/close-books@1.4.0", SHA256: digestA})
}

// publicGood is an instance with a transparency log and certificate
// transparency, whose root lists every harness authority but the SHA-1 one.
func publicGood(t testing.TB, h *sh.Harness, opts ...sh.RootOpt) sign.AttestationInstance {
	t.Helper()
	opts = append([]sh.RootOpt{sh.WithCTLogKey(), sh.WithTimestampAuthorities(sh.TSASecond, sh.TSASameURI, sh.TSARSA)}, opts...)
	return sign.AttestationInstance{
		Name: "public-good", Root: mustRoot(t, h.TrustedRootJSON(opts...)),
		TransparencyLog: true, CertificateTransparency: true,
	}
}

// githubInstance is a timestamp-only instance whose root also lists an
// expired authority, as the root of GitHub's instance does.
func githubInstance(t testing.TB, h *sh.Harness) sign.AttestationInstance {
	t.Helper()
	opts := append(sh.GitHubStyleRoot(), sh.WithTimestampAuthorities(sh.TSASecond, sh.TSARSA))
	return sign.AttestationInstance{Name: "github", Root: mustRoot(t, h.TrustedRootJSON(opts...))}
}

func mustRoot(t testing.TB, raw []byte) *sign.AttestationRoot {
	t.Helper()
	r, err := sign.ParseAttestationRoot(raw)
	if err != nil {
		t.Fatalf("ParseAttestationRoot: %v", err)
	}
	return r
}

// pgBundle is a public-good bundle: an SCT and a Rekor v1 entry with a
// signed entry timestamp and an inclusion proof.
func pgBundle(t testing.TB, h *sh.Harness, opts ...sh.BundleOpt) []byte {
	t.Helper()
	return h.DSSEBundle(statement(t), append([]sh.BundleOpt{sh.WithSCT(), sh.WithRekorV1Entry()}, opts...)...)
}

// ghBundle is a GitHub-instance bundle: one RFC 3161 timestamp, no log
// entry, and no SCT.
func ghBundle(t testing.TB, h *sh.Harness, opts ...sh.BundleOpt) []byte {
	t.Helper()
	return h.DSSEBundle(statement(t), append([]sh.BundleOpt{sh.WithTimestamp(sh.TSAPrimary)}, opts...)...)
}

// wantReason asserts err is a refusal with reason.
func wantReason(t testing.TB, err error, reason sign.AttestationReason) {
	t.Helper()
	got, ok := sign.AttestationReasonOf(err)
	if !ok {
		t.Fatalf("err = %v, want a refusal with reason %s", err, reason)
	}
	if got != reason {
		t.Fatalf("reason = %s, want %s (err: %v)", got, reason, err)
	}
	if !errors.Is(err, sign.ErrAttestationRefused) {
		t.Fatalf("refusal does not wrap ErrAttestationRefused")
	}
}

// verify runs VerifyAttestationBundle and, when it accepts, the signer.
func verify(t testing.TB, inst sign.AttestationInstance, raw []byte, s sign.AttestationSigner) (*sign.VerifiedAttestation, error) {
	t.Helper()
	v, err := sign.VerifyAttestationBundle(context.Background(), raw, inst)
	if err != nil {
		return nil, err
	}
	return v, v.AcceptsSigner(s)
}

// TestVerifyAttestation_AcceptsEachInstanceKind verifies a public-good
// bundle and a GitHub-instance bundle, and the fields each yields.
//
// Spec: §4.7.11
func TestVerifyAttestation_AcceptsEachInstanceKind(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	leaf := sh.WithLeaf(sh.GitHubActionsLeaf(sharedWorkflow, skillsSource()))
	for _, tc := range []struct {
		name string
		inst sign.AttestationInstance
		raw  []byte
	}{
		{"public-good", publicGood(t, h), pgBundle(t, h, leaf)},
		{"github", githubInstance(t, h), ghBundle(t, h, leaf)},
		{"legacy media type", githubInstance(t, h), ghBundle(t, h, leaf, sh.WithMediaType(sh.BundleMediaTypeV03Legacy))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := verify(t, tc.inst, tc.raw, actionsSigner())
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if !v.AttestedTime.Equal(h.Clock()) || len(v.VerifiedTimes) != 1 {
				t.Errorf("times = %v, attested %v; want one time at %v", v.VerifiedTimes, v.AttestedTime, h.Clock())
			}
			if v.Instance != tc.inst.Name || v.Statement.PredicateType != slsaProvenance || len(v.Statement.Subjects) != 2 {
				t.Errorf("result = %+v", v)
			}
			c := v.Certificate
			if c.Issuer.Value != sh.GitHubActionsIssuer || c.Identity.Value != sharedWorkflow || !c.IdentityIsURI ||
				c.SourceRepositoryDigest.Value != sourceCommit || c.SourceRepositoryOwner.Value != acmeOwner {
				t.Errorf("certificate fields = %+v", c)
			}
		})
	}
}

// TestVerifyAttestation_BundleAcceptance covers the bundle conditions of
// §4.7.11: each case builds a bundle that breaks one condition and checks
// the reason, or builds one the conditions accept.
//
// Spec: §4.7.11
func TestVerifyAttestation_BundleAcceptance(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	pg, gh := publicGood(t, h), githubInstance(t, h)
	later := h.Clock().Add(5 * time.Minute)
	// ctByRoot lists CT logs and leaves the flag at its zero value; ctByFlag
	// sets the flag on a root that lists none.
	ctByRoot := pg
	ctByRoot.CertificateTransparency = false
	ctByFlag := gh
	ctByFlag.CertificateTransparency = true
	for _, tc := range []struct {
		name string
		inst sign.AttestationInstance
		raw  []byte
		want sign.AttestationReason
	}{
		{"bad signature", pg, pgBundle(t, h, sh.WithBadSignature()), sign.ReasonSignature},
		{"bad signature without a log", gh, ghBundle(t, h, sh.WithBadSignature()), sign.ReasonSignature},
		{"version 0.1 with a promise and no proof", pg, pgBundle(t, h,
			sh.WithMediaType(sh.BundleMediaTypeV01)), sign.ReasonMediaType},
		{"version 0.1 promise only", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithMediaType(sh.BundleMediaTypeV01), sh.WithRekorV1Entry(sh.WithoutRekorProof())), sign.ReasonMediaType},
		{"two envelope signatures", gh, ghBundle(t, h, sh.WithSecondSignature()), sign.ReasonPayload},
		{"payload type", gh, ghBundle(t, h, sh.WithPayloadType("application/json")), sign.ReasonPayload},
		{"message signature", gh, ghBundle(t, h, sh.WithMessageSignature()), sign.ReasonPayload},
		{"signed-at subject", gh, h.DSSEBundle(sh.Statement(t, slsaProvenance,
			sh.Subject{Name: layerSubject, SHA256: digestA},
			sh.Subject{Name: ".podium/signed-at/1768478400", SHA256: digestA}),
			sh.WithTimestamp(sh.TSAPrimary)), sign.ReasonReservedSubject},
		{"timestamp that does not verify beside one that does", gh, ghBundle(t, h,
			sh.WithTimestamp(sh.TSASecond, sh.WithCorruptTimestamp())), sign.ReasonTimestampCondition},
		{"timestamp from an authority outside the root", gh, ghBundle(t, h,
			sh.WithTimestamp(sh.TSASHA1)), sign.ReasonTimestampCondition},
		{"two timestamps from one authority, later second", gh, ghBundle(t, h,
			sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(later))), sign.ReasonTimestampCondition},
		{"two timestamps from one authority, later first", gh, h.DSSEBundle(statement(t),
			sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(later)), sh.WithTimestamp(sh.TSAPrimary)), sign.ReasonTimestampCondition},
		{"two authority entries that share a URI", pg, pgBundle(t, h,
			sh.WithTimestamp(sh.TSAPrimary), sh.WithTimestamp(sh.TSASameURI)), sign.ReasonTimestampCondition},
		{"two log entries", pg, pgBundle(t, h, sh.WithRekorV1Entry(sh.WithRekorIndex(9))), sign.ReasonTimestampCondition},
		{"log entry where no log is required", gh, ghBundle(t, h, sh.WithRekorV1Entry()), sign.ReasonTimestampCondition},
		{"timestamp token outside a response", gh, h.DSSEBundle(statement(t),
			sh.WithTimestamp(sh.TSAPrimary, sh.WithBareToken())), sign.ReasonTimestampCondition},
		{"timestamp embedding two certificates", gh, h.DSSEBundle(statement(t),
			sh.WithTimestamp(sh.TSAPrimary, sh.WithEmbeddedChain())), sign.ReasonTimestampCondition},
		{"no time at all", gh, h.DSSEBundle(statement(t)), sign.ReasonTime},
		{"inclusion proof without a signed entry timestamp", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithRekorV1Entry(sh.WithoutSET())), sign.ReasonTime},
		{"verified time outside the certificate's validity", gh, h.DSSEBundle(statement(t),
			sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(h.Clock().Add(time.Hour)))), sign.ReasonTime},
		{"no leaf certificate", gh, ghBundle(t, h, sh.WithoutCertificate()), sign.ReasonChain},
		{"leaf from a foreign authority", publicGood(t, h, sh.WithForeignCA()), pgBundle(t, h), sign.ReasonChain},
		{"leaf without code signing", gh, ghBundle(t, h, sh.WithLeaf(sh.LeafWithoutCodeSigning())), sign.ReasonChain},
		{"leaf with no extended key usage", gh, ghBundle(t, h, sh.WithLeaf(sh.LeafWithoutEKU())), sign.ReasonChain},
		{"leaf with only the any-purpose usage", gh, ghBundle(t, h, sh.WithLeaf(sh.LeafAnyUsageOnly())), sign.ReasonChain},
		{"leaf with no extended key usage, log instance", pg, pgBundle(t, h, sh.WithLeaf(sh.LeafWithoutEKU())), sign.ReasonChain},
		{"leaf with only the any-purpose usage, log instance", pg, pgBundle(t, h, sh.WithLeaf(sh.LeafAnyUsageOnly())), sign.ReasonChain},
		{"root lists CT logs and the flag is off", ctByRoot, h.DSSEBundle(statement(t), sh.WithRekorV1Entry()), sign.ReasonCertificateTimestamp},
		{"flag requires CT and the root lists none", ctByFlag, ghBundle(t, h, sh.WithSCT()), sign.ReasonCertificateTimestamp},
		{"media type under the protobuf field name", gh, ghBundle(t, h, sh.WithMediaTypeMember("media_type")), sign.ReasonMediaType},
		{"media type that is not a string", gh, []byte(`{"mediaType":3}`), sign.ReasonMediaType},
		{"bundle with a byte order mark", gh, append([]byte("\xef\xbb\xbf"), ghBundle(t, h)...), sign.ReasonParse},
		{"no SCT where the instance has CT logs", pg, h.DSSEBundle(statement(t), sh.WithRekorV1Entry()), sign.ReasonCertificateTimestamp},
		{"SCT from a foreign log", pg, h.DSSEBundle(statement(t), sh.WithForeignSCT(), sh.WithRekorV1Entry()), sign.ReasonCertificateTimestamp},
		{"no log entry where a log is required", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithTimestamp(sh.TSAPrimary)), sign.ReasonLogEntry},
		{"log entry from a foreign log key", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithTimestamp(sh.TSAPrimary), sh.WithRekorV1Entry(sh.WithForeignLogKey())), sign.ReasonLogEntry},
		{"log entry with a promise that does not verify", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithTimestamp(sh.TSAPrimary), sh.WithRekorV1Entry(sh.WithBadSET())), sign.ReasonLogEntry},
		{"log entry relabeled as intoto", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithRekorV1Entry(sh.WithKindVersionLabel("intoto", "0.0.2"))), sign.ReasonLogEntry},
		{"log entry relabeled with another version", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithRekorV1Entry(sh.WithKindVersionLabel("dsse", "0.0.2"))), sign.ReasonLogEntry},
		{"v2 body for another digest under a v1 checkpoint", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithTimestamp(sh.TSAPrimary), sh.WithRekorV1Entry(sh.WithRekorV2BodyForOther(), sh.WithV1Checkpoint())), sign.ReasonLogEntry},
		{"v2 body for the bundle under a v1 checkpoint", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithTimestamp(sh.TSAPrimary), sh.WithRekorV1Entry(sh.WithRekorV2Body(), sh.WithV1Checkpoint())), sign.ReasonLogEntry},
		{"v2 body for another digest under a v2 checkpoint", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithTimestamp(sh.TSAPrimary), sh.WithRekorV1Entry(sh.WithRekorV2BodyForOther())), sign.ReasonLogEntry},
		{"v2 checkpoint from another host", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithTimestamp(sh.TSAPrimary), sh.WithRekorV1Entry(sh.WithRekorV2Body(), sh.WithCheckpointOrigin("rekor.other.test"))), sign.ReasonLogEntry},
		{"v2 checkpoint with a v1 origin form", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithTimestamp(sh.TSAPrimary), sh.WithRekorV1Entry(sh.WithRekorV2Body(), sh.WithCheckpointOrigin("log.acme.test - 1"))), sign.ReasonLogEntry},
		{"v2 body that names another kind", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithTimestamp(sh.TSAPrimary), sh.WithRekorV1Entry(sh.WithRekorV2Body(), sh.WithV2BodyKind("dsse"))), sign.ReasonLogEntry},
		{"log entry from a log the root does not list", publicGood(t, h, sh.WithoutTLogs()), h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithTimestamp(sh.TSAPrimary), sh.WithRekorV1Entry(sh.WithoutSET())), sign.ReasonLogEntry},
		{"v1 body under a v2 checkpoint", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithRekorV1Entry(sh.WithV2Checkpoint())), sign.ReasonLogEntry},
		{"version 0.3 log entry without a proof", pg, h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithRekorV1Entry(sh.WithoutRekorProof())), sign.ReasonParse},
		{"not JSON", gh, []byte("acme, not a bundle"), sign.ReasonParse},
		{"duplicate media type members", gh, duplicateMediaType(t, ghBundle(t, h)), sign.ReasonParse},
		{"unknown media type", gh, ghBundle(t, h, sh.WithMediaType("application/vnd.dev.sigstore.bundle.v0.4+json")), sign.ReasonMediaType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sign.VerifyAttestationBundle(context.Background(), tc.raw, tc.inst)
			wantReason(t, err, tc.want)
		})
	}
}

// TestVerifyAttestation_Times covers the verified times: the earliest of
// several is the attested time, a timestamp added beside a signed entry
// timestamp does not move it, and an unsigned integrated time is no time.
//
// Spec: §4.7.11
func TestVerifyAttestation_Times(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	pg := publicGood(t, h)
	clock := h.Clock()
	t.Run("later timestamp beside a signed entry timestamp", func(t *testing.T) {
		raw := pgBundle(t, h, sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(clock.Add(5*time.Minute))))
		v, err := verify(t, pg, raw, emailSigner())
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if len(v.VerifiedTimes) != 2 || !v.AttestedTime.Equal(clock) {
			t.Fatalf("times %v attested %v; want two, earliest %v", v.VerifiedTimes, v.AttestedTime, clock)
		}
	})
	t.Run("earliest of three", func(t *testing.T) {
		raw := h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithRekorV1Entry(sh.WithIntegratedTime(clock.Add(2*time.Minute))),
			sh.WithTimestamp(sh.TSASecond, sh.WithGenTime(clock.Add(time.Minute))),
			sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(clock.Add(3*time.Minute))))
		v, err := verify(t, pg, raw, emailSigner())
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		want := []time.Time{clock.Add(time.Minute), clock.Add(2 * time.Minute), clock.Add(3 * time.Minute)}
		for i, w := range want {
			if !v.VerifiedTimes[i].Equal(w) {
				t.Fatalf("verified times = %v, want %v", v.VerifiedTimes, want)
			}
		}
		if !v.AttestedTime.Equal(want[0]) {
			t.Fatalf("attested %v, want %v", v.AttestedTime, want[0])
		}
	})
	t.Run("Rekor v2 entry bound to the bundle", func(t *testing.T) {
		raw := h.DSSEBundle(statement(t), sh.WithSCT(), sh.WithTimestamp(sh.TSAPrimary), sh.WithRekorV1Entry(sh.WithRekorV2Body()))
		v, err := verify(t, pg, raw, emailSigner())
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if len(v.VerifiedTimes) != 1 || !v.AttestedTime.Equal(clock) {
			t.Fatalf("times %v; want only the timestamp, because a Rekor v2 entry carries no signed time", v.VerifiedTimes)
		}
	})
	t.Run("integrated time without a promise is no time", func(t *testing.T) {
		raw := h.DSSEBundle(statement(t), sh.WithSCT(),
			sh.WithRekorV1Entry(sh.WithoutSET(), sh.WithIntegratedTime(clock.Add(-2*time.Minute))),
			sh.WithTimestamp(sh.TSAPrimary))
		v, err := verify(t, pg, raw, emailSigner())
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if len(v.VerifiedTimes) != 1 || !v.AttestedTime.Equal(clock) {
			t.Fatalf("times %v attested %v; want only the timestamp at %v", v.VerifiedTimes, v.AttestedTime, clock)
		}
	})
}

// TestVerifyAttestation_TimestampAuthorities covers the §4.7.11 rules on
// timestamp authorities: a CA-first entry refuses the root, an expired
// entry loads, an RSA-SHA384 timestamp is accepted and marked, the SHA-1
// chain is reported, and podium attest import's removal of the embedded
// certificates makes a refused bundle acceptable.
//
// Spec: §4.7.11
func TestVerifyAttestation_TimestampAuthorities(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	t.Run("CA-first entry", func(t *testing.T) {
		_, err := sign.ParseAttestationRoot(h.TrustedRootJSON(sh.WithCAFirstTSA()))
		if !errors.Is(err, sign.ErrAttestationRootInvalid) {
			t.Fatalf("err = %v, want ErrAttestationRootInvalid", err)
		}
	})
	t.Run("expired entries load and the current one verifies", func(t *testing.T) {
		gh := githubInstance(t, h)
		if _, err := verify(t, gh, ghBundle(t, h), emailSigner()); err != nil {
			t.Fatalf("verify: %v", err)
		}
	})
	t.Run("RSA with SHA-384 is accepted and marked", func(t *testing.T) {
		v, err := verify(t, githubInstance(t, h), h.DSSEBundle(statement(t), sh.WithTimestamp(sh.TSARSA)), emailSigner())
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if !v.RSATimestampWithoutSHA256 {
			t.Fatal("RSATimestampWithoutSHA256 = false, want true")
		}
		v, err = verify(t, githubInstance(t, h), ghBundle(t, h), emailSigner())
		if err != nil || v.RSATimestampWithoutSHA256 {
			t.Fatalf("ECDSA timestamp: err %v, marked %v", err, v != nil && v.RSATimestampWithoutSHA256)
		}
	})
	t.Run("SHA-1 chain is reported", func(t *testing.T) {
		r := mustRoot(t, h.TrustedRootJSON(sh.WithTimestampAuthorities(sh.TSASHA1)))
		got := r.SHA1TimestampAuthorities()
		if len(got) != 1 || got[0] != "https://tsa-sha1.acme.test" {
			t.Fatalf("SHA1TimestampAuthorities = %v", got)
		}
		if n := len(mustRoot(t, h.TrustedRootJSON()).SHA1TimestampAuthorities()); n != 0 {
			t.Fatalf("default root reports %d SHA-1 authorities", n)
		}
	})
	t.Run("embedded chain removed by import", func(t *testing.T) {
		raw := h.DSSEBundle(statement(t), sh.WithTimestamp(sh.TSAPrimary, sh.WithEmbeddedChain()))
		gh := githubInstance(t, h)
		_, err := verify(t, gh, raw, emailSigner())
		wantReason(t, err, sign.ReasonTimestampCondition)
		if _, err := verify(t, gh, sh.StripTimestampCertificates(t, raw), emailSigner()); err != nil {
			t.Fatalf("stripped bundle: %v", err)
		}
	})
}

// duplicateMediaType adds a media_type member beside mediaType, which
// protojson refuses as one field written twice.
func duplicateMediaType(t testing.TB, raw []byte) []byte {
	t.Helper()
	i := bytes.IndexByte(raw, '{')
	return append(append(append([]byte(nil), raw[:i+1]...),
		[]byte(`"media_type":"`+sh.BundleMediaTypeV03+`",`)...), raw[i+1:]...)
}

// TestVerifyAttestation_LogKeyWindow covers the §4.7.11 log key validity
// window: a log whose window excludes a verified time verifies no entry,
// whether the entry carries a signed entry timestamp or the time comes
// from an RFC 3161 timestamp, and the last listing of a log ID decides.
//
// Spec: §4.7.11
func TestVerifyAttestation_LogKeyWindow(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	clock := h.Clock()
	at := func(from, to time.Duration) sh.Window {
		start, end := clock.Add(from), clock.Add(to)
		return sh.Window{Start: &start, End: &end}
	}
	expired, notYet, inside := at(-48*time.Hour, -24*time.Hour), at(24*time.Hour, 48*time.Hour), at(-time.Hour, time.Hour)
	v1NoSET := func() []byte {
		return h.DSSEBundle(statement(t), sh.WithSCT(), sh.WithTimestamp(sh.TSAPrimary), sh.WithRekorV1Entry(sh.WithoutSET()))
	}
	v2 := func() []byte {
		return h.DSSEBundle(statement(t), sh.WithSCT(), sh.WithTimestamp(sh.TSAPrimary), sh.WithRekorV1Entry(sh.WithRekorV2Body()))
	}
	v1SET := func() []byte { return pgBundle(t, h) }
	for _, tc := range []struct {
		name    string
		windows []sh.Window
		raw     []byte
		want    sign.AttestationReason
	}{
		{"v1 entry with no promise, expired key", []sh.Window{expired}, v1NoSET(), sign.ReasonTime},
		{"v1 entry with no promise, key not yet valid", []sh.Window{notYet}, v1NoSET(), sign.ReasonTime},
		{"v2 entry, expired key", []sh.Window{expired}, v2(), sign.ReasonTime},
		{"v2 entry, key not yet valid", []sh.Window{notYet}, v2(), sign.ReasonTime},
		{"v1 entry with a promise, expired key", []sh.Window{expired}, v1SET(), sign.ReasonTime},
		{"v1 entry with a promise, key not yet valid", []sh.Window{notYet}, v1SET(), sign.ReasonTime},
		{"v1 entry with no promise inside the window", []sh.Window{inside}, v1NoSET(), ""},
		{"v2 entry inside the window", []sh.Window{inside}, v2(), ""},
		{"v1 entry with a promise inside the window", []sh.Window{inside}, v1SET(), ""},
		{"listed twice, last listing inside", []sh.Window{expired, inside}, v1NoSET(), ""},
		{"listed twice, last listing expired", []sh.Window{inside, expired}, v1NoSET(), sign.ReasonTime},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verify(t, publicGood(t, h, sh.WithLogValidFor(tc.windows...)), tc.raw, emailSigner())
			if tc.want == "" {
				if err != nil {
					t.Fatalf("verify: %v", err)
				}
				return
			}
			wantReason(t, err, tc.want)
		})
	}
}
