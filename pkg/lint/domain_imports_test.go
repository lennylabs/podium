package lint_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
}

const liveArtifact = `---
type: context
version: 1.0.0
description: x
sensitivity: low
---

`

// Spec: §4.5.2 — DOMAIN.md `include:` patterns must match at
// least one known artifact; an unresolved pattern surfaces as an
// ingest-time warning (not error) so authors can stage cross-
// layer setups.
func TestRuleDomainImportsResolve_UnresolvedWarns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"finance/ap/payments/run/ARTIFACT.md": liveArtifact,
		"finance/DOMAIN.md": `---
name: finance
include:
  - finance/ap/payments/*
  - finance/never-defined/*
---
`,
	})
	reg, err := filesystem.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	records, err := reg.Walk(filesystem.WalkOptions{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	diags := (&lint.Linter{}).Lint(context.Background(), reg, records)
	gotUnresolved := false
	gotResolved := false
	for _, d := range diags {
		if d.Code != "lint.domain_import_unresolved" {
			continue
		}
		if strings.Contains(d.Message, "finance/never-defined/*") {
			gotUnresolved = true
		}
		if strings.Contains(d.Message, "finance/ap/payments/*") {
			gotResolved = true
		}
	}
	if !gotUnresolved {
		t.Errorf("missing warning for finance/never-defined/*: %+v", diags)
	}
	if gotResolved {
		t.Errorf("false-positive warning for finance/ap/payments/*: %+v", diags)
	}
}

// Spec: §4.5.2 — two domains importing each other is allowed
// but lint-warned.
func TestRuleDomainImportCycle_DetectsMutualImport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"alpha/main/ARTIFACT.md": liveArtifact,
		"beta/main/ARTIFACT.md":  liveArtifact,
		"alpha/DOMAIN.md": `---
name: alpha
include:
  - beta/*
---
`,
		"beta/DOMAIN.md": `---
name: beta
include:
  - alpha/*
---
`,
	})
	reg, _ := filesystem.Open(dir)
	records, _ := reg.Walk(filesystem.WalkOptions{})
	diags := (&lint.Linter{}).Lint(context.Background(), reg, records)
	got := false
	for _, d := range diags {
		if d.Code == "lint.domain_import_cycle" {
			got = true
		}
	}
	if !got {
		t.Errorf("missing cycle diagnostic: %+v", diags)
	}
}

// Spec: §4.5.5 (F-4.5.11) — when the tenant disables per-domain
// discovery overrides, a DOMAIN.md carrying a discovery: block is
// warned (ingest still succeeds); the default rule set (overrides
// allowed) draws no such diagnostic.
func TestRuleDomainDiscoveryOverrideDisallowed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"finance/ap/run/ARTIFACT.md": liveArtifact,
		"finance/ap/DOMAIN.md": `---
description: AP
discovery:
  notable_count: 3
---
`,
		// A DOMAIN.md without a discovery: block must not be flagged.
		"finance/DOMAIN.md": `---
description: Finance
---
`,
	})
	reg, err := filesystem.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	records, err := reg.Walk(filesystem.WalkOptions{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	const code = "lint.domain_discovery_override_disabled"

	// Default linter (overrides allowed): no discovery-override warning.
	for _, d := range (&lint.Linter{}).Lint(context.Background(), reg, records) {
		if d.Code == code {
			t.Errorf("unexpected %s when overrides are allowed: %+v", code, d)
		}
	}

	// Overrides disabled: exactly the finance/ap DOMAIN.md (which has a
	// discovery: block) is warned.
	disabled := false
	diags := (&lint.Linter{AllowPerDomainOverrides: &disabled}).Lint(context.Background(), reg, records)
	var flagged []string
	for _, d := range diags {
		if d.Code == code {
			flagged = append(flagged, d.ArtifactID)
		}
	}
	if len(flagged) != 1 || flagged[0] != "finance/ap" {
		t.Errorf("discovery-override warnings = %v, want exactly [finance/ap]", flagged)
	}
}

// Spec: §9.3 "Cancellable" — the cross-artifact domain rules honor the
// caller's context: an already-cancelled context short-circuits the per-layer
// walk, so the unresolved-import warning that the same input produces under a
// live context is suppressed. This is the behavioral counterpart to the static
// signature guard in test/conformance.
func TestRuleDomainImports_HonorsContextCancellation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"finance/ap/payments/run/ARTIFACT.md": liveArtifact,
		"finance/DOMAIN.md": `---
name: finance
include:
  - finance/never-defined/*
---
`,
	})
	reg, err := filesystem.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	records, err := reg.Walk(filesystem.WalkOptions{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	// Sanity: a live context produces the unresolved-import warning.
	live := (&lint.Linter{}).Lint(context.Background(), reg, records)
	if !hasCode(live, "lint.domain_import_unresolved") {
		t.Fatalf("expected unresolved warning under a live context: %+v", live)
	}

	// A cancelled context short-circuits the walk before it inspects the layer.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := (&lint.Linter{}).Lint(ctx, reg, records)
	if hasCode(cancelled, "lint.domain_import_unresolved") {
		t.Errorf("cancelled context still produced domain-import diagnostics: %+v", cancelled)
	}
}

func hasCode(diags []lint.Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

// Spec: §4.5.2 — domain with no imports lints clean (no false
// positives).
func TestRuleDomainImports_NoImportsClean(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"team/x/ARTIFACT.md": liveArtifact,
	})
	reg, _ := filesystem.Open(dir)
	records, _ := reg.Walk(filesystem.WalkOptions{})
	diags := (&lint.Linter{}).Lint(context.Background(), reg, records)
	for _, d := range diags {
		if d.Code == "lint.domain_import_unresolved" || d.Code == "lint.domain_import_cycle" {
			t.Errorf("false positive: %s — %s", d.Code, d.Message)
		}
	}
}
