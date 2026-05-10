// Command speccov reports spec-citation coverage across Podium's test suite.
//
// Every test function carries a structured comment of the form
//
//	// Spec: §X.Y short title — assertion text.
//	// Phase: N
//
// speccov walks the repository, parses the comments above every Test
// function it finds (Go, Python, TypeScript), and produces:
//
//	speccov report      — table of section -> count of citing tests
//	speccov uncovered   — spec sections with zero citing tests
//	speccov drift       — citations referencing sections that no longer exist
//	speccov tests       — flat list of every parsed test with its citation
//
// The MVP shipped in Stage 1 implements report, uncovered, drift, and tests.
// Sentence-level coverage and matrix audits land in later stages.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/lennylabs/podium/tools/internal/specparser"
)

const usageText = `usage: speccov <command> [flags]

Commands:
  report       Print every spec section with the count of tests citing it.
  uncovered    Print spec sections with zero citing tests.
  drift        Print citations to sections that no longer exist in spec/.
  tests        Print every test with its citation and phase.

Flags:
  -repo <path>    Repository root (default: walk up to the directory containing .phase).
  -spec <path>    Spec directory relative to repo root (default: spec).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	cmd := os.Args[1]

	fs := flag.NewFlagSet("speccov", flag.ExitOnError)
	repo := fs.String("repo", "", "repository root")
	spec := fs.String("spec", "spec", "spec directory")
	if err := fs.Parse(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	root, err := resolveRepoRoot(*repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot resolve repo root: %v\n", err)
		os.Exit(2)
	}

	sections, err := specparser.LoadSpecSections(joinPath(root, *spec))
	if err != nil {
		fmt.Fprintf(os.Stderr, "load spec: %v\n", err)
		os.Exit(1)
	}

	tests, err := specparser.WalkTests(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "walk tests: %v\n", err)
		os.Exit(1)
	}

	switch cmd {
	case "report":
		os.Exit(report(os.Stdout, sections, tests))
	case "uncovered":
		os.Exit(uncovered(os.Stdout, sections, tests))
	case "drift":
		os.Exit(drift(os.Stdout, sections, tests))
	case "tests":
		os.Exit(listTests(os.Stdout, tests))
	case "help", "-h", "--help":
		fmt.Fprint(os.Stdout, usageText)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n%s", cmd, usageText)
		os.Exit(2)
	}
}

// report prints a section -> count table to w. Returns 0.
func report(w io.Writer, sections []specparser.Section, tests []specparser.Test) int {
	bySection := groupBySection(tests)
	fmt.Fprintln(w, "section                  tests   title")
	fmt.Fprintln(w, "-------                  -----   -----")
	for _, s := range sections {
		count := len(bySection[s.ID])
		title := s.Title
		fmt.Fprintf(w, "%-22s   %5d   %s\n", s.ID, count, title)
	}
	covered, total := coverageStats(sections, bySection)
	fmt.Fprintf(w, "\n%d/%d sections have at least one citing test (%d uncited).\n",
		covered, total, total-covered)
	return 0
}

// uncovered prints sections without any citing test. Exit 1 if any uncited.
func uncovered(w io.Writer, sections []specparser.Section, tests []specparser.Test) int {
	bySection := groupBySection(tests)
	missing := []specparser.Section{}
	for _, s := range sections {
		if len(bySection[s.ID]) == 0 {
			missing = append(missing, s)
		}
	}
	if len(missing) == 0 {
		fmt.Fprintln(w, "all spec sections have at least one citing test.")
		return 0
	}
	fmt.Fprintf(w, "%d spec sections have no citing test:\n\n", len(missing))
	for _, s := range missing {
		fmt.Fprintf(w, "  %s   %s\n", s.ID, s.Title)
	}
	return 1
}

// drift prints citations referencing sections that no longer exist.
func drift(w io.Writer, sections []specparser.Section, tests []specparser.Test) int {
	known := map[string]bool{}
	for _, s := range sections {
		known[s.ID] = true
	}
	var rogue []specparser.Test
	for _, t := range tests {
		if t.Citation.SectionID == "n/a" || t.Citation.SectionID == "" {
			continue
		}
		if !known[t.Citation.SectionID] {
			rogue = append(rogue, t)
		}
	}
	if len(rogue) == 0 {
		fmt.Fprintln(w, "no spec citation drift detected.")
		return 0
	}
	fmt.Fprintf(w, "%d test(s) cite spec sections that no longer exist:\n\n", len(rogue))
	for _, t := range rogue {
		fmt.Fprintf(w, "  %s   %s   (%s)\n", t.Citation.SectionID, t.Name, t.File)
	}
	return 1
}

// listTests prints every parsed test.
func listTests(w io.Writer, tests []specparser.Test) int {
	sort.Slice(tests, func(i, j int) bool { return tests[i].Name < tests[j].Name })
	fmt.Fprintln(w, "phase   section          test")
	for _, t := range tests {
		section := t.Citation.SectionID
		if section == "" {
			section = "(none)"
		}
		fmt.Fprintf(w, "%5d   %-15s   %s\n", t.Phase, section, t.Name)
	}
	return 0
}

func groupBySection(tests []specparser.Test) map[string][]specparser.Test {
	m := map[string][]specparser.Test{}
	for _, t := range tests {
		if t.Citation.SectionID == "" || t.Citation.SectionID == "n/a" {
			continue
		}
		m[t.Citation.SectionID] = append(m[t.Citation.SectionID], t)
		// Multi-cite tests count toward each aliased section so
		// "Spec: §8.1 / §4.7.5" covers both.
		for _, alias := range t.Citation.Aliases {
			if alias == "" || alias == "n/a" {
				continue
			}
			m[alias] = append(m[alias], t)
		}
	}
	return m
}

func coverageStats(sections []specparser.Section, bySection map[string][]specparser.Test) (covered, total int) {
	for _, s := range sections {
		total++
		if len(bySection[s.ID]) > 0 {
			covered++
		}
	}
	return covered, total
}

func resolveRepoRoot(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(joinPath(dir, ".phase")); err == nil {
			return dir, nil
		}
		parent := parentDir(dir)
		if parent == dir {
			return "", errors.New(".phase file not found in any parent")
		}
		dir = parent
	}
}

// joinPath and parentDir are tiny path helpers that avoid importing path/filepath
// in the hot path. They handle plain forward / OS-separator joins.
func joinPath(parts ...string) string {
	out := ""
	for i, p := range parts {
		if i == 0 {
			out = p
			continue
		}
		if !strings.HasSuffix(out, string(os.PathSeparator)) {
			out += string(os.PathSeparator)
		}
		out += p
	}
	return out
}

func parentDir(p string) string {
	idx := strings.LastIndex(p, string(os.PathSeparator))
	if idx <= 0 {
		return string(os.PathSeparator)
	}
	return p[:idx]
}
