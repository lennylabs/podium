package sign_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/sign"
)

// The sample bundles under testdata/attestation are real attestations of
// the gh 2.106.0 linux_386 tar.gz release asset (sha256:4718e9ae...45e8),
// fetched on 2026-11-26 without credentials from the GitHub attestation API
// of the public cli/cli repository. public-good.sigstore.json is the build
// provenance its release workflow signed on the public-good instance: a
// DSSE envelope, a Rekor v1 entry with a signed entry timestamp, and no
// RFC 3161 timestamp. github.sigstore.json is the release attestation
// GitHub signed on its own instance: an RFC 3161 timestamp, no log entry,
// and a certificate with no Fulcio extension. The trusted roots are the
// trusted_root.json targets of the Sigstore and GitHub TUF repositories on
// the same day.
const sampleDir = "testdata/attestation"

// sampleInstance loads a sample trusted root as an instance.
func sampleInstance(t *testing.T, root string, tlog bool) sign.AttestationInstance {
	t.Helper()
	r, err := sign.LoadAttestationRootFile(filepath.Join(sampleDir, root))
	if err != nil {
		t.Fatalf("LoadAttestationRootFile: %v", err)
	}
	return sign.AttestationInstance{Name: root, Root: r, TransparencyLog: tlog, CertificateTransparency: tlog}
}

// TestVerifyAttestation_SampleBundles verifies the real sample bundles of
// each instance kind, which the harness-generated bundles stand in for
// elsewhere.
//
// Spec: §4.7.11
func TestVerifyAttestation_SampleBundles(t *testing.T) {
	t.Parallel()
	pgRaw, err := os.ReadFile(filepath.Join(sampleDir, "public-good.sigstore.json"))
	if err != nil {
		t.Fatal(err)
	}
	pg := sampleInstance(t, "public-good.trusted_root.json", true)
	v, err := sign.VerifyAttestationBundle(context.Background(), pgRaw, pg)
	if err != nil {
		t.Fatalf("public-good sample: %v", err)
	}
	if want := time.Date(2026, 11, 25, 22, 46, 41, 0, time.UTC); !v.AttestedTime.Equal(want) {
		t.Errorf("attested time %v, want %v", v.AttestedTime, want)
	}
	signer := sign.AttestationSigner{
		Issuer:             "https://token.actions.githubusercontent.com",
		Identity:           "https://github.com/cli/cli/.github/workflows/deployment.yml@refs/heads/trunk",
		SourceRepositoryID: "212613049", SourceRepositoryOwnerID: "59704711",
		SourceRepositoryRef: "refs/heads/trunk",
	}
	if err := v.AcceptsSigner(signer); err != nil {
		t.Errorf("signer of the sample: %v", err)
	}
	if v.Certificate.SourceRepositoryDigest.Value != "529111a21612ff1e29dc45e567a157df24e0e243" {
		t.Errorf("source commit = %q", v.Certificate.SourceRepositoryDigest.Value)
	}
	prefix := signer
	prefix.Identity = signer.Identity[:len(signer.Identity)-1]
	wantReason(t, v.AcceptsSigner(prefix), sign.ReasonIdentity)
	branch := signer
	branch.SourceRepositoryRef = "refs/heads/main"
	wantReason(t, v.AcceptsSigner(branch), sign.ReasonRefBinding)
	// The same bundle on an instance with no transparency log carries a log
	// entry that instance does not admit.
	_, err = sign.VerifyAttestationBundle(context.Background(), pgRaw, sampleInstance(t, "github.trusted_root.json", false))
	wantReason(t, err, sign.ReasonTimestampCondition)

	ghRaw, err := os.ReadFile(filepath.Join(sampleDir, "github.sigstore.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The release attestation's timestamp, chain, and signature verify, and
	// its certificate carries no Issuer (V2) extension, so it is refused
	// for every signer.
	_, err = sign.VerifyAttestationBundle(context.Background(), ghRaw, sampleInstance(t, "github.trusted_root.json", false))
	wantReason(t, err, sign.ReasonIssuer)
	_, err = sign.VerifyAttestationBundle(context.Background(), ghRaw, pg)
	wantReason(t, err, sign.ReasonTimestampCondition)
}
