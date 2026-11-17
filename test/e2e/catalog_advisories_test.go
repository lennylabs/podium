package e2e

// Catalog advisories in `podium lint`, end to end.
//
// Spec §7.3.1 defines the catalog advisories (lint.thin_description,
// lint.colliding_descriptions, and lint.near_duplicate_content) and states that
// `podium lint` runs them over every artifact of every layer it walks. It also
// defines the CI contract: `--fail-on`, the `--json` envelope, and the exit
// codes. Spec §13.12 lists the PODIUM_LINT_* variables that set the
// thresholds. These tests drive the real `podium` binary over a multi-layer
// filesystem registry whose advisories span both layers.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
)

// advisoryReconcileDesc and advisoryReconcileCopyDesc differ by one word, so
// their normalized word sets have a similarity of 10/11, above the 0.7 default
// and below 1.0.
const (
	advisoryReconcileDesc     = "Reconcile vendor invoices against purchase orders at month end close."
	advisoryReconcileCopyDesc = "Reconcile the vendor invoices against purchase orders at month end close."
)

// advisorySharedRunbook is the prose two skill bodies share. It holds about
// 180 words, so two bodies that each add one sentence of their own have a
// five-word shingle similarity of 0.86, above the 0.8 default.
const advisorySharedRunbook = "Start by confirming which environment the request targets and record the ticket number in the change log. " +
	"Open the secrets manager console with your own account and never with a shared administrator login. " +
	"Generate the replacement value with the approved generator so the length and the character classes meet policy. " +
	"Store the new value as a pending version and leave the current version active until every consumer is ready. " +
	"Update the deployment manifests that reference the secret and roll them out one service at a time. " +
	"Watch the error dashboards for five minutes after each rollout and stop if the authentication failures climb. " +
	"Promote the pending version to active once every service reports healthy connections with the new value. " +
	"Disable the previous version but keep it recoverable for seven days in case a forgotten consumer appears. " +
	"Write a short summary in the ticket that lists the services touched and the time each one switched over. " +
	"Close the ticket only after the owner of each affected service confirms that their alerts stayed quiet. "

// advisoryRegistry writes a two-layer filesystem registry that draws each
// catalog advisory once per artifact: a thin description in layer-a, a pair
// of overlapping descriptions split across layer-a and layer-b, and two skill
// bodies of at least 50 words in different layers that share all but one
// sentence.
func advisoryRegistry(t *testing.T) string {
	t.Helper()
	return writeRegistry(t, map[string]string{
		".registry-config":                       solofsRegistryConfig("layer-a", "layer-b"),
		"layer-a/finance/vague/ARTIFACT.md":      contextArtifact("demo"),
		"layer-a/finance/reconcile/ARTIFACT.md":  contextArtifact(advisoryReconcileDesc),
		"layer-b/ops/reconcile-copy/ARTIFACT.md": contextArtifact(advisoryReconcileCopyDesc),
		"layer-a/ops/rotate-a/SKILL.md": advisorySkill("rotate-a",
			"Rotate the staging database credentials and confirm each service reconnects.",
			"Page the on-call database engineer when a read replica lags behind the primary."),
		"layer-b/ops/rotate-b/SKILL.md": advisorySkill("rotate-b",
			"Replace production API signing keys after a suspected compromise is reported.",
			"Notify the security channel whenever a signing key is revoked ahead of schedule."),
	})
}

// advisorySkill returns a versioned SKILL.md whose body is the shared runbook
// followed by one sentence of its own.
func advisorySkill(name, description, ownSentence string) string {
	return "---\nname: " + name + "\ndescription: " + description +
		"\nmetadata:\n  version: \"1.0.0\"\n---\n\n" + advisorySharedRunbook + ownSentence + "\n"
}

// lintEnvelope is the §7.3.1 `podium lint --json` object.
type lintEnvelope struct {
	Registry    string `json:"registry"`
	Diagnostics []struct {
		ArtifactID string `json:"artifact_id"`
		Code       string `json:"code"`
		Severity   string `json:"severity"`
		Message    string `json:"message"`
	} `json:"diagnostics"`
	Rejected []struct {
		ArtifactID string         `json:"artifact_id"`
		Code       string         `json:"code"`
		Reason     string         `json:"reason"`
		Details    map[string]any `json:"details"`
	} `json:"rejected"`
	Counts map[string]int `json:"counts"`
}

// decodeLintEnvelope decodes stdout as exactly one JSON object and fails when
// anything other than white space follows it.
func decodeLintEnvelope(t *testing.T, stdout string) lintEnvelope {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	var env lintEnvelope
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("decode lint envelope: %v\nstdout:\n%s", err, stdout)
	}
	if rest := strings.TrimSpace(stdout[dec.InputOffset():]); rest != "" {
		t.Fatalf("stdout carries more than the envelope; trailing %q", rest)
	}
	return env
}

// Spec: §7.3.1, §13.12 — `podium lint` reports the three catalog advisories
// across layers and exits 0, because each is a warning and --fail-on defaults
// to error.
func TestCatalogAdvisories_LintReportsEachCode(t *testing.T) {
	t.Parallel()
	reg := advisoryRegistry(t)
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	cliWantExit(t, res, 0, "lint over the advisory registry")
	for _, want := range []string{
		"[warning] finance/vague: description is thin",
		"(lint.thin_description)",
		"[warning] finance/reconcile: description overlaps ops/reconcile-copy",
		"[warning] ops/reconcile-copy: description overlaps finance/reconcile",
		"(lint.colliding_descriptions)",
		"[warning] ops/rotate-a: SKILL.md body nearly duplicates ops/rotate-b SKILL.md body",
		"[warning] ops/rotate-b: SKILL.md body nearly duplicates ops/rotate-a SKILL.md body",
		"(lint.near_duplicate_content)",
	} {
		cliContains(t, res.Stdout, want, "lint stdout")
	}
}

// Spec: §7.3.1 — --fail-on warning fails the run on a warning, and a
// --fail-on value other than error or warning is a usage error.
func TestCatalogAdvisories_FailOn(t *testing.T) {
	t.Parallel()
	reg := advisoryRegistry(t)
	res := runPodium(t, "", nil, "lint", "--registry", reg, "--fail-on", "warning")
	cliWantExit(t, res, 1, "lint --fail-on warning")
	cliContains(t, res.Stdout, "lint.near_duplicate_content", "lint --fail-on warning still prints the diagnostics")

	bogus := runPodium(t, "", nil, "lint", "--registry", reg, "--fail-on", "bogus")
	cliWantExit(t, bogus, 2, "lint --fail-on bogus")
	cliContains(t, bogus.Stderr, "--fail-on must be error or warning", "usage error")
}

// Spec: §7.3.1 — under --json standard output carries the envelope alone,
// diagnostics are ordered by artifact identifier, then code, then message,
// and counts tally them by severity.
func TestCatalogAdvisories_JSONEnvelope(t *testing.T) {
	t.Parallel()
	reg := advisoryRegistry(t)
	res := runPodium(t, "", nil, "lint", "--registry", reg, "--json")
	cliWantExit(t, res, 0, "lint --json")
	env := decodeLintEnvelope(t, res.Stdout)
	if env.Registry != reg {
		t.Errorf("registry = %q, want %q", env.Registry, reg)
	}
	want := [][2]string{
		{"finance/reconcile", "lint.colliding_descriptions"},
		{"finance/vague", "lint.thin_description"},
		{"ops/reconcile-copy", "lint.colliding_descriptions"},
		{"ops/rotate-a", "lint.near_duplicate_content"},
		{"ops/rotate-b", "lint.near_duplicate_content"},
	}
	if len(env.Diagnostics) != len(want) {
		t.Fatalf("diagnostics = %+v, want %d entries", env.Diagnostics, len(want))
	}
	for i, d := range env.Diagnostics {
		if d.ArtifactID != want[i][0] || d.Code != want[i][1] || d.Severity != "warning" {
			t.Errorf("diagnostics[%d] = %s %s %s, want %s %s warning", i, d.ArtifactID, d.Code, d.Severity, want[i][0], want[i][1])
		}
	}
	if len(env.Rejected) != 0 {
		t.Errorf("rejected = %+v, want []", env.Rejected)
	}
	wantCounts := map[string]int{"error": 0, "warning": 5, "info": 0}
	for k, v := range wantCounts {
		if got, ok := env.Counts[k]; !ok || got != v {
			t.Errorf("counts[%s] = %d (present %v), want %d", k, got, ok, v)
		}
	}
	if !strings.Contains(res.Stdout, `"rejected": []`) {
		t.Errorf("rejected must render as [] when empty:\n%s", res.Stdout)
	}
}

// Spec: §7.3.1, §13.12 — PODIUM_LINT_DESCRIPTION_SIMILARITY=1.0 raises the
// colliding threshold above the paraphrase's 10/11, so it no longer fires,
// and an unparsable PODIUM_LINT_CONTENT_SIMILARITY keeps the 0.8 default,
// so the near-duplicate still fires and standard error names the variable.
func TestCatalogAdvisories_ThresholdVariables(t *testing.T) {
	t.Parallel()
	reg := advisoryRegistry(t)

	strict := runPodium(t, "", []string{"PODIUM_LINT_DESCRIPTION_SIMILARITY=1.0"}, "lint", "--registry", reg)
	cliWantExit(t, strict, 0, "lint with description similarity 1.0")
	cliNotContains(t, strict.Stdout, "lint.colliding_descriptions", "paraphrase silenced at 1.0")
	cliContains(t, strict.Stdout, "lint.near_duplicate_content", "near-duplicate unaffected")

	bad := runPodium(t, "", []string{"PODIUM_LINT_CONTENT_SIMILARITY=abc"}, "lint", "--registry", reg)
	cliWantExit(t, bad, 0, "lint with an unparsable content similarity")
	cliContains(t, bad.Stdout, "lint.near_duplicate_content", "near-duplicate fires at the default")
	cliContains(t, bad.Stderr, "PODIUM_LINT_CONTENT_SIMILARITY", "stderr names the rejected variable")
}

// Spec: §7.3.1, §4.6 — under --json an artifact that declares extends: is
// listed in rejected with details.constraint extends_removed, standard error
// does not name it, standard output holds the envelope alone, and the run
// exits 1.
func TestCatalogAdvisories_JSONRejectsExtends(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":                 solofsRegistryConfig("base-layer", "team-layer"),
		"base-layer/core/note/ARTIFACT.md": contextArtifact("Core note describing the shared finance glossary terms."),
		"team-layer/team/note/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Team note that extends the shared finance glossary.\nextends: core/note\n---\n\nTeam note body.\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg, "--json")
	cliWantExit(t, res, 1, "lint --json with an extends: artifact")
	env := decodeLintEnvelope(t, res.Stdout)
	if len(env.Rejected) != 1 {
		t.Fatalf("rejected = %+v, want one entry", env.Rejected)
	}
	r := env.Rejected[0]
	if r.ArtifactID != "team/note" || r.Code != layer.InvalidArtifactCode {
		t.Errorf("rejected[0] = %s %s, want team/note %s", r.ArtifactID, r.Code, layer.InvalidArtifactCode)
	}
	if r.Details["constraint"] != layer.ExtendsRemovedConstraint {
		t.Errorf("rejected[0].details = %v, want constraint %s", r.Details, layer.ExtendsRemovedConstraint)
	}
	cliNotContains(t, res.Stderr, "rejected:", "--json keeps refused artifacts off stderr")
}

// Spec: §7.3.1 — a registry path that cannot be opened writes the error on
// standard error, no envelope, and exits 1.
func TestCatalogAdvisories_JSONMissingRegistry(t *testing.T) {
	t.Parallel()
	missing := t.TempDir() + "/absent"
	res := runPodium(t, "", nil, "lint", "--registry", missing, "--json")
	cliWantExit(t, res, 1, "lint --json over a missing registry")
	if strings.TrimSpace(res.Stdout) != "" {
		t.Errorf("stdout carries output on an open failure:\n%s", res.Stdout)
	}
	cliContains(t, res.Stderr, "error:", "open failure on stderr")
}

// Spec: §7.3.1, §4.6 — a same-ID collision between two layers fails the
// walk: standard output stays empty, standard error carries ingest.collision,
// and the run exits 1.
func TestCatalogAdvisories_JSONCollisionWritesNoEnvelope(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":                solofsMultiLayerConfig,
		"layer-a/shared/note/ARTIFACT.md": contextArtifact("Shared note for the finance close checklist owners."),
		"layer-b/shared/note/ARTIFACT.md": contextArtifact("Shared note for the treasury payment approval owners."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg, "--json")
	cliWantExit(t, res, 1, "lint --json over a collision")
	if res.Stdout != "" {
		t.Errorf("stdout = %q, want empty on a walk failure", res.Stdout)
	}
	cliContains(t, res.Stderr, "ingest.collision", "collision on stderr")
}
