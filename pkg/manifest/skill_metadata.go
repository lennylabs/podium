package manifest

import (
	"fmt"
	"strings"
)

// skillMetadataFields lists the Podium fields a skill can carry as strings in
// SKILL.md metadata, each under the key that names the field. The order is the
// order of the §4.3.4 encoding table, and every list this file returns follows
// it, so lint warnings and filled-key lists are deterministic.
//
// Spec: §4.3.4
var skillMetadataFields = []string{
	"when_to_use",
	"tags",
	"sensitivity",
	"search_visibility",
	"deprecated",
	"replaced_by",
	"release_notes",
}

// skillMetadataCodec decodes one metadata-eligible field. decode reports
// whether the value follows the field's encoding and, when it does, writes the
// decoded value into the artifact. assign copies the field from src to dst.
type skillMetadataCodec struct {
	encoding string
	decode   func(v string, out *Artifact) bool
	assign   func(dst, src *Artifact)
}

// skillMetadataCodecs holds the codec of every key in skillMetadataFields.
var skillMetadataCodecs = map[string]skillMetadataCodec{
	"when_to_use": {
		encoding: "one situation per line, with at least one non-empty line",
		decode:   decodeWhenToUse,
		assign:   func(dst, src *Artifact) { dst.WhenToUse = src.WhenToUse },
	},
	"tags": {
		encoding: "comma-separated, with no empty element and no line break",
		decode:   decodeTags,
		assign:   func(dst, src *Artifact) { dst.Tags = src.Tags },
	},
	"sensitivity": {
		encoding: "low, medium, or high",
		decode:   decodeSensitivity,
		assign:   func(dst, src *Artifact) { dst.Sensitivity = src.Sensitivity },
	},
	"search_visibility": {
		encoding: "indexed or direct-only",
		decode:   decodeSearchVisibility,
		assign:   func(dst, src *Artifact) { dst.SearchVisibility = src.SearchVisibility },
	},
	"deprecated": {
		encoding: "true or false",
		decode:   decodeDeprecated,
		assign:   func(dst, src *Artifact) { dst.Deprecated = src.Deprecated },
	},
	"replaced_by": {
		encoding: "one line",
		decode:   decodeReplacedBy,
		assign:   func(dst, src *Artifact) { dst.ReplacedBy = src.ReplacedBy },
	},
	"release_notes": {
		encoding: "any string",
		decode:   decodeReleaseNotes,
		assign:   func(dst, src *Artifact) { dst.ReleaseNotes = src.ReleaseNotes },
	},
}

// IsSkillMetadataField reports whether key names a Podium field that SKILL.md
// metadata can carry.
//
// Spec: §4.3.4
func IsSkillMetadataField(key string) bool {
	_, ok := skillMetadataCodecs[key]
	return ok
}

// SkillMetadataIssue describes a metadata value that does not follow its
// field's encoding. Encoding is the human-readable encoding a lint warning
// prints beside the key and the value.
type SkillMetadataIssue struct {
	Key      string
	Value    string
	Encoding string
}

// SkillMetadataFields is the decoding of a SKILL.md's metadata-eligible keys.
// Values holds each decoded field. Decoded holds every key whose value
// decoded, so a decoded deprecated "false" is distinguishable from an absent
// key. Issues lists the values that do not follow their encoding.
type SkillMetadataFields struct {
	Values  Artifact
	Decoded map[string]bool
	Issues  []SkillMetadataIssue
}

// DecodeSkillMetadata decodes the metadata-eligible keys of s by the §4.3.4
// encodings. An empty value, which a YAML null decodes to, is absent and
// raises no issue. A nil s returns empty Values and an empty, non-nil Decoded.
//
// Spec: §4.3.4
func DecodeSkillMetadata(s *Skill) SkillMetadataFields {
	out := SkillMetadataFields{Decoded: map[string]bool{}}
	if s == nil {
		return out
	}
	for _, key := range skillMetadataFields {
		v := s.Metadata[key]
		if v == "" {
			continue
		}
		c := skillMetadataCodecs[key]
		if !c.decode(v, &out.Values) {
			out.Issues = append(out.Issues, SkillMetadataIssue{Key: key, Value: v, Encoding: c.encoding})
			continue
		}
		out.Decoded[key] = true
	}
	return out
}

// StatedKeys returns the set of top-level keys an ARTIFACT.md frontmatter
// states, whatever their values. A key stated through a merge key or an alias
// key counts under the name ParseArtifact decodes, because ParseArtifact
// applies the merge or the alias, and a key missing from the set would let
// metadata overwrite the ARTIFACT.md value. Empty input states nothing and
// returns a nil map and no error.
//
// Spec: §4.3.4
func StatedKeys(artifactMD []byte) (map[string]bool, error) {
	if len(artifactMD) == 0 {
		return nil, nil
	}
	all, err := frontmatterKeyMap(artifactMD)
	if err != nil {
		return nil, fmt.Errorf("manifest: stated keys: %w", err)
	}
	stated := make(map[string]bool, len(all))
	for k := range all {
		stated[k] = true
	}
	return stated, nil
}

// ResolveSkillFields resolves a skill's fields into a. It sets a.Version by
// ResolveSkillVersion, then fills each metadata-eligible field from SKILL.md
// metadata when stated does not hold the field's key and the metadata value
// decodes. It returns the filled keys in the §4.3.4 table order.
//
// Precedence is by key presence: a key ARTIFACT.md states keeps its
// ARTIFACT.md value whatever that value is, so an explicit deprecated: false,
// sensitivity: "", or tags: [] wins over metadata. A zero-value rule cannot
// express that, which is why the caller passes the raw frontmatter keys
// (StatedKeys) rather than letting this function infer presence from a. The
// function writes resolved values into a, so lint compares the authored
// ARTIFACT.md values with DecodeSkillMetadata and never the resolved artifact.
// A nil a is a no-op that returns nil.
//
// Spec: §4.3.4
func ResolveSkillFields(a *Artifact, stated map[string]bool, s *Skill) []string {
	if a == nil {
		return nil
	}
	a.Version = ResolveSkillVersion(a, s)
	dec := DecodeSkillMetadata(s)
	var filled []string
	for _, key := range skillMetadataFields {
		if stated[key] || !dec.Decoded[key] {
			continue
		}
		skillMetadataCodecs[key].assign(a, &dec.Values)
		filled = append(filled, key)
	}
	return filled
}

// ResolveStoredSkill resolves a stored skill row from its two byte strings.
// With empty artifactMD and a SKILL.md that parses, the base artifact is a
// plain skill carrying the SKILL.md description and no key is stated, which
// matches how the registry reads a plain-skill row. With an ARTIFACT.md that
// parses as type: skill and a SKILL.md that parses, the stated keys come from
// the ARTIFACT.md frontmatter. Every other input returns ok false.
//
// Spec: §4.3.4, §13.4
func ResolveStoredSkill(artifactMD, skillMD []byte) (a *Artifact, s *Skill, filled []string, ok bool) {
	s, err := ParseSkill(skillMD)
	if err != nil {
		return nil, nil, nil, false
	}
	if len(artifactMD) == 0 {
		a = &Artifact{Type: TypeSkill, Description: s.Description}
		return a, s, ResolveSkillFields(a, nil, s), true
	}
	a, err = ParseArtifact(artifactMD)
	if err != nil || a.Type != TypeSkill {
		return nil, nil, nil, false
	}
	stated, err := StatedKeys(artifactMD)
	if err != nil {
		// ParseArtifact decoded the same frontmatter, so this branch runs
		// only if the two decoders ever disagree; refuse rather than resolve
		// with no stated keys, which would let metadata override ARTIFACT.md.
		return nil, nil, nil, false
	}
	return a, s, ResolveSkillFields(a, stated, s), true
}

// decodeWhenToUse reads one situation per line, trimming each line and
// dropping empty ones. At least one line must remain.
func decodeWhenToUse(v string, out *Artifact) bool {
	var lines []string
	for _, line := range strings.Split(v, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return false
	}
	out.WhenToUse = lines
	return true
}

// decodeTags reads a comma-separated list with no line break, trimming each
// element. An empty element fails the encoding.
func decodeTags(v string, out *Artifact) bool {
	if hasLineBreak(v) {
		return false
	}
	parts := strings.Split(v, ",")
	tags := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return false
		}
		tags = append(tags, p)
	}
	out.Tags = tags
	return true
}

func decodeSensitivity(v string, out *Artifact) bool {
	switch s := Sensitivity(v); s {
	case SensitivityLow, SensitivityMedium, SensitivityHigh:
		out.Sensitivity = s
		return true
	}
	return false
}

func decodeSearchVisibility(v string, out *Artifact) bool {
	switch sv := SearchVisibility(v); sv {
	case SearchVisibilityIndexed, SearchVisibilityDirectOnly:
		out.SearchVisibility = sv
		return true
	}
	return false
}

func decodeDeprecated(v string, out *Artifact) bool {
	switch v {
	case "true":
		out.Deprecated = true
		return true
	case "false":
		out.Deprecated = false
		return true
	}
	return false
}

func decodeReplacedBy(v string, out *Artifact) bool {
	if hasLineBreak(v) {
		return false
	}
	out.ReplacedBy = v
	return true
}

func decodeReleaseNotes(v string, out *Artifact) bool {
	out.ReleaseNotes = v
	return true
}

// hasLineBreak reports whether v holds a line feed or a carriage return.
func hasLineBreak(v string) bool {
	return strings.ContainsAny(v, "\r\n")
}
