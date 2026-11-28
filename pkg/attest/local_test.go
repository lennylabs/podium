package attest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
)

// localFixture is a filesystem-source package of finance/pay in a layer
// whose digest is layerDigest, and a bundle that lists both subjects.
type localFixture struct {
	h           *sh.Harness
	pkg         attest.LocalPackage
	layerDigest string
}

// newLocalFixture signs the package subject and the layer subject named
// layerName in one bundle of the default signer.
func newLocalFixture(t *testing.T, layerName string) *localFixture {
	t.Helper()
	h := sh.New(t)
	f := &localFixture{h: h, layerDigest: "sha256:" + strings.Repeat("ab", 32)}
	digest := signerDigest(t, map[string][]byte{"ARTIFACT.md": []byte(agentPlainMD)})
	bundle := f.bundle(t, sh.Subject{Name: "finance/pay@2.0.0", SHA256: strings.TrimPrefix(digest, "sha256:")},
		sh.Subject{Name: layerName, SHA256: strings.TrimPrefix(f.layerDigest, "sha256:")})
	f.pkg = attest.LocalPackage{ID: "finance/pay", SubjectName: "finance/pay@2.0.0", PackageDigest: digest,
		Layer: &attest.LocalLayer{SubjectName: ".podium/layer", Digest: f.layerDigest, Bundles: [][]byte{bundle}}}
	return f
}

// bundle signs a statement of the subjects.
func (f *localFixture) bundle(t *testing.T, subjects ...sh.Subject) []byte {
	t.Helper()
	return f.h.DSSEBundle(sh.Statement(t, slsaType, subjects...),
		sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(f.h.Clock().Add(time.Minute))))
}

// checker is a checker under one user-source file of rules and a default.
func (f *localFixture) checker(t *testing.T, def attest.Mode, rules ...string) *attest.Checker {
	t.Helper()
	file := writePolicy(t, f.h, policyJSON(rules...), sign.AttestationInstance{})
	p, err := attest.NewPolicy(attest.Source{Class: attest.UserSource, DefaultMode: def, Files: []*attest.PolicyFile{file}})
	if err != nil {
		t.Fatal(err)
	}
	return &attest.Checker{Policy: p}
}

// Spec: §13.11.3, §4.7.11 (Attestation check, step 10) — a filesystem-source
// package passes under require when a bundle of its rule's signer lists its
// subject with the digest of the files the walk read and the layer subject
// with the layer digest, and the result carries the digest and the signer
// that the lock records.
func TestVerifyLocal_RequirePasses(t *testing.T) {
	t.Parallel()
	f := newLocalFixture(t, ".podium/layer")
	res, err := f.checker(t, "", requireRule("require", "finance/**")).VerifyLocal(context.Background(), f.pkg)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Checked || res.PackageDigest != f.pkg.PackageDigest || len(res.Accepted) != 1 ||
		res.Accepted[0].Instance != "github" || res.Accepted[0].Identity != sh.DefaultSAN {
		t.Fatalf("result = %+v", res)
	}
}

// Spec: §13.11.3 — an ID under none runs no check and reads no bundle.
func TestVerifyLocal_NoneChecksNothing(t *testing.T) {
	t.Parallel()
	f := newLocalFixture(t, ".podium/layer")
	f.pkg.Layer = nil
	res, err := (&attest.Checker{}).VerifyLocal(context.Background(), f.pkg)
	if err != nil || res.Checked || res.Resolution.Mode != attest.ModeNone {
		t.Fatalf("VerifyLocal = %+v, %v", res, err)
	}
}

// Spec: §13.11.3 (Layer subject) — a layer subject that is missing, that
// names another root, or that carries another digest is
// materialize.attestation_mismatch (layer_digest), and so is a layer with no
// layer digest; the package subject alone does not pass.
func TestVerifyLocal_LayerSubject(t *testing.T) {
	t.Parallel()
	f := newLocalFixture(t, ".podium/layer/other")
	c := f.checker(t, "", requireRule("require", "finance/**"))
	_, err := c.VerifyLocal(context.Background(), f.pkg)
	wantFailure(t, err, attest.CodeAttestationMismatch, attest.ConstraintLayerDigest)

	f.pkg.Layer.SubjectName = ".podium/layer/other"
	if _, err := c.VerifyLocal(context.Background(), f.pkg); err != nil {
		t.Fatalf("the expected root that the bundle names: %v", err)
	}
	f.pkg.Layer.Digest = "sha256:" + strings.Repeat("cd", 32)
	_, err = c.VerifyLocal(context.Background(), f.pkg)
	wantFailure(t, err, attest.CodeAttestationMismatch, attest.ConstraintLayerDigest)
	f.pkg.Layer.Digest = ""
	_, err = c.VerifyLocal(context.Background(), f.pkg)
	wantFailure(t, err, attest.CodeAttestationMismatch, attest.ConstraintLayerDigest)
}

// Spec: §13.11.3 — under warn every failure is a warning and the package is
// delivered; a package with no subject name, a layer whose directory failed
// to read, a layer with no bundle, and a package digest no subject lists
// each take their code under require.
func TestVerifyLocal_Failures(t *testing.T) {
	t.Parallel()
	f := newLocalFixture(t, ".podium/layer")
	req := f.checker(t, "", requireRule("require", "finance/**"))
	warn := f.checker(t, "", requireRule("warn", "finance/**"))
	ctx := context.Background()

	noName := f.pkg
	noName.SubjectName, noName.SubjectErr = "", attest.ErrNoSubjectName
	_, err := req.VerifyLocal(ctx, noName)
	wantFailure(t, err, attest.CodeAttestationMissing, "")

	limits := f.pkg
	limits.Layer = &attest.LocalLayer{Err: attest.NewFailure(attest.CodeAttestationInvalid, attest.ConstraintLimits, "too many")}
	_, err = req.VerifyLocal(ctx, limits)
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintLimits)

	empty := f.pkg
	empty.Layer = &attest.LocalLayer{SubjectName: ".podium/layer", Digest: f.layerDigest}
	_, err = req.VerifyLocal(ctx, empty)
	wantFailure(t, err, attest.CodeAttestationMissing, "")

	nilLayer := f.pkg
	nilLayer.Layer = nil
	_, err = req.VerifyLocal(ctx, nilLayer)
	wantFailure(t, err, attest.CodeAttestationMissing, "")

	tampered := f.pkg
	tampered.PackageDigest = "sha256:" + strings.Repeat("ef", 32)
	_, err = req.VerifyLocal(ctx, tampered)
	wantFailure(t, err, attest.CodeAttestationMismatch, attest.ConstraintSubject)

	res, err := warn.VerifyLocal(ctx, tampered)
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0].Constraint != attest.ConstraintSubject {
		t.Fatalf("warn = %+v, %v", res, err)
	}

	// A rule whose signer is another identity is untrusted.
	other := f.checker(t, "", `{"ids":["finance/**"],"mode":"require","requirements":[{"signers":[{"issuer":"`+sh.DefaultIssuer+`","identity":"bob@acme.com","instance":"github"}]}]}`)
	_, err = other.VerifyLocal(ctx, f.pkg)
	wantFailure(t, err, attest.CodeAttestationUntrusted, attest.ConstraintSigner)

	// An ID no rule matches under a user default of require has no user rule.
	_, err = f.checker(t, attest.ModeRequire, requireRule("require", "other/**")).VerifyLocal(ctx, f.pkg)
	wantFailure(t, err, attest.CodeAttestationUntrusted, attest.ConstraintUserRule)
}

// Spec: §13.11.3 — a rule that a warn rule of a second file matches reports
// the warn rule's layer subject failure as a warning while the require rule
// passes, so one signer vouches for the layer toward its own rule alone.
func TestVerifyLocal_ReportedRuleLayerSubject(t *testing.T) {
	t.Parallel()
	f := newLocalFixture(t, ".podium/layer")
	user := writePolicy(t, f.h, policyJSON(requireRule("require", "finance/**")), sign.AttestationInstance{})
	repo := writePolicy(t, f.h, policyJSON(`{"ids":["finance/**"],"mode":"warn","requirements":[{"signers":[{"issuer":"`+sh.DefaultIssuer+`","identity":"bob@acme.com","instance":"github"}]}]}`), sign.AttestationInstance{})
	p, err := attest.NewPolicy(attest.Source{Class: attest.UserSource, Files: []*attest.PolicyFile{user}},
		attest.Source{Class: attest.RepositorySource, Files: []*attest.PolicyFile{repo}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := (&attest.Checker{Policy: p}).VerifyLocal(context.Background(), f.pkg)
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0].Constraint != attest.ConstraintSigner {
		t.Fatalf("VerifyLocal = %+v, %v", res, err)
	}
}

// Spec: §13.11.3 (Bundles) — the directory reader returns the bundle files
// in name order, reads no other entry, treats a missing directory as no
// bundle, and refuses more files than the count limit, a file over the size
// limit, a directory it cannot list, and an entry that is not a regular file.
func TestReadBundleDirectory(t *testing.T) {
	t.Parallel()
	lim := attest.Limits{MaxBytes: 8, MaxBundles: 2}
	dir := ".podium/attestations/"
	got, err := attest.ReadBundleDirectory(fstest.MapFS{
		dir + "b.sigstore.json": {Data: []byte("bb")},
		dir + "a.sigstore.json": {Data: []byte("aa")},
		dir + "notes.txt":       {Data: []byte("ignored, and longer than the limit")},
	}, lim)
	if err != nil || len(got) != 2 || string(got[0]) != "aa" || string(got[1]) != "bb" {
		t.Fatalf("read = %q, %v", got, err)
	}
	if got, err := attest.ReadBundleDirectory(fstest.MapFS{}, attest.Limits{}); err != nil || got != nil {
		t.Fatalf("missing directory = %q, %v", got, err)
	}
	_, err = attest.ReadBundleDirectory(fstest.MapFS{
		dir + "a.sigstore.json": {Data: []byte("a")}, dir + "b.sigstore.json": {Data: []byte("b")}, dir + "c.sigstore.json": {Data: []byte("c")},
	}, lim)
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintLimits)
	_, err = attest.ReadBundleDirectory(fstest.MapFS{dir + "a.sigstore.json": {Data: []byte("123456789")}}, lim)
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintLimits)
	_, err = attest.ReadBundleDirectory(fstest.MapFS{dir + "a.sigstore.json/x": {Data: []byte("x")}}, lim)
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintUnreadable)
	_, err = attest.ReadBundleDirectory(fstest.MapFS{".podium/attestations": {Data: []byte("a file")}}, lim)
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintUnreadable)
}

// Spec: §13.11.3 — a bundle file that is a link to a file outside the
// directory is read as the file it names on disk, and a dangling link is
// unreadable.
func TestReadBundleDirectory_Links(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	att := filepath.Join(root, ".podium", "attestations")
	if err := os.MkdirAll(att, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(att, "a.sigstore.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := attest.ReadBundleDirectory(os.DirFS(root), attest.Limits{})
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintUnreadable)
}
