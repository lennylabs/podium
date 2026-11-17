package lint

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SemanticOverlapCode is the §7.3.1 code of the semantic overlap check.
const SemanticOverlapCode = "lint.semantic_overlap"

// Defaults and bounds of the §7.3.1 semantic overlap check (§13.12).
const (
	// DefaultSemanticSimilarity is the threshold applied to stored overlap
	// records when they are read, for an embedding model that
	// modelSemanticSimilarity names no default for. Similarity values depend
	// on the embedding model, so a deployment tunes it against its own
	// catalog.
	DefaultSemanticSimilarity = 0.85
	// DefaultSemanticNeighbors is the number of nearest peer artifacts one
	// computation finds and stores records for.
	DefaultSemanticNeighbors = 10
	// DefaultSemanticRefreshInterval is the interval of the sweep that
	// computes artifacts with missing or stale overlap records.
	DefaultSemanticRefreshInterval = 24 * time.Hour
	// maxSemanticNeighbors bounds PODIUM_LINT_SEMANTIC_NEIGHBORS, because a
	// computation's index query grows with it, up to 50 times one more than
	// the count when rows of earlier versions crowd out the peers.
	maxSemanticNeighbors = 100
	// minSemanticRefreshInterval is the shortest sweep interval; a shorter
	// one would recompute a large catalog faster than it can finish.
	minSemanticRefreshInterval = time.Minute
)

// modelSemanticSimilarity holds the §13.12 default read threshold of each
// embedding model the spec names, keyed by the normalized model name
// (normalizeModelName).
var modelSemanticSimilarity = map[string]float64{
	"text-embedding-3-small": 0.80,
	"nomic-embed-text":       0.86,
	"multilingual-e5-large":  0.90,
}

// DefaultSemanticSimilarityFor returns the default read threshold for the
// configured embedding model identifier: 0.80 for text-embedding-3-small,
// 0.86 for nomic-embed-text, 0.90 for multilingual-e5-large, and
// DefaultSemanticSimilarity for any other model, an empty identifier
// included. The identifier is matched after normalizeModelName, so a
// provider prefix or an Ollama tag selects the same default as the bare
// name.
//
// Spec: §13.12
func DefaultSemanticSimilarityFor(model string) float64 {
	if v, ok := modelSemanticSimilarity[normalizeModelName(model)]; ok {
		return v
	}
	return DefaultSemanticSimilarity
}

// normalizeModelName reduces a configured model identifier to the bare model
// name the defaults are keyed by. It trims surrounding space and lowercases
// the identifier, drops everything up to the last '/', which removes a
// provider or organization prefix such as "intfloat/multilingual-e5-large"
// or "openai/text-embedding-3-small", and drops everything from the first ':'
// after that, which removes an Ollama tag such as "nomic-embed-text:latest"
// or "nomic-embed-text:v1.5". What remains must equal a known name exactly,
// so a different model that shares a prefix, such as "nomic-embed-text-v2",
// takes the general default.
func normalizeModelName(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	if i := strings.Index(m, ":"); i >= 0 {
		m = m[:i]
	}
	return m
}

// SemanticConfig configures the §7.3.1 semantic overlap check. The registry
// reads it; `podium lint` reads no search index and never runs the check.
//
// Spec: §7.3.1, §13.12
type SemanticConfig struct {
	// Enabled is PODIUM_LINT_SEMANTIC_OVERLAP=on. The check is off by
	// default.
	Enabled bool
	// Similarity is the configured read threshold, from 0.5 to 1.0, or zero
	// when PODIUM_LINT_SEMANTIC_SIMILARITY is unset and the threshold is the
	// configured model's default (Threshold).
	Similarity float64
	// Neighbors is the number of nearest peer artifacts one computation
	// finds and stores records for, from 1 to 100. The computation widens
	// its index query past rows of earlier versions, the artifact's own
	// included, to find them.
	Neighbors int
	// RefreshInterval is the sweep interval: at least one minute, or zero
	// for no sweep.
	RefreshInterval time.Duration
}

// DefaultSemanticConfig returns the spec defaults: off, the configured
// model's threshold (a zero Similarity), 10, and 24h.
//
// Spec: §13.12
func DefaultSemanticConfig() SemanticConfig {
	return SemanticConfig{
		Neighbors:       DefaultSemanticNeighbors,
		RefreshInterval: DefaultSemanticRefreshInterval,
	}
}

// Threshold returns the read threshold the registry applies when a request
// names none: the configured Similarity when it is set, and otherwise the
// default of model, the configured embedding model identifier
// (DefaultSemanticSimilarityFor).
//
// Spec: §7.3.1, §13.12
func (c SemanticConfig) Threshold(model string) float64 {
	if validSimilarity(c.Similarity) {
		return c.Similarity
	}
	return DefaultSemanticSimilarityFor(model)
}

// Resolved replaces each out-of-range numeric field with its default, so a
// zero or partly filled value behaves as the defaults for the fields left
// unset. An out-of-range Similarity becomes zero, which selects the model's
// default. A zero RefreshInterval stays zero, because zero turns the sweep
// off.
func (c SemanticConfig) Resolved() SemanticConfig {
	d := DefaultSemanticConfig()
	if !validSimilarity(c.Similarity) {
		c.Similarity = d.Similarity
	}
	if c.Neighbors < 1 || c.Neighbors > maxSemanticNeighbors {
		c.Neighbors = d.Neighbors
	}
	if c.RefreshInterval != 0 && c.RefreshInterval < minSemanticRefreshInterval {
		c.RefreshInterval = d.RefreshInterval
	}
	return c
}

// SemanticConfigFromEnv reads the PODIUM_LINT_SEMANTIC_* variables through
// getenv, with the invalid-value behavior of ThresholdsFromEnv: an unset or
// empty variable keeps the default silently, and a value that does not parse
// or falls outside its range keeps the default and logs one line naming the
// variable.
//
// Spec: §13.12
func SemanticConfigFromEnv(getenv func(string) string, logf func(format string, args ...any)) SemanticConfig {
	e := thresholdEnv{getenv: getenv, logf: logf}
	d := DefaultSemanticConfig()
	return SemanticConfig{
		Enabled:         e.onOff("PODIUM_LINT_SEMANTIC_OVERLAP", d.Enabled),
		Similarity:      e.semanticSimilarity("PODIUM_LINT_SEMANTIC_SIMILARITY"),
		Neighbors:       e.boundedInt("PODIUM_LINT_SEMANTIC_NEIGHBORS", 1, maxSemanticNeighbors, d.Neighbors),
		RefreshInterval: e.refreshInterval("PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL", d.RefreshInterval),
	}
}

// ParseSimilarity parses a threshold given on a request or a command line. It
// reports false for a value that does not parse or falls outside 0.5 to 1.0,
// the range of every §7.3.1 threshold.
//
// Spec: §7.3.1
func ParseSimilarity(v string) (float64, bool) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || !validSimilarity(f) {
		return 0, false
	}
	return f, true
}

// ValidSimilarity reports whether f lies from 0.5 to 1.0, the range of every
// §7.3.1 threshold.
func ValidSimilarity(f float64) bool { return validSimilarity(f) }

// semanticSimilarity parses name as a number in [0.5, 1.0]. An unset, empty,
// or invalid value returns zero, which selects the configured model's
// default, and an invalid one logs a line naming the variable.
func (e thresholdEnv) semanticSimilarity(name string) float64 {
	v := e.lookup(name)
	if v == "" {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || !validSimilarity(f) {
		e.invalid(name, v, "want a number from 0.5 to 1.0", "for the configured embedding model")
		return 0
	}
	return f
}

// onOff parses name as on or off.
func (e thresholdEnv) onOff(name string, def bool) bool {
	switch v := e.lookup(name); v {
	case "":
		return def
	case "on":
		return true
	case "off":
		return false
	default:
		e.invalid(name, v, "want on or off", onOffString(def))
		return def
	}
}

func onOffString(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// boundedInt parses name as a base-10 integer in [lo, hi].
func (e thresholdEnv) boundedInt(name string, lo, hi, def int) int {
	v := e.lookup(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		e.invalid(name, v, fmt.Sprintf("want an integer from %d to %d", lo, hi), def)
		return def
	}
	return n
}

// refreshInterval parses name as a Go duration of at least one minute, or as
// a zero duration, which turns the sweep off.
func (e thresholdEnv) refreshInterval(name string, def time.Duration) time.Duration {
	v := e.lookup(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || (d != 0 && d < minSemanticRefreshInterval) {
		e.invalid(name, v, "want a duration of at least 1m, or 0 to turn the sweep off", def)
		return def
	}
	return d
}

// Peer is one artifact a pairwise description diagnostic names, with the
// similarity of the pair from 0 to 1. ComputedAt is the time the overlap
// record was computed, set on a lint.semantic_overlap peer and zero on a
// lint.colliding_descriptions peer, which is computed on every run.
//
// Spec: §7.3.1
type Peer struct {
	ArtifactID string
	Similarity float64
	ComputedAt time.Time
}

// SemanticSubject is one artifact the semantic diagnostics are built for.
// Suppressed reports that its lint_suppress lists lint.semantic_overlap, so it
// raises no diagnostic; its peers still name it.
type SemanticSubject struct {
	ArtifactID string
	Suppressed bool
}

// SemanticPair is one stored overlap record that passed the read filters the
// registry applies before the threshold: both versions served, the configured
// model, and both artifacts nameable by the read.
type SemanticPair struct {
	A, B       string
	Similarity float64
	ComputedAt time.Time
}

// SemanticDiagnostics builds lint.semantic_overlap for each subject from the
// pairs at or above threshold. The two artifacts of such a pair are peers. A
// diagnostic names the subject's peers by descending similarity and then by
// identifier, each with its similarity as a percentage, abbreviates a list of
// more than 10 peers, and carries the full peer list in Peers. A pair is
// assumed to name two distinct artifacts. The diagnostics are ordered by
// artifact identifier.
//
// Spec: §7.3.1
func SemanticDiagnostics(subjects []SemanticSubject, pairs []SemanticPair, threshold float64) []Diagnostic {
	peersOf := map[string][]Peer{}
	for _, p := range pairs {
		if !(p.Similarity >= threshold) {
			continue
		}
		peersOf[p.A] = append(peersOf[p.A], Peer{ArtifactID: p.B, Similarity: p.Similarity, ComputedAt: p.ComputedAt})
		peersOf[p.B] = append(peersOf[p.B], Peer{ArtifactID: p.A, Similarity: p.Similarity, ComputedAt: p.ComputedAt})
	}
	var out []Diagnostic
	for _, s := range subjects {
		found := peersOf[s.ArtifactID]
		if s.Suppressed || len(found) == 0 {
			continue
		}
		sortPeers(found)
		d := warn(s.ArtifactID, SemanticOverlapCode,
			fmt.Sprintf("description is semantically similar to %s; give each artifact a distinct summary so search and load_domain can tell them apart",
				formatSemanticPeers(found)))
		d.Peers = found
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ArtifactID < out[j].ArtifactID })
	return out
}

// sortPeers orders peers by descending similarity and then by identifier.
func sortPeers(p []Peer) {
	sort.Slice(p, func(i, j int) bool {
		if p[i].Similarity != p[j].Similarity {
			return p[i].Similarity > p[j].Similarity
		}
		return p[i].ArtifactID < p[j].ArtifactID
	})
}

// formatSemanticPeers renders the peers through formatPeers, so the list and
// its abbreviation past 10 peers read as the catalog advisories do.
func formatSemanticPeers(found []Peer) string {
	first := make([]peer, 0, min(len(found), maxListedPeers))
	for i := 0; i < len(found) && i < maxListedPeers; i++ {
		first = append(first, peer{set: i, pct: similarityPercent(found[i].Similarity)})
	}
	return formatPeers(first, len(found), func(i int) string { return found[i].ArtifactID })
}

// similarityPercent rounds a similarity down to a whole percentage, clamped to
// [0, 100]. The small slack keeps a similarity computed as 0.8499999 from a
// float32 distance from printing one point below its threshold.
func similarityPercent(s float64) int {
	pct := int(math.Floor(s*100 + 1e-6))
	return max(0, min(pct, 100))
}

// DescriptionRules returns the §7.3.1 description checks,
// lint.thin_description and lint.colliding_descriptions, built from
// l.Thresholds with every unset field at its default. The catalog overlap
// report runs them over stored records, which carry descriptions and no
// content units, so lint.near_duplicate_content is left out. A nil l uses the
// defaults.
//
// Spec: §7.3.1
func (l *Linter) DescriptionRules() []Rule {
	th := DefaultThresholds()
	if l != nil {
		th = l.Thresholds.resolved()
	}
	return descriptionRules(th)
}

// SortDiagnostics orders diagnostics as Linter.Lint and `podium lint` do: by
// artifact identifier, then code, then message.
//
// Spec: §7.3.1
func SortDiagnostics(d []Diagnostic) {
	sort.SliceStable(d, func(i, j int) bool {
		if d[i].ArtifactID != d[j].ArtifactID {
			return d[i].ArtifactID < d[j].ArtifactID
		}
		if d[i].Code != d[j].Code {
			return d[i].Code < d[j].Code
		}
		return d[i].Message < d[j].Message
	})
}
