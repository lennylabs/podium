package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
)

// attestPredicate is the predicate type the attestation fixtures sign.
const attestPredicate = "https://slsa.dev/provenance/v1"

// signerRule is a policy rule of ids under mode with one requirement of the
// harness signer identity.
func signerRule(mode, identity string, ids ...string) string {
	q := make([]string, len(ids))
	for i, id := range ids {
		q[i] = `"` + id + `"`
	}
	return `{"ids":[` + strings.Join(q, ",") + `],"mode":"` + mode + `","requirements":[{"predicate_type":"` + attestPredicate +
		`","signers":[{"issuer":"` + sh.DefaultIssuer + `","identity":"` + identity + `","instance":"github"}]}]}`
}

// testAttestation returns an AttestationFunc whose policy is one user-source
// file of rules with the github instance of the harness's root.
func testAttestation(t *testing.T, h *sh.Harness, def attest.Mode, rules ...string) (*Attestation, AttestationFunc) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "github-root.json"), h.TrustedRootJSON(sh.GitHubStyleRoot()...), 0o600); err != nil {
		t.Fatal(err)
	}
	text := `{"version":1,"instances":[{"name":"github","trusted_root_file":"github-root.json","transparency_log":"none","certificate_transparency":"none"}],"rules":[` +
		strings.Join(rules, ",") + `]}`
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := attest.LoadPolicyFile(path, sign.AttestationInstance{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p, err := attest.NewPolicy(attest.Source{Class: attest.UserSource, Name: "test", DefaultMode: def, Files: []*attest.PolicyFile{f}})
	if err != nil {
		t.Fatal(err)
	}
	att := &Attestation{Policy: p, LayerRoots: map[string]string{}}
	return att, func() (*Attestation, error) { return att, nil }
}

// signLayer writes a bundle of the harness signer into the layer at dir that
// lists every package subject and the layer subject layerName, as
// `podium attest` would, and returns the bundle.
func signLayer(t *testing.T, h *sh.Harness, dir, layerName string) []byte {
	t.Helper()
	content, err := filesystem.LayerContent(filesystem.Layer{ID: filepath.Base(dir), Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	subjects := []sh.Subject{{Name: layerName, SHA256: strings.TrimPrefix(attest.LayerDigest(content), "sha256:")}}
	for _, s := range content.Subjects {
		subjects = append(subjects, sh.Subject{Name: s.Name, SHA256: strings.TrimPrefix(s.Digest, "sha256:")})
	}
	bundle := h.DSSEBundle(sh.Statement(t, attestPredicate, subjects...),
		sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(h.Clock().Add(time.Minute))))
	writeBundle(t, dir, bundle)
	return bundle
}

// writeBundle writes a bundle file into the layer's attestation directory.
func writeBundle(t *testing.T, dir string, bundle []byte) {
	t.Helper()
	att := filepath.Join(dir, ".podium", "attestations")
	if err := os.MkdirAll(att, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(att, attest.BundleFileName(bundle)), bundle, 0o644); err != nil {
		t.Fatal(err)
	}
}

// twoAgentLayer writes a single-layer registry with finance/ap/pay and
// finance/close/books.
func twoAgentLayer(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "team")
	testharness.WriteTree(t, dir,
		testharness.WriteTreeOption{Path: "finance/ap/pay/ARTIFACT.md", Content: agentArtifactSrc},
		testharness.WriteTreeOption{Path: "finance/close/books/ARTIFACT.md", Content: agentArtifactSrc},
	)
	return dir
}

// dropOf returns the drop of id, or fails.
func dropOf(t *testing.T, res *Result, id string) layer.Drop {
	t.Helper()
	for _, d := range res.Dropped {
		if d.ArtifactID == id {
			return d
		}
	}
	t.Fatalf("no drop of %s in %+v", id, res.Dropped)
	return layer.Drop{}
}

// lockEntriesOf returns the lock entries of id.
func lockEntriesOf(t *testing.T, target, id string) []LockArtifact {
	t.Helper()
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = %v, %v", lock, err)
	}
	var out []LockArtifact
	for _, la := range lock.Artifacts {
		if la.ID == id {
			out = append(out, la)
		}
	}
	return out
}

// Spec: §13.11.3, §7.5.3 (Attestation members) — a filesystem-source sync
// under require writes each artifact that a bundle of its rule's signer
// attests together with the layer subject, and its lock entries record the
// package digest of the files the walk read and the accepted bundle with
// the signer the sync verified.
func TestRun_FilesystemAttestationRequirePasses(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	reg := twoAgentLayer(t)
	bundle := signLayer(t, h, reg, ".podium/layer")
	_, f := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/**"))
	target := t.TempDir()
	res, err := Run(Options{RegistryPath: reg, Target: target, Attestation: f})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dropped) != 0 || len(res.Artifacts) != 2 {
		t.Fatalf("result = %+v", res)
	}
	entries := lockEntriesOf(t, target, "finance/ap/pay")
	content, err := filesystem.LayerContent(filesystem.Layer{ID: "team", Path: reg})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].PackageDigest != content.Subjects[0].Digest || len(entries[0].Attestations) != 1 {
		t.Fatalf("lock entries = %+v, want the digest %s", entries, content.Subjects[0].Digest)
	}
	a := entries[0].Attestations[0]
	if a.Digest != attest.AttestationDigest(bundle) || a.PredicateType != attestPredicate ||
		a.Signer != (LockSigner{Instance: "github", Issuer: sh.DefaultIssuer, Identity: sh.DefaultSAN}) {
		t.Fatalf("lock attestation = %+v", a)
	}
	lock, _ := ReadLock(target)
	if lock.Version != LockVersion {
		t.Fatalf("lock version = %d", lock.Version)
	}
}

// Spec: §7.5 (Author attestation), §13.11.3 — a record the attestation
// check refuses under require is skipped: nothing of it is written, its
// previous output and lock entries stay, it is listed among the dropped
// artifacts with its code, and every other record is written. Under warn the
// record is written and the failure is a warning.
func TestRun_FilesystemAttestationRefusalKeepsPreviousOutput(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	reg := twoAgentLayer(t)
	signLayer(t, h, reg, ".podium/layer")
	target := t.TempDir()
	if _, err := Run(Options{RegistryPath: reg, Target: target}); err != nil {
		t.Fatalf("first run with no policy: %v", err)
	}
	before := lockEntriesOf(t, target, "finance/close/books")
	if len(before) == 0 {
		t.Fatal("the first run recorded no entry for finance/close/books")
	}
	_, f := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/ap/**"),
		signerRule("require", "bob@acme.com", "finance/close/**"))
	res, err := Run(Options{RegistryPath: reg, Target: target, Attestation: f})
	if err != nil {
		t.Fatal(err)
	}
	d := dropOf(t, res, "finance/close/books")
	if d.Code != attest.CodeAttestationUntrusted || d.Details["constraint"] != attest.ConstraintSigner || !d.PreviousOutputKept {
		t.Fatalf("drop = %+v", d)
	}
	after := lockEntriesOf(t, target, "finance/close/books")
	if len(after) != len(before) || after[0].MaterializedPath != before[0].MaterializedPath {
		t.Fatalf("lock entries = %+v, want %+v kept", after, before)
	}
	if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(before[0].MaterializedPath))); err != nil {
		t.Fatalf("the refused record's previous file: %v", err)
	}
	if got := lockEntriesOf(t, target, "finance/ap/pay"); len(got) == 0 || got[0].PackageDigest == "" {
		t.Fatalf("the attested record's entries = %+v", got)
	}

	_, warn := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/ap/**"),
		signerRule("warn", "bob@acme.com", "finance/close/**"))
	res, err = Run(Options{RegistryPath: reg, Target: target, Attestation: warn})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dropped) != 0 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "finance/close/books: materialize.attestation_untrusted (constraint signer)") {
		t.Fatalf("warn run = dropped %+v warnings %q", res.Dropped, res.Warnings)
	}
	if got := lockEntriesOf(t, target, "finance/close/books"); len(got) == 0 || got[0].PackageDigest != "" || len(got[0].Attestations) != 0 {
		t.Fatalf("a record written with a warning records no accepted bundle: %+v", got)
	}
}

// Spec: §13.11.3 (Layer subject) — a directory signed under another root
// is refused with layer_digest until defaults.attestation.layer_roots names
// that root for the layer, and a root the trust_layer_root rule refuses
// fails the load with config.attestation_policy_unavailable.
func TestRun_FilesystemAttestationLayerRoots(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	reg := twoAgentLayer(t)
	signLayer(t, h, reg, ".podium/layer/agents")
	att, f := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/**"))
	res, err := Run(Options{RegistryPath: reg, Target: t.TempDir(), Attestation: f})
	if err != nil {
		t.Fatal(err)
	}
	if d := dropOf(t, res, "finance/ap/pay"); d.Code != attest.CodeAttestationMismatch || d.Details["constraint"] != attest.ConstraintLayerDigest {
		t.Fatalf("drop = %+v", d)
	}
	att.LayerRoots["team"] = "agents"
	if res, err = Run(Options{RegistryPath: reg, Target: t.TempDir(), Attestation: f}); err != nil || len(res.Dropped) != 0 {
		t.Fatalf("with layer_roots: %+v, %v", res, err)
	}
	att.LayerRoots["team"] = "../x"
	_, err = Run(Options{RegistryPath: reg, Target: t.TempDir(), Attestation: f})
	var cfgErr *AttestationConfigError
	if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), attest.CodePolicyUnavailable) {
		t.Fatalf("an invalid root = %v", err)
	}
}

// Spec: §13.11.3 (Bundles) — a layer whose attestation directory holds more
// files than PODIUM_ATTESTATION_MAX_BUNDLES refuses each artifact checked
// above none with limits, and an unsigned layer is missing.
func TestRun_FilesystemAttestationLimitsAndMissing(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	reg := twoAgentLayer(t)
	att, f := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/**"))
	res, err := Run(Options{RegistryPath: reg, Target: t.TempDir(), Attestation: f})
	if err != nil {
		t.Fatal(err)
	}
	if d := dropOf(t, res, "finance/ap/pay"); d.Code != attest.CodeAttestationMissing {
		t.Fatalf("unsigned = %+v", d)
	}
	signLayer(t, h, reg, ".podium/layer")
	signLayer(t, h, reg, ".podium/layer/other")
	att.Limits = attest.Limits{MaxBundles: 1}
	res, err = Run(Options{RegistryPath: reg, Target: t.TempDir(), Attestation: f})
	if err != nil {
		t.Fatal(err)
	}
	if d := dropOf(t, res, "finance/close/books"); d.Code != attest.CodeAttestationInvalid || d.Details["constraint"] != attest.ConstraintLimits {
		t.Fatalf("over the limit = %+v", d)
	}
}

// Spec: §7.5.3 (Signer changes) — a signer that differs from the one the
// prior lock entries record is reported, in a dry run as well, and refused
// under pin_signers with pinned_signer, keeping the previous output.
func TestRun_SignerChangesAndPinning(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	reg := twoAgentLayer(t)
	signLayer(t, h, reg, ".podium/layer")
	att, f := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/**"))
	target := t.TempDir()
	if _, err := Run(Options{RegistryPath: reg, Target: target, Attestation: f}); err != nil {
		t.Fatal(err)
	}
	res, err := Run(Options{RegistryPath: reg, Target: target, Attestation: f})
	if err != nil || len(res.SignerChanges) != 0 {
		t.Fatalf("an unchanged signer = %+v, %v", res.SignerChanges, err)
	}
	// Forge the prior signer, as a run under another configuration recorded.
	lock, err := ReadLock(target)
	if err != nil {
		t.Fatal(err)
	}
	for i := range lock.Artifacts {
		for j := range lock.Artifacts[i].Attestations {
			lock.Artifacts[i].Attestations[j].Signer.Identity = "carol@acme.com"
		}
	}
	if err := WriteLock(target, lock); err != nil {
		t.Fatal(err)
	}
	res, err = Run(Options{RegistryPath: reg, Target: target, Attestation: f, DryRun: true})
	if err != nil || len(res.SignerChanges) != 2 || len(res.SignerChanges[0].Added) != 1 || len(res.SignerChanges[0].Removed) != 1 {
		t.Fatalf("dry run = %+v, %v", res.SignerChanges, err)
	}
	if s := res.SignerChanges[0].String(); !strings.Contains(s, "carol@acme.com") || !strings.Contains(s, sh.DefaultSAN) {
		t.Fatalf("change text = %q", s)
	}
	att.PinSigners = true
	res, err = Run(Options{RegistryPath: reg, Target: target, Attestation: f})
	if err != nil {
		t.Fatal(err)
	}
	d := dropOf(t, res, "finance/ap/pay")
	if d.Code != attest.CodeAttestationUntrusted || d.Details["constraint"] != attest.ConstraintPinnedSigner || !d.PreviousOutputKept {
		t.Fatalf("pinned = %+v", d)
	}
	if got := lockEntriesOf(t, target, "finance/ap/pay"); len(got) == 0 || got[0].Attestations[0].Signer.Identity != "carol@acme.com" {
		t.Fatalf("the refused record's entries are not kept: %+v", got)
	}
}

// Spec: §7.5.3 — a key-based signer renders by its key identifier.
func TestLockSigner_String(t *testing.T) {
	t.Parallel()
	if got := (LockSigner{Identity: "SHA256:abc"}).String(); got != "key SHA256:abc" {
		t.Fatalf("key signer = %q", got)
	}
	if got := (LockSigner{Instance: "github", Issuer: "i", Identity: "a"}).String(); got != "github i a" {
		t.Fatalf("certificate signer = %q", got)
	}
}

// Spec: §13.11.3, §4.7.11 (Well-formed requests and records) — under no
// policy a package whose ID holds a segment in the 8.3 short-name form,
// which Windows resolves to another directory, is refused with id_form and
// nothing of it is written, while the other packages are.
func TestRun_FilesystemWellFormedRuleUnderNone(t *testing.T) {
	t.Parallel()
	reg := filepath.Join(t.TempDir(), "team")
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: "personal/CLOSE-~1/ARTIFACT.md", Content: agentArtifactSrc},
		testharness.WriteTreeOption{Path: "ok/a/ARTIFACT.md", Content: agentArtifactSrc},
	)
	target := t.TempDir()
	res, err := Run(Options{RegistryPath: reg, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if d := dropOf(t, res, "personal/CLOSE-~1"); d.Code != attest.CodeRequestMismatch || d.Details["constraint"] != attest.ConstraintIDForm {
		t.Fatalf("drop = %+v", d)
	}
	if _, err := os.Stat(filepath.Join(target, "personal")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("personal/CLOSE-~1 written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "ok", "a", "ARTIFACT.md")); err != nil {
		t.Fatalf("the other package: %v", err)
	}
}

// Spec: §7.5 (Author attestation), §6.4 — an overlay package that breaks the
// well-formed rule is skipped with a warning, an overlay artifact whose ID the
// user sources resolve to require is not applied and the registry's record
// is checked instead, overlay: allow applies it, and overlay: deny applies no
// overlay artifact.
func TestRun_OverlayUnderPolicy(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	reg := twoAgentLayer(t)
	signLayer(t, h, reg, ".podium/layer")
	overlay := t.TempDir()
	const overlayBody = "---\ntype: agent\nversion: 9.9.9\ndescription: overlay\n---\n\nOverlay.\n"
	testharness.WriteTree(t, overlay,
		testharness.WriteTreeOption{Path: "finance/ap/pay/ARTIFACT.md", Content: overlayBody},
		testharness.WriteTreeOption{Path: "personal/CLOSE-~1/ARTIFACT.md", Content: overlayBody},
		testharness.WriteTreeOption{Path: "personal/note/ARTIFACT.md", Content: overlayBody},
	)
	att, f := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/**"))
	run := func() *Result {
		t.Helper()
		res, err := Run(Options{RegistryPath: reg, Target: t.TempDir(), OverlayPath: overlay, Attestation: f, DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	versions := func(res *Result) map[string]string {
		out := map[string]string{}
		for _, a := range res.Artifacts {
			out[a.ID] = a.Version
		}
		return out
	}
	res := run()
	v := versions(res)
	if v["finance/ap/pay"] != "1.0.0" || v["personal/note"] != "9.9.9" || v["personal/CLOSE-~1"] != "" || len(res.Warnings) != 2 {
		t.Fatalf("default = %v, warnings %q", v, res.Warnings)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "finance/ap/pay: materialize.attestation_untrusted (constraint overlay)") {
		t.Fatalf("warnings = %q", res.Warnings)
	}
	att.Overlay = OverlayAllow
	if v := versions(run()); v["finance/ap/pay"] != "9.9.9" {
		t.Fatalf("allow = %v", v)
	}
	att.Overlay = OverlayDeny
	if v := versions(run()); v["finance/ap/pay"] != "1.0.0" || v["personal/note"] != "" {
		t.Fatalf("deny = %v", v)
	}
}

// Spec: §7.5 (Author attestation) — a nil AttestationFunc and a nil result
// are a sync with no policy, and a load error is a configuration error that
// keeps its message.
func TestResolveAttestation(t *testing.T) {
	t.Parallel()
	if a, err := resolveAttestation(nil); err != nil || a.Policy != nil {
		t.Fatalf("nil func = %+v, %v", a, err)
	}
	if a, err := resolveAttestation(func() (*Attestation, error) { return nil, nil }); err != nil || a == nil {
		t.Fatalf("nil result = %+v, %v", a, err)
	}
	_, err := resolveAttestation(func() (*Attestation, error) { return nil, errors.New("config.attestation_policy_unavailable: x") })
	var cfgErr *AttestationConfigError
	if !errors.As(err, &cfgErr) || err.Error() != "config.attestation_policy_unavailable: x" || cfgErr.Unwrap() == nil {
		t.Fatalf("load error = %v", err)
	}
	wrapped := &AttestationConfigError{Err: errors.New("y")}
	if _, err := resolveAttestation(func() (*Attestation, error) { return nil, wrapped }); !errors.Is(err, wrapped) {
		t.Fatalf("a config error passes through: %v", err)
	}
}

// Spec: §7.5 — refusalDrop gives an error with no Failure the invalid code.
func TestRefusalDrop_PlainError(t *testing.T) {
	t.Parallel()
	d := refusalDrop("a", errors.New("boom"))
	if d.Code != attest.CodeAttestationInvalid || d.Details != nil {
		t.Fatalf("drop = %+v", d)
	}
}

// Spec: §7.5.3 — lockAttestations keeps one item per bundle and signer.
func TestLockAttestations_Dedupes(t *testing.T) {
	t.Parallel()
	a := attest.AcceptedBundle{Digest: "sha256:1", Instance: "github", Issuer: "i", Identity: "x"}
	if got := lockAttestations([]attest.AcceptedBundle{a, a}); len(got) != 1 {
		t.Fatalf("items = %+v", got)
	}
}

// Spec: §7.5 — checkSelected passes a record with no check, which is a
// workspace overlay record, without reading it.
func TestCheckSelected_OverlayRecord(t *testing.T) {
	t.Parallel()
	res := &Result{}
	kept, refused, _ := checkSelected(context.Background(), []materialRecord{{ID: "a"}}, nil, &Attestation{}, res)
	if len(kept) != 1 || len(refused) != 0 {
		t.Fatalf("kept %+v refused %v", kept, refused)
	}
}
