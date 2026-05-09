// Command phasegate orchestrates Podium's phased build.
//
//	phasegate status     - print active phase, test counts, next failing test.
//	phasegate next       - print one record describing the next failing test.
//	phasegate advance    - run the active phase's suite; if green, bump .phase.
//
// Stage 1 ships status as the primary signal. next and advance gain teeth in
// later stages once there are tests to organize.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/lennylabs/podium/tools/internal/specparser"
)

// MaxPhase is the highest phase number defined by the MVP build sequence
// (spec §10). phasegate advance refuses to move past this.
const MaxPhase = 19

const usageText = `usage: phasegate <command>

Commands:
  status     Print active phase and a one-screen summary.
  next       Print the next failing test (name, citation, summary).
  advance    Run the active phase suite; if green, bump .phase.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet("phasegate", flag.ExitOnError)
	if err := fs.Parse(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	switch cmd {
	case "status":
		os.Exit(status(root))
	case "next":
		os.Exit(next(root))
	case "advance":
		os.Exit(advance(root))
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n%s", cmd, usageText)
		os.Exit(2)
	}
}

func status(root string) int {
	phase, err := readPhase(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Active phase:  %d\n", phase)

	specTests, _ := specparser.WalkTests(root)
	specCiting := countCitingPhase(specTests, phase)
	specTotal := countAllSpecCiting(specTests)
	fmt.Printf("Spec tests:    %d cite §sections at phase ≤ %d (of %d total)\n",
		specCiting, phase, specTotal)

	counts, err := runGoTestCount(root, phase)
	if err != nil {
		fmt.Printf("Test status:   %v\n", err)
		return 0
	}
	fmt.Printf("Suite:         %d passing, %d failing, %d skipped\n",
		counts.passing, counts.failing, counts.skipped)

	switch {
	case counts.failing > 0:
		fmt.Printf("Phase %d: %d test(s) failing. Run `make next` for the next one to fix.\n",
			phase, counts.failing)
	case specCiting == 0:
		fmt.Printf("Phase %d: no spec-citing tests defined. Phase advance is blocked until tests exist.\n", phase)
	case phase >= MaxPhase:
		fmt.Printf("Phase %d: GREEN. Final MVP phase reached.\n", phase)
	default:
		fmt.Printf("Phase %d: GREEN. Run `make advance` to move to phase %d.\n", phase, phase+1)
	}
	return 0
}

// countCitingPhase returns the number of tests whose Phase tag is ≤ active
// AND whose Spec citation is a real §section (not "n/a"). These are the
// tests that prove a phase's spec assertions hold.
func countCitingPhase(tests []specparser.Test, active int) int {
	n := 0
	for _, t := range tests {
		if t.Phase < 0 || t.Phase > active {
			continue
		}
		if t.Citation.SectionID == "" || t.Citation.SectionID == "n/a" {
			continue
		}
		n++
	}
	return n
}

func countAllSpecCiting(tests []specparser.Test) int {
	n := 0
	for _, t := range tests {
		if t.Citation.SectionID == "" || t.Citation.SectionID == "n/a" {
			continue
		}
		n++
	}
	return n
}

func next(root string) int {
	phase, err := readPhase(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	rep, err := runGoTestReport(root, phase)
	if err != nil {
		fmt.Println("(no test report available)")
		return 1
	}
	if rep.firstFailing == nil {
		specTests, _ := specparser.WalkTests(root)
		specCiting := countCitingPhase(specTests, phase)
		if specCiting == 0 {
			fmt.Printf("phase %d has no spec-citing tests; nothing to drive implementation against.\n", phase)
			return 0
		}
		fmt.Println("no failing tests in the active phase. Run `make advance` to move forward.")
		return 0
	}
	f := rep.firstFailing
	fmt.Printf("name:     %s\n", f.name)
	if f.file != "" {
		if f.line > 0 {
			fmt.Printf("file:     %s:%d\n", f.file, f.line)
		} else {
			fmt.Printf("file:     %s\n", f.file)
		}
	}
	fmt.Printf("package:  %s\n", f.pkg)
	fmt.Printf("phase:    %d\n", f.phase)
	if f.citation != "" {
		fmt.Printf("citation: %s\n", f.citation)
		if f.note != "" {
			fmt.Printf("note:     %s\n", f.note)
		}
	} else {
		fmt.Printf("citation: (none — annotate the test with `// Spec: §X.Y title — assertion.`)\n")
	}
	if f.summary != "" {
		fmt.Println("failure:")
		for _, line := range strings.Split(f.summary, "\n") {
			fmt.Println("  " + line)
		}
	}
	return 0
}

func advance(root string) int {
	phase, err := readPhase(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if phase >= MaxPhase {
		fmt.Fprintf(os.Stderr, "active phase is %d; phase %d is the last MVP phase (spec §10). Nothing to advance to.\n", phase, MaxPhase)
		return 1
	}
	counts, err := runGoTestCount(root, phase)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test run failed: %v\n", err)
		return 1
	}
	if counts.failing > 0 {
		fmt.Fprintf(os.Stderr, "phase %d not green: %d failing test(s). Refusing to advance.\n",
			phase, counts.failing)
		return 1
	}
	specTests, _ := specparser.WalkTests(root)
	specCiting := countCitingPhase(specTests, phase)
	if specCiting == 0 {
		fmt.Fprintf(os.Stderr, "phase %d has no spec-citing tests. Refusing to advance: a green phase with no tests is not a green phase.\n", phase)
		return 1
	}
	if err := writePhase(root, phase+1); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Advanced from phase %d to %d.\n", phase, phase+1)
	return 0
}

// readPhase reads .phase. Stage 1 always returns the file's value or an
// error; the file is initialized to 0.
func readPhase(root string) (int, error) {
	data, err := os.ReadFile(filepath.Join(root, ".phase"))
	if err != nil {
		return 0, fmt.Errorf("read .phase: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse .phase: %w", err)
	}
	return n, nil
}

func writePhase(root string, n int) error {
	return os.WriteFile(filepath.Join(root, ".phase"), []byte(strconv.Itoa(n)+"\n"), 0o644)
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".phase")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New(".phase not found in any parent directory")
		}
		dir = parent
	}
}

type testCounts struct {
	passing int
	failing int
	skipped int
}

type failedTest struct {
	name     string
	pkg      string
	file     string
	line     int
	citation string
	note     string
	phase    int
	summary  string
}

type testReport struct {
	counts       testCounts
	firstFailing *failedTest
}

// goTestEvent matches a single line of `go test -json` output. Only the
// fields we consume are listed; the package emits more.
type goTestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// failureLineRegex matches the conventional Go test failure prefix that
// `t.Errorf` and `t.Fatalf` emit:
//
//	    foo_test.go:42: assertion failed
//
// Capture group 1 is the file basename, 2 is the line number, 3 is the
// optional message. Indented with whitespace in the actual output.
var failureLineRegex = regexp.MustCompile(`^\s*([^/\s:][^\s:]*\.go):(\d+):\s*(.*)$`)

// runGoTestCount runs the test suite and returns pass/fail/skip counts
// derived from `go test -json`.
func runGoTestCount(root string, phase int) (testCounts, error) {
	rep, err := runGoTestReport(root, phase)
	if err != nil {
		return testCounts{}, err
	}
	return rep.counts, nil
}

// runGoTestReport runs the test suite and decodes the JSON event stream
// per-event. Output events for failing tests are buffered so the failure
// record carries the actual assertion message and source line.
func runGoTestReport(root string, phase int) (testReport, error) {
	cmd := exec.Command("go", "test", "-json", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PODIUM_PHASE="+strconv.Itoa(phase))
	out, _ := cmd.CombinedOutput()

	type key struct{ pkg, test string }
	output := map[key][]string{}
	failed := []key{}

	var rep testReport
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		var ev goTestEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		// Top-level test events have a Test field starting with "Test"
		// and no slash. Subtests carry "/" in the name; we treat them
		// as their parent for accounting.
		if ev.Test == "" {
			continue
		}
		k := key{ev.Package, ev.Test}
		switch ev.Action {
		case "output":
			output[k] = append(output[k], ev.Output)
		case "pass":
			if !strings.Contains(ev.Test, "/") {
				rep.counts.passing++
			}
		case "fail":
			if !strings.Contains(ev.Test, "/") {
				rep.counts.failing++
				failed = append(failed, k)
			}
		case "skip":
			if !strings.Contains(ev.Test, "/") {
				rep.counts.skipped++
			}
		}
	}

	if len(failed) == 0 {
		return rep, nil
	}

	// Build the failure record for the first failing test.
	first := failed[0]
	tests, _ := specparser.WalkTests(root)
	annotated := indexTests(tests)

	rep.firstFailing = &failedTest{
		name: first.test,
		pkg:  first.pkg,
	}
	if t, ok := annotated[first.test]; ok {
		rep.firstFailing.file = relativePath(root, t.File)
		rep.firstFailing.line = t.Line
		rep.firstFailing.citation = t.Citation.SectionID
		_, rep.firstFailing.note = specparser.SplitNote(t.Citation.Note)
		if rep.firstFailing.note == "" {
			rep.firstFailing.note = t.Citation.Note
		}
		rep.firstFailing.phase = t.Phase
	}
	rep.firstFailing.summary = extractFailureSummary(output[first])
	return rep, nil
}

// indexTests indexes the parsed tests by name. When two tests share a
// name (very rare; would have to live in different packages), the first
// one wins.
func indexTests(tests []specparser.Test) map[string]specparser.Test {
	out := make(map[string]specparser.Test, len(tests))
	for _, t := range tests {
		if _, ok := out[t.Name]; ok {
			continue
		}
		out[t.Name] = t
	}
	return out
}

// extractFailureSummary distills the buffered output for a failing test
// into the assertion message and the surrounding context lines.
//
// `go test` output for a failure looks like:
//
//	=== RUN   TestFoo
//	    foo_test.go:42: got 7, want 8
//	    foo_test.go:43:   diff:
//	    foo_test.go:44:     -7
//	    foo_test.go:44:     +8
//	--- FAIL: TestFoo (0.00s)
//
// We keep the assertion lines (those matching failureLineRegex) and
// strip the boilerplate. If no assertion lines are present, fall back
// to the entire output stripped of `=== RUN` / `--- FAIL` framing.
func extractFailureSummary(lines []string) string {
	keep := []string{}
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\n")
		if strings.HasPrefix(strings.TrimSpace(line), "=== RUN") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "=== PAUSE") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "=== CONT") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "--- FAIL") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "--- PASS") {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		keep = append(keep, strings.TrimRight(line, " \t"))
	}
	if len(keep) > 12 {
		keep = append(keep[:12], "  … (output truncated)")
	}
	return strings.Join(keep, "\n")
}

func relativePath(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return rel
}
