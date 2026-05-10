package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// importCmd converts a directory tree of standalone skill files
// (typically `~/.claude/skills/` shape: per-skill directory with a
// SKILL.md inside) into a Podium-shaped filesystem layer (per §3:
// each artifact lives in its own directory with ARTIFACT.md +
// SKILL.md and any bundled resources).
//
// Usage:
//
//	podium import --source <dir> --target <dir> [--type skill] [--version 1.0.0]
//
// `--source` walks every immediate subdirectory whose name doubles
// as the artifact id; `--target` receives the rewritten layer
// rooted at <target>/. The tool is intentionally conservative: it
// never modifies the source.
func importCmd(args []string) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	setUsage(fs, "Convert a skills/* tree into a Podium-shaped layer.")
	source := fs.String("source", "", "directory of skill subdirectories (required)")
	target := fs.String("target", "", "destination layer directory (required)")
	typ := fs.String("type", "skill", "artifact type to use in ARTIFACT.md")
	version := fs.String("version", "1.0.0", "artifact version to use in ARTIFACT.md")
	dryRun := fs.Bool("dry-run", false, "report the plan; write nothing")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *source == "" || *target == "" {
		fmt.Fprintln(os.Stderr, "error: --source and --target are required")
		return 2
	}
	plan, err := buildImportPlan(*source, *typ, *version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(plan) == 0 {
		fmt.Fprintln(os.Stderr, "error: source directory contained no skills")
		return 1
	}
	if *dryRun {
		fmt.Printf("would write %d artifact(s) to %s:\n", len(plan), *target)
		for _, p := range plan {
			fmt.Printf("  %s/\n", p.id)
		}
		return 0
	}
	if err := os.MkdirAll(*target, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	for _, p := range plan {
		if err := writeImportedArtifact(*target, p); err != nil {
			fmt.Fprintf(os.Stderr, "error: write %s: %v\n", p.id, err)
			return 1
		}
		fmt.Printf("wrote %s/\n", filepath.Join(*target, p.id))
	}
	return 0
}

type importedArtifact struct {
	id          string
	skillBody   []byte
	resources   map[string][]byte
	manifestTyp string
	version     string
}

// buildImportPlan walks source for immediate subdirectories. For
// each, the SKILL.md (or single .md file) becomes the artifact's
// SKILL.md; any other regular files attach as bundled resources.
func buildImportPlan(source, typ, version string) ([]importedArtifact, error) {
	entries, err := os.ReadDir(source)
	if err != nil {
		return nil, err
	}
	out := []importedArtifact{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(source, e.Name())
		body, resources, err := collectSkillFiles(dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if body == nil {
			continue
		}
		out = append(out, importedArtifact{
			id:          e.Name(),
			skillBody:   body,
			resources:   resources,
			manifestTyp: typ,
			version:     version,
		})
	}
	return out, nil
}

func collectSkillFiles(dir string) ([]byte, map[string][]byte, error) {
	resources := map[string][]byte{}
	var body []byte
	walk := filepath.Walk
	err := walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		base := filepath.Base(path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		switch {
		case base == "SKILL.md" || (body == nil && strings.HasSuffix(base, ".md")):
			body = data
		default:
			resources[filepath.ToSlash(rel)] = data
		}
		return nil
	})
	return body, resources, err
}

func writeImportedArtifact(target string, p importedArtifact) error {
	dest := filepath.Join(target, p.id)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	manifest := fmt.Sprintf("---\ntype: %s\nversion: %s\n---\n\n", p.manifestTyp, p.version)
	if err := os.WriteFile(filepath.Join(dest, "ARTIFACT.md"), []byte(manifest), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), p.skillBody, 0o644); err != nil {
		return err
	}
	for rel, body := range p.resources {
		if rel == "SKILL.md" || rel == "ARTIFACT.md" {
			continue
		}
		full := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}
