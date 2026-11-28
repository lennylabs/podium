package attest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"sort"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/version"
)

// Lifecycle members a consumer reads by exact name for the package digest.
const (
	memberDeprecated = "deprecated"
	memberReplacedBy = "replaced_by"
)

// skillType is the served type whose manifest document is SKILL.md.
const skillType = string(manifest.TypeSkill)

// Lifecycle is the pair of §4.7.4 lifecycle members a consumer frames into
// the package digest, read from the served response: deprecated, false when
// absent, and replaced_by, empty when absent.
type Lifecycle struct {
	Deprecated bool
	ReplacedBy string
}

// ReadLifecycle reads deprecated and replaced_by from the exact-name member
// map of a served response under the §4.7.10 step 2 member rules: a null
// member is absent, an absent deprecated is false, and an absent
// replaced_by is empty. A deprecated that is not a boolean, or a
// replaced_by that is not a string, is a materialize.attestation_mismatch
// Failure with the constraint derived_field.
//
// Spec: §4.7.11 (Attestation check, step 3)
func ReadLifecycle(members map[string]json.RawMessage) (Lifecycle, error) {
	var l Lifecycle
	if raw, ok := members[memberDeprecated]; ok && !isNull(raw) {
		if err := json.Unmarshal(raw, &l.Deprecated); err != nil {
			return Lifecycle{}, fail(CodeAttestationMismatch, ConstraintDerivedField, "the deprecated member is not a boolean", nil)
		}
	}
	if raw, ok := members[memberReplacedBy]; ok && !isNull(raw) {
		if err := json.Unmarshal(raw, &l.ReplacedBy); err != nil {
			return Lifecycle{}, fail(CodeAttestationMismatch, ConstraintDerivedField, "the replaced_by member is not a string", nil)
		}
	}
	return l, nil
}

// isNull reports whether a raw member is the JSON literal null.
func isNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// CheckDocumentSlots applies step 2 of the §4.7.11 attestation check: no
// resource at the path ARTIFACT.md, no resource at the path SKILL.md in a
// skill, and no served SKILL.md that is not empty in a record of another
// type. Paths are compared byte for byte. A failure is a
// materialize.attestation_mismatch Failure with the constraint
// document_slot. The package digest lists files by path, so without the
// step a registry could serve an authored document as a resource and a
// document of its own in the document slot, and the digest would match.
//
// Spec: §4.7.11 (Attestation check, step 2)
func CheckDocumentSlots(rec version.DeliveryRecord) error {
	slot := func(detail string) error {
		return fail(CodeAttestationMismatch, ConstraintDocumentSlot, detail, nil)
	}
	if _, ok := rec.Resources[manifest.ArtifactFile]; ok {
		return slot("the record carries a resource at the path ARTIFACT.md")
	}
	if rec.Type == skillType {
		if _, ok := rec.Resources[manifest.SkillFile]; ok {
			return slot("the skill carries a resource at the path SKILL.md")
		}
		return nil
	}
	if rec.SkillRaw != "" {
		return slot("a record of a type other than skill carries a SKILL.md document")
	}
	return nil
}

// CheckManifestBody applies the body rule of step 3 of the §4.7.11
// attestation check: the served manifest body is the §4.7.10 step 5
// derivation of the served SKILL.md for a skill, and of the served
// ARTIFACT.md document for every other type. For a skill whose served
// SKILL.md is empty the body is empty or the derivation of the served
// ARTIFACT.md document. A failure is a materialize.attestation_mismatch
// Failure with the constraint derived_field. The rule reads bytes and
// parses no YAML.
//
// Spec: §4.7.11 (Attestation check, step 3)
func CheckManifestBody(rec version.DeliveryRecord) error {
	mismatch := func(cause error) error {
		return fail(CodeAttestationMismatch, ConstraintDerivedField, "the manifest body differs from its derivation", cause)
	}
	doc := rec.Frontmatter
	if rec.Type == skillType && rec.SkillRaw != "" {
		doc = rec.SkillRaw
	}
	body, err := manifest.ManifestBodyOf([]byte(doc))
	if err != nil {
		// ManifestBodyOf returns no error today; a future one derives no
		// body, so the record is refused rather than compared.
		return mismatch(err)
	}
	emptySkill := rec.Type == skillType && rec.SkillRaw == "" && rec.ManifestBody == ""
	if rec.ManifestBody != body && !emptySkill {
		return mismatch(nil)
	}
	return nil
}

// IsSynthesizedDocument applies step 4 of the §4.7.11 attestation check: it
// reports whether the served ARTIFACT.md document of a skill equals, byte
// for byte, the §4.3.4 synthesized document for the served version. It is
// false for a record of another type and for a served version that has no
// synthesized document.
//
// Spec: §4.7.11 (Attestation check, step 4), §4.3.4
func IsSynthesizedDocument(rec version.DeliveryRecord) bool {
	if rec.Type != skillType {
		return false
	}
	want, err := manifest.SynthesizedSkillDocument(rec.Version)
	return err == nil && bytes.Equal(want, []byte(rec.Frontmatter))
}

// PackageDigestCandidates applies step 5 of the §4.7.11 attestation check:
// the package digests a record can carry, computed from the verified record
// and the lifecycle members, in the state resolved, with no resource body
// and no YAML decoder. The entries are the served ARTIFACT.md document, the
// served SKILL.md when it is not empty, and each resource under the content
// hash the record frames, a zero-length resource included. It computes
// candidates that omit ARTIFACT.md when step 4 finds the synthesized
// document, because an authored ARTIFACT.md can equal those bytes, and
// candidates with an empty replaced_by beside the served one, because a
// latest load can serve the upgrade target of a newer deprecated version
// (§4.7.4). It returns at most four distinct digests in ascending order. A
// resource content hash that is not sha256: followed by 64 lowercase
// hexadecimal digits yields no digest a subject can list, and is a
// materialize.attestation_mismatch Failure with the constraint subject.
// Step 2 has refused a resource at a document path, so no path enters a
// candidate twice.
//
// Spec: §4.7.11 (Attestation check, step 5), §4.7.6
func PackageDigestCandidates(rec version.DeliveryRecord, life Lifecycle) ([]string, error) {
	entries := make(map[string]Sum, len(rec.Resources)+2)
	for p, h := range rec.Resources {
		sum, err := parseStoredSum(h)
		if err != nil {
			return nil, fail(CodeAttestationMismatch, ConstraintSubject, "a resource content hash is not sha256: and 64 lowercase hexadecimal digits", err)
		}
		entries[p] = sum
	}
	if rec.SkillRaw != "" {
		entries[manifest.SkillFile] = sha256.Sum256([]byte(rec.SkillRaw))
	}
	withDoc := cloneEntries(entries)
	withDoc[manifest.ArtifactFile] = sha256.Sum256([]byte(rec.Frontmatter))
	entrySets := []map[string]Sum{withDoc}
	if IsSynthesizedDocument(rec) {
		entrySets = append(entrySets, entries)
	}
	replacements := []string{life.ReplacedBy}
	if life.ReplacedBy != "" {
		replacements = append(replacements, "")
	}
	seen := map[string]bool{}
	var out []string
	for _, es := range entrySets {
		for _, rb := range replacements {
			d := PackageDigest(Package{Resolved: true, Entries: es, Fields: manifest.ResolvedFields{
				Type:        manifest.ArtifactType(rec.Type),
				Sensitivity: manifest.Sensitivity(rec.Sensitivity),
				Deprecated:  life.Deprecated,
				ReplacedBy:  rb,
			}})
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// cloneEntries returns a copy of an entry map.
func cloneEntries(m map[string]Sum) map[string]Sum {
	out := make(map[string]Sum, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ExpectedSubjectName applies step 6 of the §4.7.11 attestation check: the
// subject name a statement lists for the record answering requestedID,
// which the request binding has shown to be the served ID. It is
// <id>@<version> for a served version of the major.minor.patch form, and
// the bare ID for a skill served under a registry-assigned 0.0.0-auto.<N>
// version, whose subject carries no version. A registry-assigned version on
// another type is a materialize.attestation_mismatch Failure with the
// constraint assigned_version, and a version of any other form one with the
// constraint version. An ID that has no subject name matches no subject,
// which is a Failure with the constraint subject.
//
// Spec: §4.7.11 (Attestation check, step 6), §4.7.6
func ExpectedSubjectName(requestedID string, rec version.DeliveryRecord) (string, error) {
	var v string
	switch {
	case manifest.ValidateVersion(rec.Version) == nil:
		v = rec.Version
	case isAutoVersion(rec.Version) && rec.Type == skillType:
		v = ""
	case isAutoVersion(rec.Version):
		return "", fail(CodeAttestationMismatch, ConstraintAssignedVersion, "a registry-assigned version is served on a type other than skill", nil)
	default:
		return "", fail(CodeAttestationMismatch, ConstraintVersion, "the served version has no subject name form", nil)
	}
	name, err := SubjectName(requestedID, v)
	if err != nil {
		return "", fail(CodeAttestationMismatch, ConstraintSubject, "the requested ID has no subject name", err)
	}
	return name, nil
}

// isAutoVersion reports whether v is a registry-assigned version.
func isAutoVersion(v string) bool {
	_, ok := manifest.IsAutoVersion(v)
	return ok
}

// sortedKeys returns the keys of m in ascending byte-wise order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
