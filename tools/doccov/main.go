// Command doccov reports runnable-command coverage across Podium's
// documentation.
//
// A documentation page is "runnable" when it contains a fenced bash, sh,
// shell, console, or zsh block, or an untagged fenced block whose lines begin
// with a podium, make, curl, go, pip, npm, or docker command. Pure config or
// data blocks (yaml, json, toml, python, markdown, and other non-shell tags)
// are not runnable, even when they mention a command name in a string value.
//
// Every runnable page must appear in the checked-in manifest, mapped either to
// the feature-named end-to-end test that covers it (by the test's D-<slug>) or
// to an explicit waiver with a reason. doccov resolves a slug to its test file
// by scanning the `// End-to-end tests for <path> (D-<slug>).` header comments
// under test/e2e.
//
//	doccov report   — table of every runnable page and its manifest disposition.
//	doccov check    — fail (exit 1) on any coverage gap.
//
// check fails when:
//
//	a runnable page is absent from the manifest,
//	a mapped slug resolves to no end-to-end test file, or
//	a manifest entry names a page that no longer exists.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const usageText = `usage: doccov <command> [flags]

Commands:
  report   Print every runnable doc page with its manifest disposition.
  check    Fail (exit 1) on any runnable page missing from the manifest, any
           mapped slug with no test file, or any manifest entry for a deleted
           page.

Flags:
  -repo <path>       Repository root (default: walk up to the directory containing go.mod).
  -docs <path>       Docs directory relative to repo root (default: docs).
  -manifest <path>   Manifest path relative to repo root (default: tools/doccov/manifest.yaml).
  -e2e <path>        End-to-end test directory relative to repo root (default: test/e2e).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	cmd := os.Args[1]

	fs := flag.NewFlagSet("doccov", flag.ExitOnError)
	repo := fs.String("repo", "", "repository root")
	docs := fs.String("docs", "docs", "docs directory")
	manifestPath := fs.String("manifest", filepath.Join("tools", "doccov", "manifest.yaml"), "manifest path")
	e2e := fs.String("e2e", filepath.Join("test", "e2e"), "end-to-end test directory")
	if err := fs.Parse(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	root, err := resolveRepoRoot(*repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot resolve repo root: %v\n", err)
		os.Exit(2)
	}

	pages, err := ScanRunnablePages(root, *docs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan docs: %v\n", err)
		os.Exit(1)
	}

	manifest, err := LoadManifest(resolveUnderRoot(root, *manifestPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
		os.Exit(1)
	}

	slugs, err := ScanDocSlugs(resolveUnderRoot(root, *e2e))
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan e2e slugs: %v\n", err)
		os.Exit(1)
	}

	switch cmd {
	case "report":
		os.Exit(report(os.Stdout, pages, manifest, slugs))
	case "check":
		os.Exit(check(os.Stdout, root, pages, manifest, slugs))
	case "help", "-h", "--help":
		fmt.Fprint(os.Stdout, usageText)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n%s", cmd, usageText)
		os.Exit(2)
	}
}

// report prints every runnable page with its manifest disposition. Returns 0.
func report(w io.Writer, pages []string, m *Manifest, slugs map[string]string) int {
	byPath := m.byPath()
	covered, waived, unmapped := 0, 0, 0

	fmt.Fprintln(w, "disposition   doc page")
	fmt.Fprintln(w, "-----------   --------")
	for _, p := range pages {
		entry, ok := byPath[p]
		switch {
		case !ok:
			fmt.Fprintf(w, "UNMAPPED      %s\n", p)
			unmapped++
		case entry.Waiver != "":
			fmt.Fprintf(w, "waived        %s   (%s)\n", p, entry.Waiver)
			waived++
		default:
			file := slugs[entry.Slug]
			if file == "" {
				file = "<no test file>"
			}
			fmt.Fprintf(w, "covered       %s   -> %s (%s)\n", p, entry.Slug, file)
			covered++
		}
	}
	fmt.Fprintf(w, "\n%d runnable page(s): %d covered, %d waived, %d unmapped.\n",
		len(pages), covered, waived, unmapped)
	return 0
}

// check verifies the manifest fully covers the runnable pages and that every
// entry resolves. Returns 1 when it finds any problem.
func check(w io.Writer, root string, pages []string, m *Manifest, slugs map[string]string) int {
	byPath := m.byPath()
	runnable := map[string]bool{}
	for _, p := range pages {
		runnable[p] = true
	}

	var problems []string

	// 1. Every runnable page must be present in the manifest.
	for _, p := range pages {
		entry, ok := byPath[p]
		if !ok {
			problems = append(problems,
				fmt.Sprintf("runnable doc page not in manifest: %s (add a slug or a waiver)", p))
			continue
		}
		// 2. A mapped slug must resolve to an existing test file.
		if entry.Waiver == "" {
			if entry.Slug == "" {
				problems = append(problems,
					fmt.Sprintf("manifest entry for %s has neither a slug nor a waiver", p))
				continue
			}
			file, ok := slugs[entry.Slug]
			if !ok {
				problems = append(problems,
					fmt.Sprintf("%s maps to %s, which no end-to-end test header declares", p, entry.Slug))
				continue
			}
			if _, err := os.Stat(file); err != nil {
				problems = append(problems,
					fmt.Sprintf("%s maps to %s, whose test file %s does not exist", p, entry.Slug, file))
			}
		}
	}

	// 3. Every manifest entry must name a page that still exists, and a covering
	//    entry must point at a page the scanner still classifies as runnable.
	for _, e := range m.Pages {
		abs := filepath.Join(root, filepath.FromSlash(e.Path))
		if _, err := os.Stat(abs); err != nil {
			problems = append(problems,
				fmt.Sprintf("manifest entry points at a missing doc page: %s", e.Path))
			continue
		}
		if e.Waiver == "" && !runnable[e.Path] {
			problems = append(problems,
				fmt.Sprintf("manifest covers %s with %s, but the page no longer contains a runnable command (waive or remove it)", e.Path, e.Slug))
		}
	}

	if len(problems) == 0 {
		fmt.Fprintf(w, "doccov: %d runnable doc page(s), all mapped to a covering test or a waiver.\n", len(pages))
		return 0
	}
	sort.Strings(problems)
	fmt.Fprintf(w, "doccov found %d problem(s):\n\n", len(problems))
	for _, p := range problems {
		fmt.Fprintf(w, "  - %s\n", p)
	}
	return 1
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
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found in any parent")
		}
		dir = parent
	}
}

// resolveUnderRoot joins p against root when p is relative, and returns p
// unchanged when it is already absolute, so a caller may pass either form.
func resolveUnderRoot(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

// repoRel returns p relative to root using forward slashes, for stable output
// and manifest matching across platforms.
func repoRel(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(rel)
}

// normPath canonicalizes a manifest or scanned path to forward slashes.
func normPath(p string) string {
	return filepath.ToSlash(strings.TrimSpace(p))
}
