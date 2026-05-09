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
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lennylabs/podium/tools/internal/specparser"
)

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
		fmt.Printf("Phase %d: no spec-citing tests yet. Stage 2+ adds them.\n", phase)
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
		fmt.Println("(no test report available — Stage 1 minimum)")
		return 1
	}
	if rep.firstFailing == nil {
		fmt.Println("(no failing tests in the active phase)")
		return 0
	}
	f := rep.firstFailing
	fmt.Printf("name:     %s\n", f.name)
	fmt.Printf("file:     %s:%d\n", f.file, f.line)
	fmt.Printf("citation: %s\n", f.citation)
	fmt.Printf("summary:  %s\n", f.summary)
	return 0
}

func advance(root string) int {
	phase, err := readPhase(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
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
	file     string
	line     int
	citation string
	summary  string
}

type testReport struct {
	counts       testCounts
	firstFailing *failedTest
}

// runGoTestCount runs `go test ./...` with PODIUM_PHASE=phase and returns
// pass/fail/skip counts derived from `go test -json`. Stage 1 keeps this
// simple: it doesn't try to associate failures with annotations yet.
func runGoTestCount(root string, phase int) (testCounts, error) {
	rep, err := runGoTestReport(root, phase)
	if err != nil {
		return testCounts{}, err
	}
	return rep.counts, nil
}

func runGoTestReport(root string, phase int) (testReport, error) {
	cmd := exec.Command("go", "test", "-json", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PODIUM_PHASE="+strconv.Itoa(phase))
	out, _ := cmd.CombinedOutput()

	var rep testReport
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.Contains(line, `"Action":"pass"`) && strings.Contains(line, `"Test":"Test`):
			rep.counts.passing++
		case strings.Contains(line, `"Action":"fail"`) && strings.Contains(line, `"Test":"Test`):
			rep.counts.failing++
			if rep.firstFailing == nil {
				rep.firstFailing = parseFailing(line)
			}
		case strings.Contains(line, `"Action":"skip"`) && strings.Contains(line, `"Test":"Test`):
			rep.counts.skipped++
		}
	}
	return rep, nil
}

// parseFailing pulls the test name out of a `go test -json` fail event. The
// richer attribution (file, line, citation) lands when speccov can index the
// suite; for Stage 1, name + a placeholder summary is enough.
func parseFailing(line string) *failedTest {
	name := jsonStringField(line, "Test")
	pkg := jsonStringField(line, "Package")
	if name == "" {
		return nil
	}
	return &failedTest{
		name:    name,
		file:    pkg,
		summary: "see `go test ./...` output for details",
	}
}

// jsonStringField extracts a string field from a single-line JSON object.
// Adequate for the well-formed output of `go test -json`.
func jsonStringField(line, field string) string {
	key := `"` + field + `":"`
	i := strings.Index(line, key)
	if i < 0 {
		return ""
	}
	rest := line[i+len(key):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}
