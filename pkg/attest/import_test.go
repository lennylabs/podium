package attest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
)

const (
	slsa    = "https://slsa.dev/provenance/v1"
	scanTyp = "https://acme.test/scan/v1"
	hexA    = "9f2c000000000000000000000000000000000000000000000000000000000001"
	hexB    = "9f2c000000000000000000000000000000000000000000000000000000000002"
)

// githubRoot is a timestamp-only instance, as GitHub's instance is.
func githubRoot(t *testing.T, h *sh.Harness) sign.AttestationInstance {
	t.Helper()
	r, err := sign.ParseAttestationRoot(h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	if err != nil {
		t.Fatalf("ParseAttestationRoot: %v", err)
	}
	return sign.AttestationInstance{Name: "github", Root: r}
}

func stmt(t *testing.T, subjects ...sh.Subject) []byte {
	return sh.Statement(t, slsa, subjects...)
}

// Spec: §4.7.11 (Timestamp condition), §7.3.5 — a timestamp that embeds two
// certificates breaks the timestamp condition; the import removes them and
// the bundle verifies under the root's authority entry, with every byte
// outside the signedTimestamp value kept.
func TestRemoveTimestampCertificates_TwoCertificates(t *testing.T) {
	h := sh.New(t)
	raw := h.DSSEBundle(stmt(t, sh.Subject{Name: ".podium/layer", SHA256: hexA}), sh.WithTimestamp(sh.TSAPrimary, sh.WithEmbeddedChain()))
	gh := githubRoot(t, h)
	if _, err := sign.VerifyAttestationBundle(context.Background(), raw, gh); err == nil {
		t.Fatal("a timestamp with two certificates verified")
	} else if r, _ := sign.AttestationReasonOf(err); r != sign.ReasonTimestampCondition {
		t.Fatalf("reason = %q, want timestamp_condition", r)
	}
	out, changed, err := attest.RemoveTimestampCertificates(raw)
	if err != nil || !changed {
		t.Fatalf("RemoveTimestampCertificates: changed %v, %v", changed, err)
	}
	v, err := sign.VerifyAttestationBundle(context.Background(), out, gh)
	if err != nil {
		t.Fatalf("the bundle after the removal: %v", err)
	}
	if v.AcceptsSigner(sign.AttestationSigner{Issuer: sh.DefaultIssuer, Identity: sh.DefaultSAN}) != nil {
		t.Fatal("the signer changed")
	}
	assertOnlyTimestampChanged(t, raw, out)
	// A second pass finds nothing to remove and returns the bytes.
	again, changed, err := attest.RemoveTimestampCertificates(out)
	if err != nil || changed || !bytes.Equal(again, out) {
		t.Fatalf("second pass: changed %v, %v", changed, err)
	}
}

// Spec: §7.3.5 — the default token embeds the authority's certificate,
// which the import removes, and a token with none is kept byte for byte.
func TestRemoveTimestampCertificates_OneOrNone(t *testing.T) {
	h := sh.New(t)
	gh := githubRoot(t, h)
	one := h.DSSEBundle(stmt(t, sh.Subject{Name: "a@1.0.0", SHA256: hexA}), sh.WithTimestamp(sh.TSAPrimary))
	out, changed, err := attest.RemoveTimestampCertificates(one)
	if err != nil || !changed {
		t.Fatalf("one certificate: changed %v, %v", changed, err)
	}
	if _, err := sign.VerifyAttestationBundle(context.Background(), out, gh); err != nil {
		t.Fatalf("verify: %v", err)
	}
	none := h.DSSEBundle(stmt(t, sh.Subject{Name: "a@1.0.0", SHA256: hexA}), sh.WithTimestamp(sh.TSAPrimary, sh.WithoutEmbeddedCerts()))
	out, changed, err = attest.RemoveTimestampCertificates(none)
	if err != nil || changed || !bytes.Equal(out, none) {
		t.Fatalf("no certificate: changed %v, %v", changed, err)
	}
	// A bundle with no timestamp, such as a public-good bundle with a log
	// entry, is kept too.
	pg := h.DSSEBundle(stmt(t, sh.Subject{Name: "a@1.0.0", SHA256: hexA}), sh.WithSCT(), sh.WithRekorV1Entry())
	if out, changed, err := attest.RemoveTimestampCertificates(pg); err != nil || changed || !bytes.Equal(out, pg) {
		t.Fatalf("no timestamp: changed %v, %v", changed, err)
	}
}

// Spec: §7.3.5 — the protobuf JSON parse also reads the protobuf field
// names, so the removal follows them, and URL-safe base64 decodes.
func TestRemoveTimestampCertificates_ProtoNames(t *testing.T) {
	h := sh.New(t)
	raw := h.DSSEBundle(stmt(t, sh.Subject{Name: "a@1.0.0", SHA256: hexA}), sh.WithTimestamp(sh.TSAPrimary, sh.WithEmbeddedChain()))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	vm := doc["verificationMaterial"].(map[string]any)
	td := vm["timestampVerificationData"].(map[string]any)
	list := td["rfc3161Timestamps"].([]any)
	ts := list[0].(map[string]any)
	der, _ := base64.StdEncoding.DecodeString(ts["signedTimestamp"].(string))
	renamed := map[string]any{
		"mediaType":    doc["mediaType"],
		"dsseEnvelope": doc["dsseEnvelope"],
		"verification_material": map[string]any{
			"certificate": vm["certificate"],
			"timestamp_verification_data": map[string]any{
				"rfc3161_timestamps": []any{map[string]any{"signed_timestamp": base64.RawURLEncoding.EncodeToString(der)}},
			},
		},
	}
	in, err := json.Marshal(renamed)
	if err != nil {
		t.Fatal(err)
	}
	out, changed, err := attest.RemoveTimestampCertificates(in)
	if err != nil || !changed {
		t.Fatalf("changed %v, %v", changed, err)
	}
	if _, err := sign.VerifyAttestationBundle(context.Background(), out, githubRoot(t, h)); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// Spec: §7.3.5 — a timestamp that does not parse refuses the import.
func TestRemoveTimestampCertificates_Refusals(t *testing.T) {
	wrap := func(v string) []byte {
		return []byte(`{"mediaType":"x","verificationMaterial":{"timestampVerificationData":{"rfc3161Timestamps":[{"signedTimestamp":` + v + `}]}}}`)
	}
	seq := func(parts ...[]byte) []byte {
		b, _ := asn1.Marshal(asn1.RawValue{Tag: asn1.TagSequence, IsCompound: true, Bytes: bytes.Join(parts, nil)})
		return b
	}
	b64 := func(b []byte) string { return `"` + base64.StdEncoding.EncodeToString(b) + `"` }
	status := seq(mustMarshal(t, 0))
	otherOID := mustMarshal(t, asn1.ObjectIdentifier{1, 2, 3})
	cases := map[string][]byte{
		"not a string":          wrap(`1`),
		"not base64":            wrap(`"%%%"`),
		"not DER":               wrap(b64([]byte("text"))),
		"trailing bytes":        wrap(b64(append(seq(status), 0))),
		"no token":              wrap(b64(seq(status))),
		"token not a sequence":  wrap(b64(seq(status, mustMarshal(t, 5)))),
		"token not signed data": wrap(b64(seq(status, seq(otherOID, mustExplicit(t, seq()))))),
		"signed data not a sequence": wrap(b64(seq(status, seq(mustMarshal(t, asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}),
			mustExplicit(t, mustMarshal(t, 7)))))),
		"bad member": wrap(b64(seq(status, seq(mustMarshal(t, asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}),
			mustExplicit(t, []byte{0x30, 0x02, 0x02, 0x05}))))),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := attest.RemoveTimestampCertificates(in); !errors.Is(err, attest.ErrTimestamp) {
				t.Fatalf("err = %v, want ErrTimestamp", err)
			}
		})
	}
	// A member that is not an object or an array visits nothing, so the
	// bundle parse reports it rather than the removal.
	for _, in := range []string{`[]`, `{"verificationMaterial":1}`, `{"verificationMaterial":{"timestampVerificationData":{"rfc3161Timestamps":{}}}}`} {
		if out, changed, err := attest.RemoveTimestampCertificates([]byte(in)); err != nil || changed || string(out) != in {
			t.Fatalf("%s: changed %v, %v", in, changed, err)
		}
	}
	// Signed data with no certificates member is kept.
	sd := seq(mustMarshal(t, 1), mustMarshal(t, 2))
	resp := seq(status, seq(mustMarshal(t, asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}), mustExplicit(t, sd)))
	if _, ok, err := attest.StripTimestampResponse(resp); ok || err != nil {
		t.Fatalf("no certificates: ok %v, %v", ok, err)
	}
	for _, in := range []string{`{"a":`, `{"verificationMaterial":{"timestampVerificationData":{"rfc3161Timestamps":[1,`} {
		if _, _, err := attest.RemoveTimestampCertificates([]byte(in)); err == nil {
			t.Fatalf("%s: truncated JSON accepted", in)
		}
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := asn1.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustExplicit(t *testing.T, inner []byte) []byte {
	t.Helper()
	b, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: inner})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// assertOnlyTimestampChanged checks that out differs from in only inside
// the signedTimestamp string.
func assertOnlyTimestampChanged(t *testing.T, in, out []byte) {
	t.Helper()
	start := func(b []byte) int {
		i := bytes.Index(b, []byte(`"signedTimestamp"`))
		if i < 0 {
			t.Fatal("no signedTimestamp member")
		}
		i += len(`"signedTimestamp"`)
		return i + bytes.IndexByte(b[i:], '"') + 1
	}
	si, so := start(in), start(out)
	if si != so || !bytes.Equal(in[:si], out[:so]) {
		t.Fatal("bytes before the timestamp changed")
	}
	ei := si + bytes.IndexByte(in[si:], '"')
	eo := so + bytes.IndexByte(out[so:], '"')
	if bytes.Equal(in[si:ei], out[so:eo]) || !bytes.Equal(in[ei:], out[eo:]) {
		t.Fatal("the timestamp kept its bytes, or bytes after it changed")
	}
}

// Spec: §4.7.11 (Attestation directory) — the file name is the hexadecimal
// digits of the attestation digest, which is the SHA-256 of the bytes.
func TestBundleFileName(t *testing.T) {
	b := []byte(`{"mediaType":"x"}`)
	sum := sha256.Sum256(b)
	if got, want := attest.BundleFileName(b), hex.EncodeToString(sum[:])+".sigstore.json"; got != want {
		t.Fatalf("BundleFileName = %q, want %q", got, want)
	}
	if got := attest.AttestationDigest(b); got != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("AttestationDigest = %q", got)
	}
	if strings.Contains(attest.BundleFileName(b), ":") {
		t.Fatal("the file name holds a colon")
	}
	for name, want := range map[string]bool{"x.sigstore.json": true, "a.json": false, ".sigstore.json": true, "x.sigstore.json.bak": false} {
		if attest.IsBundleFileName(name) != want {
			t.Fatalf("IsBundleFileName(%q) != %v", name, want)
		}
	}
}

// Spec: §7.3.5 — an input holds one bundle, or several as JSON Lines.
func TestSplitBundles(t *testing.T) {
	one := "{\n  \"a\": 1\n}\n"
	got, err := attest.SplitBundles([]byte(one))
	if err != nil || len(got) != 1 || string(got[0]) != "{\n  \"a\": 1\n}" {
		t.Fatalf("one bundle: %q, %v", got, err)
	}
	got, err = attest.SplitBundles([]byte("{\"a\":1}\n{\"b\":2}\n\n{\"c\":3}"))
	if err != nil || len(got) != 3 || string(got[1]) != `{"b":2}` {
		t.Fatalf("JSON Lines: %q, %v", got, err)
	}
	if _, err := attest.SplitBundles([]byte(" \n")); !errors.Is(err, attest.ErrNoBundle) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := attest.SplitBundles([]byte("{\"a\":1}\nnot json")); err == nil {
		t.Fatal("a line that is not JSON was accepted")
	}
}

// summary builds the unverified summary of a bundle with these subjects.
func summary(pt, issuer, identity string, subjects ...sign.AttestationSubject) sign.BundleSummary {
	return sign.BundleSummary{Statement: sign.AttestationStatement{PredicateType: pt, Subjects: subjects}, Issuer: issuer, Identity: identity}
}

func subj(name, hexDigest string) sign.AttestationSubject {
	return sign.AttestationSubject{Name: name, Digest: map[string]string{"sha256": hexDigest}}
}

// Spec: §7.3.5 (Pruning) — the prune rule over the imported set.
func TestPrune(t *testing.T) {
	cur := attest.NewCurrentSubjects(attest.Subject{Name: ".podium/layer", Digest: "sha256:" + hexA},
		[]attest.Subject{{Name: "a@1.0.0", Digest: "sha256:" + hexA}, {Name: "b@1.0.0", Digest: "sha256:" + hexB}})
	ci := "https://token.actions.githubusercontent.com"
	build := "https://github.com/acme/skills/.github/workflows/podium-attest.yml@refs/heads/main"
	scanner := "https://github.com/acme/scanner/.github/workflows/scan.yml@refs/heads/main"
	newBuild := summary(slsa, ci, build, subj(".podium/layer", hexA), subj("a@1.0.0", hexA), subj("b@1.0.0", hexB))
	half := summary(slsa, ci, build, subj(".podium/layer", hexA), subj("a@1.0.0", hexA))
	cases := []struct {
		name     string
		existing sign.BundleSummary
		imported []sign.BundleSummary
		want     attest.PruneReason
	}{
		{"stale content", summary(slsa, ci, build, subj("a@1.0.0", hexB), subj(".podium/layer", hexB)), nil, attest.PruneNoCurrentSubject},
		{"uppercase digest matches nothing", summary(slsa, ci, build, subj("a@1.0.0", strings.ToUpper(hexA))), nil, attest.PruneNoCurrentSubject},
		{"superseded by the imported set", summary(slsa, ci, build, subj(".podium/layer", hexA), subj("a@1.0.0", hexA), subj("old@1.0.0", hexA)), []sign.BundleSummary{newBuild}, attest.PruneSuperseded},
		{"covered by two imported bundles together", summary(slsa, ci, build, subj("a@1.0.0", hexA), subj("b@1.0.0", hexB)),
			[]sign.BundleSummary{half, summary(slsa, ci, build, subj(".podium/layer", hexA), subj("b@1.0.0", hexB))}, attest.PruneSuperseded},
		{"a subject the imported set lacks keeps it", summary(slsa, ci, build, subj("a@1.0.0", hexA), subj("b@1.0.0", hexB)), []sign.BundleSummary{half}, attest.PruneKeep},
		{"another signer stays", summary(slsa, ci, scanner, subj("a@1.0.0", hexA)), []sign.BundleSummary{newBuild}, attest.PruneKeep},
		{"another predicate type stays", summary(scanTyp, ci, build, subj("a@1.0.0", hexA)), []sign.BundleSummary{newBuild}, attest.PruneKeep},
		{"another issuer stays", summary(slsa, "https://gitlab.com", build, subj("a@1.0.0", hexA)), []sign.BundleSummary{newBuild}, attest.PruneKeep},
		{"no signer stays while it matches", summary(slsa, "", "", subj("a@1.0.0", hexA)), []sign.BundleSummary{summary(slsa, "", "", subj("a@1.0.0", hexA))}, attest.PruneKeep},
		{"nothing imported", summary(slsa, ci, build, subj("a@1.0.0", hexA)), nil, attest.PruneKeep},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := attest.Prune(c.existing, c.imported, cur); got != c.want {
				t.Fatalf("Prune = %q, want %q", got, c.want)
			}
		})
	}
	// A name listed twice counts once.
	if got := cur.Matching(sign.AttestationStatement{Subjects: []sign.AttestationSubject{subj("a@1.0.0", hexA), subj("a@1.0.0", hexA)}}); len(got) != 1 {
		t.Fatalf("Matching = %v", got)
	}
}

// Spec: §7.3.5 — a bundle with two timestamps from two authorities has the
// certificates of each removed, and verifies under a root that lists both.
func TestRemoveTimestampCertificates_TwoTimestamps(t *testing.T) {
	h := sh.New(t)
	raw := h.DSSEBundle(stmt(t, sh.Subject{Name: "a@1.0.0", SHA256: hexA}),
		sh.WithTimestamp(sh.TSAPrimary, sh.WithEmbeddedChain()), sh.WithTimestamp(sh.TSASecond, sh.WithEmbeddedChain()))
	out, changed, err := attest.RemoveTimestampCertificates(raw)
	if err != nil || !changed {
		t.Fatalf("changed %v, %v", changed, err)
	}
	r, err := sign.ParseAttestationRoot(h.TrustedRootJSON(append(sh.GitHubStyleRoot(), sh.WithTimestampAuthorities(sh.TSASecond))...))
	if err != nil {
		t.Fatal(err)
	}
	v, err := sign.VerifyAttestationBundle(context.Background(), out, sign.AttestationInstance{Name: "github", Root: r})
	if err != nil || len(v.VerifiedTimes) != 2 {
		t.Fatalf("verify: %v", err)
	}
}
