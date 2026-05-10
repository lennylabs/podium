// Command coverage wraps `go test -coverprofile` and enforces budgets.
//
//	coverage report             Run tests with coverage; print summary.
//	coverage budget [-min N]    Run + assert overall coverage ≥ N%.
//	coverage per-package        Run + print per-package coverage.
//
// Stage D ships an overall budget. Per-file regression detection lands
// once a baseline file (coverage/baseline.txt) exists; for now the tool
// only enforces the floor.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const usageText = `usage: coverage <command> [flags]

Commands:
  report            Run go test -coverprofile and print summary.
  budget [-min N]   Run + assert overall coverage ≥ N (default 50).
  per-package       Print per-package coverage from coverage.out.

Flags:
  -repo <path>      Repository root (default: walk up to go.mod).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet("coverage", flag.ExitOnError)
	repo := fs.String("repo", "", "repository root")
	min := fs.Float64("min", 50.0, "minimum overall coverage percentage")
	if err := fs.Parse(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	root, err := resolveRepoRoot(*repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	switch cmd {
	case "report":
		os.Exit(report(root))
	case "budget":
		os.Exit(budget(root, *min))
	case "per-package":
		os.Exit(perPackage(root))
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n%s", cmd, usageText)
		os.Exit(2)
	}
}

func report(root string) int {
	if err := runWithCover(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	pct, err := overallCoverage(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Overall coverage: %.1f%%\n", pct)
	return 0
}

func budget(root string, min float64) int {
	if err := runWithCover(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	pct, err := overallCoverage(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Overall coverage: %.1f%% (budget: %.1f%%)\n", pct, min)
	if pct < min {
		fmt.Fprintf(os.Stderr, "Coverage %.1f%% is below the budget of %.1f%%.\n", pct, min)
		return 1
	}
	return 0
}

func perPackage(root string) int {
	if err := runWithCover(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	pkgs, err := packageCoverage(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	keys := make([]string, 0, len(pkgs))
	for k := range pkgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("%-60s %6s\n", "package", "cov")
	for _, k := range keys {
		fmt.Printf("%-60s %5.1f%%\n", k, pkgs[k])
	}
	return 0
}

func runWithCover(root string) error {
	cmd := exec.Command("go", "test",
		"-count=1",
		"-coverprofile=coverage.out",
		"-coverpkg=./...",
		"./...")
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go test failed:\n%s", out.String())
	}
	return nil
}

// overallCoverage runs `go tool cover -func` and parses the trailing
// `total: ... N%` line.
func overallCoverage(root string) (float64, error) {
	cmd := exec.Command("go", "tool", "cover", "-func=coverage.out")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("go tool cover: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "total:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pctStr := strings.TrimSuffix(fields[len(fields)-1], "%")
		return strconv.ParseFloat(pctStr, 64)
	}
	return 0, errors.New("no total: line in `go tool cover` output")
}

// packageCoverage parses coverage.out and computes per-package
// coverage. Lines look like:
//
//	mode: atomic
//	github.com/lennylabs/podium/pkg/foo/bar.go:12.5,15.10 3 1
func packageCoverage(root string) (map[string]float64, error) {
	data, err := os.ReadFile(root + "/coverage.out")
	if err != nil {
		return nil, err
	}
	type counts struct {
		hit, total int
	}
	pkg := map[string]*counts{}
	for _, raw := range strings.Split(string(data), "\n") {
		if raw == "" || strings.HasPrefix(raw, "mode:") {
			continue
		}
		fields := strings.Fields(raw)
		if len(fields) < 3 {
			continue
		}
		path := fields[0]
		// path looks like "<importpath>/file.go:start.col,end.col"
		colon := strings.Index(path, ":")
		if colon < 0 {
			continue
		}
		fullPath := path[:colon]
		slash := strings.LastIndex(fullPath, "/")
		var pkgName string
		if slash < 0 {
			pkgName = fullPath
		} else {
			pkgName = fullPath[:slash]
		}
		statements, err1 := strconv.Atoi(fields[1])
		hits, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			continue
		}
		c := pkg[pkgName]
		if c == nil {
			c = &counts{}
			pkg[pkgName] = c
		}
		c.total += statements
		if hits > 0 {
			c.hit += statements
		}
	}
	out := map[string]float64{}
	for k, c := range pkg {
		if c.total == 0 {
			continue
		}
		out[k] = 100.0 * float64(c.hit) / float64(c.total)
	}
	return out, nil
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
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir, nil
		}
		parent := dir[:strings.LastIndex(dir, "/")]
		if parent == "" || parent == dir {
			return "", errors.New("go.mod not found")
		}
		dir = parent
	}
}
