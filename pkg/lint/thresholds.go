package lint

import (
	"strconv"
	"strings"
)

// Default similarity thresholds for the §7.3.1 catalog advisories.
const (
	// DefaultDescriptionSimilarity is the word-set similarity at or above
	// which two descriptions draw lint.colliding_descriptions.
	DefaultDescriptionSimilarity = 0.7
	// DefaultContentSimilarity is the five-word shingle-set similarity at
	// or above which two content units draw lint.near_duplicate_content.
	DefaultContentSimilarity = 0.8
)

// Similarity thresholds are accepted in [minSimilarity, maxSimilarity]
// (§13.12). Below 0.5 the overlap checks report pairs that share less than
// half their vocabulary, which is noise rather than a collision.
const (
	minSimilarity = 0.5
	maxSimilarity = 1.0
)

// Thresholds holds the configurable lint thresholds. A zero field takes
// its default, so a zero or partly filled Thresholds keeps the default for
// every field left unset. A non-positive integer field and a similarity
// outside [0.5, 1.0] also take the default.
//
// Spec: §4.1, §4.3.4, §4.5.5, §7.3.1, §13.12
type Thresholds struct {
	// PerFileSoftCapBytes is the §4.1 per-file bundled-resource soft cap.
	// A resource above it draws a warning.
	PerFileSoftCapBytes int64
	// PerPackageSoftCapBytes is the §4.1 per-package bundled-resource cap.
	// A package whose resources total more draws an error.
	PerPackageSoftCapBytes int64
	// SkillBodyWarnTokens is the §4.3.4 SKILL.md body warning threshold,
	// in estimated tokens (§4.1).
	SkillBodyWarnTokens int
	// SkillBodyWarnLines is the §4.3.4 SKILL.md body warning threshold,
	// in lines.
	SkillBodyWarnLines int
	// ManifestErrTokens is the §4.1 manifest cap, in estimated tokens.
	// Content above it draws an error.
	ManifestErrTokens int
	// DomainBodyWarnTokens is the §4.5.5 DOMAIN.md body warning
	// threshold, in estimated tokens.
	DomainBodyWarnTokens int
	// MinDescriptionChars is the §7.3.1 thin-description bound, in code
	// points.
	MinDescriptionChars int
	// MinDescriptionWords is the §7.3.1 thin-description bound, in
	// white-space-separated words.
	MinDescriptionWords int
	// DescriptionSimilarity is the §7.3.1 description overlap threshold.
	DescriptionSimilarity float64
	// ContentSimilarity is the §7.3.1 near-duplicate content threshold.
	ContentSimilarity float64
}

// DefaultThresholds returns the spec default for every threshold.
//
// Spec: §13.12
func DefaultThresholds() Thresholds {
	return Thresholds{
		PerFileSoftCapBytes:    PerFileSoftCapBytes,
		PerPackageSoftCapBytes: PerPackageSoftCapBytes,
		SkillBodyWarnTokens:    SkillBodyWarnTokens,
		SkillBodyWarnLines:     SkillBodyWarnLines,
		ManifestErrTokens:      ManifestErrTokens,
		DomainBodyWarnTokens:   DomainBodyWarnTokens,
		MinDescriptionChars:    MinDescriptionChars,
		MinDescriptionWords:    MinDescriptionWords,
		DescriptionSimilarity:  DefaultDescriptionSimilarity,
		ContentSimilarity:      DefaultContentSimilarity,
	}
}

// resolved replaces each unusable field with its default. Each field is
// resolved on its own: a whole-struct zero check would leave the unset
// fields of a partly filled value at zero, which turns off the
// thin-description check and makes every description pair collide.
func (t Thresholds) resolved() Thresholds {
	d := DefaultThresholds()
	positiveOr(&t.PerFileSoftCapBytes, d.PerFileSoftCapBytes)
	positiveOr(&t.PerPackageSoftCapBytes, d.PerPackageSoftCapBytes)
	positiveOr(&t.SkillBodyWarnTokens, d.SkillBodyWarnTokens)
	positiveOr(&t.SkillBodyWarnLines, d.SkillBodyWarnLines)
	positiveOr(&t.ManifestErrTokens, d.ManifestErrTokens)
	positiveOr(&t.DomainBodyWarnTokens, d.DomainBodyWarnTokens)
	positiveOr(&t.MinDescriptionChars, d.MinDescriptionChars)
	positiveOr(&t.MinDescriptionWords, d.MinDescriptionWords)
	if !validSimilarity(t.DescriptionSimilarity) {
		t.DescriptionSimilarity = d.DescriptionSimilarity
	}
	if !validSimilarity(t.ContentSimilarity) {
		t.ContentSimilarity = d.ContentSimilarity
	}
	return t
}

func positiveOr[T int | int64](v *T, def T) {
	if *v <= 0 {
		*v = def
	}
}

// validSimilarity reports whether s lies in [0.5, 1.0]. The comparison is
// written so that NaN fails it.
func validSimilarity(s float64) bool {
	return s >= minSimilarity && s <= maxSimilarity
}

// ThresholdsFromEnv reads the PODIUM_LINT_* variables through getenv.
// An unset or empty variable keeps the default silently. A value that does
// not parse, or that falls outside its range, keeps the default and logs
// one line through logf naming the variable. Registry ingest and
// `podium lint` both build their thresholds here, so the two apply the
// same values.
//
// Spec: §13.12
func ThresholdsFromEnv(getenv func(string) string, logf func(format string, args ...any)) Thresholds {
	e := thresholdEnv{getenv: getenv, logf: logf}
	d := DefaultThresholds()
	return Thresholds{
		PerFileSoftCapBytes:    e.positive("PODIUM_LINT_PER_FILE_SOFT_CAP_BYTES", 64, d.PerFileSoftCapBytes),
		PerPackageSoftCapBytes: e.positive("PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES", 64, d.PerPackageSoftCapBytes),
		SkillBodyWarnTokens:    e.positiveInt("PODIUM_LINT_SKILL_BODY_WARN_TOKENS", d.SkillBodyWarnTokens),
		SkillBodyWarnLines:     e.positiveInt("PODIUM_LINT_SKILL_BODY_WARN_LINES", d.SkillBodyWarnLines),
		ManifestErrTokens:      e.positiveInt("PODIUM_LINT_MANIFEST_ERROR_TOKENS", d.ManifestErrTokens),
		DomainBodyWarnTokens:   e.positiveInt("PODIUM_LINT_DOMAIN_BODY_WARN_TOKENS", d.DomainBodyWarnTokens),
		MinDescriptionChars:    e.positiveInt("PODIUM_LINT_MIN_DESCRIPTION_CHARS", d.MinDescriptionChars),
		MinDescriptionWords:    e.positiveInt("PODIUM_LINT_MIN_DESCRIPTION_WORDS", d.MinDescriptionWords),
		DescriptionSimilarity:  e.similarity("PODIUM_LINT_DESCRIPTION_SIMILARITY", d.DescriptionSimilarity),
		ContentSimilarity:      e.similarity("PODIUM_LINT_CONTENT_SIMILARITY", d.ContentSimilarity),
	}
}

// thresholdEnv reads one threshold variable at a time and logs only the
// variable it rejects, so no other variable's value reaches the log.
type thresholdEnv struct {
	getenv func(string) string
	logf   func(format string, args ...any)
}

// lookup returns the trimmed value of name; an empty result means unset.
func (e thresholdEnv) lookup(name string) string {
	return strings.TrimSpace(e.getenv(name))
}

func (e thresholdEnv) invalid(name, value, want string, def any) {
	e.logf("lint: %s=%q is invalid (%s); using the default %v", name, value, want, def)
}

// positive parses name as a positive base-10 integer that fits in bits.
func (e thresholdEnv) positive(name string, bits int, def int64) int64 {
	v := e.lookup(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, bits)
	if err != nil || n <= 0 {
		e.invalid(name, v, "want a positive integer", def)
		return def
	}
	return n
}

func (e thresholdEnv) positiveInt(name string, def int) int {
	return int(e.positive(name, strconv.IntSize, int64(def)))
}

// similarity parses name as a number in [0.5, 1.0].
func (e thresholdEnv) similarity(name string, def float64) float64 {
	v := e.lookup(name)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || !validSimilarity(f) {
		e.invalid(name, v, "want a number from 0.5 to 1.0", def)
		return def
	}
	return f
}
