package lint

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Spec: §13.12 — the semantic overlap check is off by default, with the
// configured model's threshold (a zero Similarity), 10 neighbours, and a 24h
// sweep.
func TestSemanticConfig_DefaultsWhenUnset(t *testing.T) {
	t.Parallel()
	var logged []string
	got := SemanticConfigFromEnv(func(string) string { return "" }, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	want := SemanticConfig{Neighbors: 10, RefreshInterval: 24 * time.Hour}
	if got != want || len(logged) != 0 {
		t.Fatalf("defaults = %+v (logged %v), want %+v and no log", got, logged, want)
	}
}

// Spec: §13.12 — the default threshold depends on the configured embedding
// model: 0.80 for text-embedding-3-small, 0.86 for nomic-embed-text, 0.90 for
// multilingual-e5-large, and 0.85 for any other model. A provider prefix, an
// Ollama tag, case, and surrounding space select the bare name's default, a
// different model that shares a prefix does not, and a configured
// PODIUM_LINT_SEMANTIC_SIMILARITY wins over every model default.
func TestSemanticConfig_ModelAwareThreshold(t *testing.T) {
	t.Parallel()
	unset := DefaultSemanticConfig()
	for model, want := range map[string]float64{
		"text-embedding-3-small":         0.80,
		"openai/text-embedding-3-small":  0.80,
		"nomic-embed-text":               0.86,
		"nomic-embed-text:latest":        0.86,
		"nomic-embed-text:v1.5":          0.86,
		" Nomic-Embed-Text ":             0.86,
		"multilingual-e5-large":          0.90,
		"intfloat/multilingual-e5-large": 0.90,
		"text-embedding-3-large":         0.85,
		"nomic-embed-text-v2":            0.85,
		"multilingual-e5-large-instruct": 0.85,
		"voyage-3":                       0.85,
		"":                               0.85,
	} {
		if got := unset.Threshold(model); got != want {
			t.Errorf("Threshold(%q) = %v, want %v", model, got, want)
		}
		if got := (SemanticConfig{Similarity: 0.93}).Threshold(model); got != 0.93 {
			t.Errorf("Threshold(%q) with 0.93 configured = %v, want the configured value", model, got)
		}
	}
	env := func(k string) string { return map[string]string{"PODIUM_LINT_SEMANTIC_SIMILARITY": "0.7"}[k] }
	if got := SemanticConfigFromEnv(env, func(string, ...any) {}).Threshold("multilingual-e5-large"); got != 0.7 {
		t.Errorf("an explicit PODIUM_LINT_SEMANTIC_SIMILARITY=0.7 gave %v on multilingual-e5-large, want 0.7", got)
	}
}

// Spec: §13.12 — each PODIUM_LINT_SEMANTIC_* variable is read, and a value
// that does not parse or falls outside its range keeps the default and logs
// a warning that names the variable.
func TestSemanticConfig_FromEnv(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		env  map[string]string
		want SemanticConfig
		logs []string
	}{
		{
			name: "valid values",
			env: map[string]string{
				"PODIUM_LINT_SEMANTIC_OVERLAP":          "on",
				"PODIUM_LINT_SEMANTIC_SIMILARITY":       "0.9",
				"PODIUM_LINT_SEMANTIC_NEIGHBORS":        "25",
				"PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL": "1m",
			},
			want: SemanticConfig{Enabled: true, Similarity: 0.9, Neighbors: 25, RefreshInterval: time.Minute},
		},
		{
			name: "off and a zero sweep",
			env:  map[string]string{"PODIUM_LINT_SEMANTIC_OVERLAP": "off", "PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL": "0"},
			want: SemanticConfig{Neighbors: 10},
		},
		{
			name: "invalid values keep their defaults",
			env: map[string]string{
				"PODIUM_LINT_SEMANTIC_OVERLAP":          "yes",
				"PODIUM_LINT_SEMANTIC_SIMILARITY":       "0.4",
				"PODIUM_LINT_SEMANTIC_NEIGHBORS":        "101",
				"PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL": "30s",
			},
			want: SemanticConfig{Neighbors: 10, RefreshInterval: 24 * time.Hour},
			logs: []string{"PODIUM_LINT_SEMANTIC_OVERLAP", "PODIUM_LINT_SEMANTIC_SIMILARITY", "PODIUM_LINT_SEMANTIC_NEIGHBORS", "PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL"},
		},
		{
			name: "unparseable numbers",
			env:  map[string]string{"PODIUM_LINT_SEMANTIC_NEIGHBORS": "ten", "PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL": "daily"},
			want: SemanticConfig{Neighbors: 10, RefreshInterval: 24 * time.Hour},
			logs: []string{"PODIUM_LINT_SEMANTIC_NEIGHBORS", "PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var logged []string
			got := SemanticConfigFromEnv(func(k string) string { return c.env[k] }, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
			if got != c.want {
				t.Errorf("config = %+v, want %+v", got, c.want)
			}
			if len(logged) != len(c.logs) {
				t.Fatalf("logged %v, want one line per %v", logged, c.logs)
			}
			for i, name := range c.logs {
				if !strings.Contains(logged[i], name) {
					t.Errorf("log %q does not name %s", logged[i], name)
				}
			}
		})
	}
}

// Spec: §13.12 — a partly filled configuration behaves as the defaults for
// the fields left unset, and a zero sweep interval stays off.
func TestSemanticConfig_Resolved(t *testing.T) {
	t.Parallel()
	got := SemanticConfig{Enabled: true, Similarity: 1.5, RefreshInterval: time.Second}.Resolved()
	want := SemanticConfig{Enabled: true, Neighbors: 10, RefreshInterval: 24 * time.Hour}
	if got != want {
		t.Errorf("Resolved = %+v, want %+v", got, want)
	}
	if off := (SemanticConfig{}).Resolved(); off.RefreshInterval != 0 {
		t.Errorf("a zero interval resolved to %s, want 0", off.RefreshInterval)
	}
}

// Spec: §7.3.1 — a request threshold is a number from 0.5 to 1.0.
func TestParseSimilarity(t *testing.T) {
	t.Parallel()
	for v, ok := range map[string]bool{"0.5": true, "1": true, "0.92": true, "0.49": false, "1.01": false, "abc": false, "": false} {
		if _, got := ParseSimilarity(v); got != ok {
			t.Errorf("ParseSimilarity(%q) ok = %v, want %v", v, got, ok)
		}
	}
}

// Spec: §7.3.1 — a pair at or above the threshold makes the two artifacts
// peers, a pair below it does not, the peers are named by descending
// similarity and then by identifier with their percentages, and the
// structured peer list carries each similarity and computed time.
func TestSemanticDiagnostics_ThresholdOrderAndPeers(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	pairs := []SemanticPair{
		{A: "a", B: "c", Similarity: 0.90, ComputedAt: at},
		{A: "a", B: "b", Similarity: 0.90, ComputedAt: at},
		{A: "a", B: "d", Similarity: 0.97, ComputedAt: at},
		{A: "a", B: "e", Similarity: 0.84, ComputedAt: at},
	}
	subjects := []SemanticSubject{{ArtifactID: "e"}, {ArtifactID: "a"}}
	diags := SemanticDiagnostics(subjects, pairs, 0.85)
	if len(diags) != 1 || diags[0].ArtifactID != "a" {
		t.Fatalf("diagnostics = %+v, want one for a (e is below the threshold)", diags)
	}
	d := diags[0]
	if d.Code != SemanticOverlapCode || d.Severity != SeverityWarning {
		t.Errorf("diagnostic = %+v, want a %s warning", d, SemanticOverlapCode)
	}
	// The check compares the §4.7 embedding projection, so the message names
	// the projection fields and does not attribute the similarity to the
	// description alone.
	want := "name, description, when_to_use, and tags are semantically similar to d (97%), b (90%), c (90%); make them distinct"
	if !strings.HasPrefix(d.Message, want) {
		t.Errorf("message = %q, want it to begin with %q", d.Message, want)
	}
	if len(d.Peers) != 3 || d.Peers[0].ArtifactID != "d" || d.Peers[0].Similarity != 0.97 || !d.Peers[2].ComputedAt.Equal(at) {
		t.Errorf("peers = %+v", d.Peers)
	}
	// A lower read threshold reports e too, with no recomputation.
	if diags := SemanticDiagnostics(subjects, pairs, 0.8); len(diags) != 2 {
		t.Errorf("at 0.8: %d diagnostics, want 2", len(diags))
	}
}

// Spec: §7.3.1 — more than 10 peers print the first 10 and a count of the
// rest, the structured list is never abbreviated, and an artifact whose
// lint_suppress lists the code raises no diagnostic while its peers still
// name it.
func TestSemanticDiagnostics_AbbreviationAndSuppression(t *testing.T) {
	t.Parallel()
	var pairs []SemanticPair
	var subjects []SemanticSubject
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("p/%02d", i)
		pairs = append(pairs, SemanticPair{A: "hub", B: id, Similarity: 0.9})
		subjects = append(subjects, SemanticSubject{ArtifactID: id})
	}
	subjects = append(subjects, SemanticSubject{ArtifactID: "hub", Suppressed: true})
	diags := SemanticDiagnostics(subjects, pairs, 0.85)
	if len(diags) != 12 {
		t.Fatalf("%d diagnostics, want 12 (hub suppressed, each peer names hub)", len(diags))
	}
	for _, d := range diags {
		if d.ArtifactID == "hub" || !strings.Contains(d.Message, "hub (90%)") {
			t.Errorf("diagnostic %+v", d)
		}
	}
	all := SemanticDiagnostics([]SemanticSubject{{ArtifactID: "hub"}}, pairs, 0.85)
	if len(all) != 1 || !strings.Contains(all[0].Message, ", and 2 more;") || len(all[0].Peers) != 12 {
		t.Errorf("hub diagnostic = %+v, want 10 named, 2 more, and 12 structured peers", all)
	}
}

// Spec: §7.3.1 — similarities round down to whole percentages, clamped.
func TestSimilarityPercent(t *testing.T) {
	t.Parallel()
	for in, want := range map[float64]int{0.8499999999: 85, 0.851: 85, 1.2: 100, -0.1: 0} {
		if got := similarityPercent(in); got != want {
			t.Errorf("similarityPercent(%v) = %d, want %d", in, got, want)
		}
	}
}

// Spec: §7.3.1 — a lint.colliding_descriptions diagnostic carries every peer
// its message names or abbreviates, in the message's order, with the exact
// similarity, and never abbreviates the list.
func TestCollidingDescriptions_StructuredPeers(t *testing.T) {
	t.Parallel()
	diags := runAdvisory([]filesystem.ArtifactRecord{
		rec("m/base", "alpha bravo charlie delta echo foxtrot golf hotel india juliet"),
		rec("a/low", "alpha bravo charlie delta echo foxtrot golf hotel"),
		rec("z/high", "alpha bravo charlie delta echo foxtrot golf hotel india"),
	})
	var base *Diagnostic
	for i := range diags {
		if diags[i].ArtifactID == "m/base" && diags[i].Code == "lint.colliding_descriptions" {
			base = &diags[i]
		}
	}
	if base == nil || len(base.Peers) != 2 || base.Peers[0].ArtifactID != "z/high" || base.Peers[0].Similarity != 0.9 ||
		base.Peers[1].ArtifactID != "a/low" || base.Peers[1].Similarity != 0.8 || !base.Peers[0].ComputedAt.IsZero() {
		t.Fatalf("m/base peers = %+v", base)
	}

	var records []filesystem.ArtifactRecord
	for i := 12; i >= 0; i-- {
		records = append(records, rec(fmt.Sprintf("dup/%02d", i), "Reconcile vendor invoices at month-end close."))
	}
	for _, d := range runAdvisory(records) {
		if d.ArtifactID != "dup/00" || d.Code != "lint.colliding_descriptions" {
			continue
		}
		if len(d.Peers) != 12 || d.Peers[0].ArtifactID != "dup/01" || d.Peers[11].ArtifactID != "dup/12" || d.Peers[11].Similarity != 1 {
			t.Errorf("dup/00 peers = %+v, want all 12 in identifier order at similarity 1", d.Peers)
		}
	}
}
