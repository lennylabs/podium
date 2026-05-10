package lint_test

import (
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
	diags := (&lint.Linter{}).Lint(reg, records)
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
	diags := (&lint.Linter{}).Lint(reg, records)
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
	diags := (&lint.Linter{}).Lint(reg, records)
	for _, d := range diags {
		if d.Code == "lint.domain_import_unresolved" || d.Code == "lint.domain_import_cycle" {
			t.Errorf("false positive: %s — %s", d.Code, d.Message)
		}
	}
}
