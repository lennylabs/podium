package attest_test

import (
	"crypto/sha256"
	"encoding/json"
	"slices"
	"testing"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/version"
)

// signerDigest is the package digest the signer's podium computes for the
// files of a package, with the resolution that ingest applies.
func signerDigest(t *testing.T, files map[string][]byte) string {
	t.Helper()
	res := manifest.ResolvePackage(manifest.StoredDocuments(files[manifest.ArtifactFile], files[manifest.SkillFile]))
	if !res.Resolved {
		t.Fatalf("package does not resolve: %v", res)
	}
	sums := map[string]attest.Sum{}
	for p, b := range files {
		sums[p] = sha256.Sum256(b)
	}
	return attest.PackageDigest(attest.NewPackage(res, sums))
}

const (
	plainSkillMD = "---\nname: close-books\ndescription: Close the books.\nmetadata:\n  version: 1.2.0\n  sensitivity: medium\n---\n\nClose the books.\n"
	agentMD      = "---\ntype: agent\nversion: 2.0.0\ndescription: Pay an invoice\nsensitivity: low\ndeprecated: true\nreplaced_by: finance/pay2\n---\n\nPay.\n"
)

// servedPlainSkill is the record a registry serves for a plain skill with
// one resource.
func servedPlainSkill(t *testing.T) version.DeliveryRecord {
	t.Helper()
	doc, err := manifest.SynthesizedSkillDocument("1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	return version.DeliveryRecord{
		ID: "finance/close-books", Version: "1.2.0", Type: "skill", Sensitivity: "medium",
		Frontmatter: string(doc), SkillRaw: plainSkillMD, ManifestBody: "Close the books.\n",
		Resources: map[string]string{"scripts/run.sh": attest.FileDigest([]byte("echo\n")), "empty.txt": attest.FileDigest(nil)},
	}
}

// Spec: §4.7.11 (Attestation check, steps 4 and 5) — a served plain skill
// frames into a candidate equal to the digest the signer computed from its
// files, which omit ARTIFACT.md, and an authored ARTIFACT.md equal to the
// synthesized bytes is a further candidate; a zero-length resource is an
// entry.
func TestPackageDigestCandidates_PlainSkill(t *testing.T) {
	t.Parallel()
	rec := servedPlainSkill(t)
	if !attest.IsSynthesizedDocument(rec) {
		t.Fatal("the served document is not the synthesized one")
	}
	cands, err := attest.PackageDigestCandidates(rec, attest.Lifecycle{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("candidates = %v, want 2", cands)
	}
	plain := signerDigest(t, map[string][]byte{"SKILL.md": []byte(plainSkillMD), "scripts/run.sh": []byte("echo\n"), "empty.txt": nil})
	authored := signerDigest(t, map[string][]byte{"ARTIFACT.md": []byte(rec.Frontmatter), "SKILL.md": []byte(plainSkillMD), "scripts/run.sh": []byte("echo\n"), "empty.txt": nil})
	if !slices.Contains(cands, plain) || !slices.Contains(cands, authored) {
		t.Fatalf("candidates %v lack %s or %s", cands, plain, authored)
	}
	// A record whose document is not the synthesized one has the one
	// candidate that includes it.
	rec.Version = "1.2.1"
	if attest.IsSynthesizedDocument(rec) {
		t.Fatal("a document for another version reads as synthesized")
	}
	if cands, _ := attest.PackageDigestCandidates(rec, attest.Lifecycle{}); len(cands) != 1 {
		t.Fatalf("candidates = %v, want 1", cands)
	}
}

// Spec: §4.7.11 (Attestation check, step 5), §4.7.4 — a served replaced_by
// adds a candidate with an empty one, so a declared target and an added one
// both have a candidate, and a served type, sensitivity, or deprecation that
// differs from the resolved value yields no candidate equal to the signed
// digest.
func TestPackageDigestCandidates_ServedFields(t *testing.T) {
	t.Parallel()
	signed := signerDigest(t, map[string][]byte{"ARTIFACT.md": []byte(agentMD)})
	rec := version.DeliveryRecord{ID: "finance/pay", Version: "2.0.0", Type: "agent", Sensitivity: "low", Frontmatter: agentMD, ManifestBody: "Pay.\n"}
	life := attest.Lifecycle{Deprecated: true, ReplacedBy: "finance/pay2"}
	cands, err := attest.PackageDigestCandidates(rec, life)
	if err != nil || len(cands) != 2 || !slices.Contains(cands, signed) {
		t.Fatalf("candidates = %v, %v; want 2 with %s", cands, err, signed)
	}
	for name, mutate := range map[string]func(*version.DeliveryRecord, *attest.Lifecycle){
		"type":                func(r *version.DeliveryRecord, _ *attest.Lifecycle) { r.Type = "rule" },
		"sensitivity low":     func(r *version.DeliveryRecord, _ *attest.Lifecycle) { r.Sensitivity = "high" },
		"deprecated absent":   func(_ *version.DeliveryRecord, l *attest.Lifecycle) { l.Deprecated = false },
		"replaced_by removed": func(_ *version.DeliveryRecord, l *attest.Lifecycle) { l.ReplacedBy = "" },
		"replaced_by changed": func(_ *version.DeliveryRecord, l *attest.Lifecycle) { l.ReplacedBy = "finance/other" },
	} {
		r, l := rec, life
		mutate(&r, &l)
		got, _ := attest.PackageDigestCandidates(r, l)
		if slices.Contains(got, signed) {
			t.Errorf("%s: candidates %v still hold the signed digest", name, got)
		}
	}
	bad := rec
	bad.Resources = map[string]string{"x": "sha256:ABC"}
	_, err = attest.PackageDigestCandidates(bad, life)
	wantFailure(t, err, attest.CodeAttestationMismatch, attest.ConstraintSubject)
}

// Spec: §4.7.11 (Attestation check, step 2) — a resource at ARTIFACT.md, a
// skill's resource at SKILL.md, and a SKILL.md served for another type are
// refused as document_slot.
func TestCheckDocumentSlots(t *testing.T) {
	t.Parallel()
	skill := servedPlainSkill(t)
	if err := attest.CheckDocumentSlots(skill); err != nil {
		t.Fatal(err)
	}
	agent := version.DeliveryRecord{Type: "agent", Resources: map[string]string{"SKILL.md": ""}}
	if err := attest.CheckDocumentSlots(agent); err != nil {
		t.Fatalf("a SKILL.md resource of an agent: %v", err)
	}
	for name, rec := range map[string]version.DeliveryRecord{
		"artifact resource": {Type: "agent", Resources: map[string]string{"ARTIFACT.md": ""}},
		"skill resource":    {Type: "skill", Resources: map[string]string{"SKILL.md": ""}},
		"skill document":    {Type: "agent", SkillRaw: "x"},
	} {
		if err := attest.CheckDocumentSlots(rec); err == nil {
			t.Errorf("%s: accepted", name)
		} else {
			wantFailure(t, err, attest.CodeAttestationMismatch, attest.ConstraintDocumentSlot)
		}
	}
}

// Spec: §4.7.11 (Attestation check, step 3) — the manifest body is the
// derivation of SKILL.md for a skill, of ARTIFACT.md otherwise, and for a
// skill with an empty SKILL.md empty or the derivation of ARTIFACT.md.
func TestCheckManifestBody(t *testing.T) {
	t.Parallel()
	ok := []version.DeliveryRecord{
		servedPlainSkill(t),
		{Type: "agent", Frontmatter: agentMD, ManifestBody: "Pay.\n"},
		{Type: "skill", Frontmatter: agentMD, ManifestBody: ""},
		{Type: "skill", Frontmatter: agentMD, ManifestBody: "Pay.\n"},
		{Type: "rule", Frontmatter: "no frontmatter", ManifestBody: ""},
	}
	for i, rec := range ok {
		if err := attest.CheckManifestBody(rec); err != nil {
			t.Errorf("case %d: %v", i, err)
		}
	}
	bad := []version.DeliveryRecord{
		{Type: "agent", Frontmatter: agentMD, ManifestBody: "Something else.\n"},
		{Type: "skill", SkillRaw: plainSkillMD, Frontmatter: agentMD, ManifestBody: "Pay.\n"},
		{Type: "skill", Frontmatter: agentMD, ManifestBody: "x"},
	}
	for _, rec := range bad {
		wantFailure(t, attest.CheckManifestBody(rec), attest.CodeAttestationMismatch, attest.ConstraintDerivedField)
	}
}

// Spec: §4.7.11 (Attestation check, step 3) — deprecated and replaced_by
// are read by exact name, null is absent, and a member of another JSON type
// is refused as derived_field.
func TestReadLifecycle(t *testing.T) {
	t.Parallel()
	m := func(s string) map[string]json.RawMessage {
		out, err := version.DecodeJSONObject([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	l, err := attest.ReadLifecycle(m(`{"deprecated":true,"replaced_by":"a/b","Deprecated":false}`))
	if err != nil || !l.Deprecated || l.ReplacedBy != "a/b" {
		t.Fatalf("lifecycle = %+v, %v", l, err)
	}
	if l, err := attest.ReadLifecycle(m(`{"deprecated":null,"replaced_by":null}`)); err != nil || l != (attest.Lifecycle{}) {
		t.Fatalf("nulls = %+v, %v", l, err)
	}
	for _, body := range []string{`{"deprecated":"true"}`, `{"replaced_by":1}`, `{"deprecated":1}`, `{"replaced_by":["a"]}`} {
		_, err := attest.ReadLifecycle(m(body))
		wantFailure(t, err, attest.CodeAttestationMismatch, attest.ConstraintDerivedField)
	}
}

// Spec: §4.7.11 (Attestation check, step 6), §4.7.6 — the expected subject
// name is <id>@<version>, the bare ID for a skill under a registry-assigned
// version, and a registry-assigned version on another type or a version of
// another form is refused.
func TestExpectedSubjectName(t *testing.T) {
	t.Parallel()
	name, err := attest.ExpectedSubjectName("finance/pay", version.DeliveryRecord{Type: "agent", Version: "2.0.0"})
	if err != nil || name != "finance/pay@2.0.0" {
		t.Fatalf("name = %q, %v", name, err)
	}
	name, err = attest.ExpectedSubjectName("finance/close", version.DeliveryRecord{Type: "skill", Version: "0.0.0-auto.3"})
	if err != nil || name != "finance/close" {
		t.Fatalf("auto name = %q, %v", name, err)
	}
	_, err = attest.ExpectedSubjectName("finance/pay", version.DeliveryRecord{Type: "agent", Version: "0.0.0-auto.3"})
	wantFailure(t, err, attest.CodeAttestationMismatch, attest.ConstraintAssignedVersion)
	_, err = attest.ExpectedSubjectName("finance/pay", version.DeliveryRecord{Type: "agent", Version: "1.0"})
	wantFailure(t, err, attest.CodeAttestationMismatch, attest.ConstraintVersion)
	_, err = attest.ExpectedSubjectName("a b", version.DeliveryRecord{Type: "agent", Version: "1.0.0"})
	wantFailure(t, err, attest.CodeAttestationMismatch, attest.ConstraintSubject)
}
