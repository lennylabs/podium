// Command matrix audits coverage of documented spec matrices: every cell
// of every matrix should have at least one test that claims it via a
// `// Matrix: §X.Y (key1, key2, ...)` annotation.
//
//	matrix audit       Print missing cells; exit 1 if any are missing.
//	matrix list        Print every known matrix with its cell count.
//	matrix scaffold    Print Go test stubs for missing cells (stdout).
//
// Matrices are hardcoded in matrices.go. Adding a new matrix is a code
// change to keep the source of truth visible alongside the auditor.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lennylabs/podium/tools/internal/specparser"
)

const usageText = `usage: matrix <command> [flags]

Commands:
  audit      Print missing cells; exit 1 if any are missing.
  list       Print every known matrix with its cell count.
  scaffold   Print Go test stubs for missing cells (stdout).

Flags:
  -repo <path>   Repository root (default: walk up to .phase).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet("matrix", flag.ExitOnError)
	repo := fs.String("repo", "", "repository root")
	if err := fs.Parse(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	root, err := resolveRepoRoot(*repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	tests, err := specparser.WalkTests(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	covered := indexCoveredCells(tests)

	matrices := KnownMatrices()
	switch cmd {
	case "audit":
		os.Exit(audit(matrices, covered))
	case "list":
		listMatrices(matrices)
	case "scaffold":
		scaffold(matrices, covered)
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n%s", cmd, usageText)
		os.Exit(2)
	}
}

// audit prints every missing cell across the registered matrices and
// returns 1 if anything is uncovered.
func audit(matrices []Matrix, covered map[string]bool) int {
	missing := 0
	for _, m := range matrices {
		uncov := []string{}
		for _, cell := range m.Cells() {
			key := cellKey(m.ID, cell)
			if !covered[key] {
				uncov = append(uncov, formatCell(m.ID, cell))
			}
		}
		fmt.Printf("%s — %s\n", m.ID, m.Title)
		fmt.Printf("  total cells: %d, missing: %d\n", len(m.Cells()), len(uncov))
		if len(uncov) > 0 {
			missing += len(uncov)
			sort.Strings(uncov)
			for _, line := range uncov {
				fmt.Println("    " + line)
			}
		}
	}
	if missing > 0 {
		fmt.Printf("\n%d cell(s) missing across %d matrices.\n", missing, len(matrices))
		return 1
	}
	fmt.Println("\nAll matrix cells covered.")
	return 0
}

func listMatrices(matrices []Matrix) {
	for _, m := range matrices {
		fmt.Printf("%s   %d cells   %s\n", m.ID, len(m.Cells()), m.Title)
	}
}

// scaffold prints Go test function stubs for every uncovered cell.
// The stub is a starting point; the developer fills in the assertion.
func scaffold(matrices []Matrix, covered map[string]bool) {
	for _, m := range matrices {
		for _, cell := range m.Cells() {
			key := cellKey(m.ID, cell)
			if covered[key] {
				continue
			}
			testName := stubName(m, cell)
			fmt.Printf(`// Spec: %s %s.
// Phase: %d
// Matrix: %s (%s)
func %s(t *testing.T) {
	testharness.RequirePhase(t, %d)
	t.Parallel()
	// TODO: assert the spec contract for this cell.
	t.Skip("not implemented")
}

`,
				m.ID, m.Title, m.Phase, m.ID, strings.Join(cell, ", "),
				testName, m.Phase)
		}
	}
}

func stubName(m Matrix, cell []string) string {
	parts := []string{"Test"}
	parts = append(parts, m.StubPrefix)
	for _, k := range cell {
		parts = append(parts, sanitize(k))
	}
	return strings.Join(parts, "_")
}

// sanitize converts an arbitrary key into a Go-identifier-safe segment.
func sanitize(s string) string {
	var b strings.Builder
	upper := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if upper {
				if r >= 'a' && r <= 'z' {
					b.WriteRune(r - 32)
				} else {
					b.WriteRune(r)
				}
				upper = false
			} else {
				b.WriteRune(r)
			}
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
			upper = false
		case r == '-' || r == '_' || r == ':':
			upper = true
		}
	}
	return b.String()
}

// indexCoveredCells walks every test's Matrix annotations and returns a
// set keyed by cellKey.
func indexCoveredCells(tests []specparser.Test) map[string]bool {
	out := map[string]bool{}
	for _, t := range tests {
		for _, cell := range t.Matrix {
			out[cellKey(cell.Matrix, cell.Keys)] = true
		}
	}
	return out
}

func cellKey(matrixID string, cell []string) string {
	return matrixID + "(" + strings.Join(cell, ",") + ")"
}

func formatCell(matrixID string, cell []string) string {
	return matrixID + "(" + strings.Join(cell, ", ") + ")"
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
		if _, err := os.Stat(dir + string(os.PathSeparator) + ".phase"); err == nil {
			return dir, nil
		}
		parent := parentDir(dir)
		if parent == dir {
			return "", errors.New(".phase not found")
		}
		dir = parent
	}
}

func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == os.PathSeparator {
			if i == 0 {
				return string(os.PathSeparator)
			}
			return p[:i]
		}
	}
	return p
}
