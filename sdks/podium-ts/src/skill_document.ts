// Skill versions and the synthesized ARTIFACT.md of a skill that has none
// (spec §4.3.4, §4.7.6). Mirrors the helpers in pkg/manifest/skill_version.go
// so the overlay serves, and materialize() omits, the same bytes the registry
// does.

// AUTO_VERSION_ZERO is the registry-reserved version a source with no stored
// rows reports for a skill that declares no usable version (§4.7.6, §13.11.3).
export const AUTO_VERSION_ZERO = "0.0.0-auto.0";

const SEMVER_RE = /^[0-9]+\.[0-9]+\.[0-9]+$/;
const AUTO_VERSION_RE = /^0\.0\.0-auto\.(0|[1-9][0-9]*)$/;

// MAX_GO_INT is the largest N manifest.IsAutoVersion accepts: strconv.Atoi
// rejects a larger value as an int overflow on the 64-bit targets.
const MAX_GO_INT = 9223372036854775807n;

// isAutoVersion reports whether v is the reserved 0.0.0-auto.<N> form with an
// N that fits a Go int. Mirrors manifest.IsAutoVersion, which returns false on
// an overflow, so an overflowing N never yields a synthesized document that
// the none adapter would write.
//
// Spec: §4.7.6
function isAutoVersion(v: string): boolean {
  const m = AUTO_VERSION_RE.exec(v);
  return m !== null && BigInt(m[1]) <= MAX_GO_INT;
}

// The bytes are fixed by §4.3.4: every line ends with a single line feed, and
// the document ends with the line feed after the comment.
const SYNTHESIZED_PREFIX = "---\ntype: skill\nversion: ";
const SYNTHESIZED_SUFFIX = "\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n";

// isSemver reports whether v is an authored major.minor.patch version.
// Mirrors manifest.ValidateVersion.
export function isSemver(v: string): boolean {
  return SEMVER_RE.test(v);
}

// resolveSkillVersion returns a skill's version: the ARTIFACT.md version when
// set, then a major.minor.patch SKILL.md metadata.version, and otherwise the
// assigned 0.0.0-auto.0. A metadata.version in any other form is ignored,
// because metadata is free-form in the Agent Skills standard. Mirrors
// manifest.ResolveSkillVersion with the §13.11.3 filesystem-source fallback.
//
// Spec: §4.3.4, §13.11.3
export function resolveSkillVersion(artifactVersion: string, metadataVersion: string): string {
  if (artifactVersion) return artifactVersion;
  if (isSemver(metadataVersion)) return metadataVersion;
  return AUTO_VERSION_ZERO;
}

// synthesizedSkillDocument returns the ARTIFACT.md bytes served for a skill
// with no ARTIFACT.md at version v. It throws for a v that is neither a
// major.minor.patch version nor the reserved 0.0.0-auto.<N> form, so no value
// can inject a key or a line into the document. Mirrors
// manifest.SynthesizedSkillDocument.
//
// Spec: §4.3.4
export function synthesizedSkillDocument(v: string): string {
  if (!isSemver(v) && !isAutoVersion(v)) {
    throw new Error(`invalid version: ${JSON.stringify(v)}`);
  }
  return SYNTHESIZED_PREFIX + v + SYNTHESIZED_SUFFIX;
}

// isSynthesizedSkillDocument reports whether doc is exactly the synthesized
// ARTIFACT.md for the version its own version line names. It compares bytes
// and parses no YAML, so a document that differs in any byte, such as a
// reordered key or a missing comment, reports false. Mirrors
// manifest.IsSynthesizedSkillDocument.
//
// Spec: §4.3.4
export function isSynthesizedSkillDocument(doc: string): boolean {
  if (!doc.startsWith(SYNTHESIZED_PREFIX) || !doc.endsWith(SYNTHESIZED_SUFFIX)) return false;
  const v = doc.slice(SYNTHESIZED_PREFIX.length, doc.length - SYNTHESIZED_SUFFIX.length);
  try {
    return synthesizedSkillDocument(v) === doc;
  } catch {
    return false;
  }
}
