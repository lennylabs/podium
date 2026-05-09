// Package cmdharness builds and runs the Podium binaries during tests so
// integration and end-to-end tests exercise the real CLI rather than the
// internal library APIs.
//
// The harness compiles each binary once per `go test` invocation (cached
// via sync.OnceFunc) into the OS temp dir; subsequent calls reuse the
// build. Tests get a small Run helper that captures stdout, stderr, and
// exit code from a single subprocess invocation.
package cmdharness

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Result captures the outcome of one Run invocation.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run builds the named binary (e.g., "podium") and invokes it with args
// from cwd. The binary is rebuilt only on the first call per test
// process; subsequent calls reuse the cached path.
func Run(t testing.TB, binary, cwd string, args ...string) Result {
	t.Helper()
	bin := buildBinary(t, binary)

	cmd := exec.Command(bin, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(os.Environ(), "PODIUM_NO_AUTOSTANDALONE=1")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if exitErr, ok := err.(*exec.ExitError); ok {
		res.ExitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s %s: %v\nstderr:\n%s", binary, strings.Join(args, " "), err, stderr.String())
	}
	return res
}

var (
	buildOnce sync.Map // map[string]*sync.Once
	buildPath sync.Map // map[string]string
)

func buildBinary(t testing.TB, binary string) string {
	t.Helper()
	once, _ := buildOnce.LoadOrStore(binary, &sync.Once{})
	once.(*sync.Once).Do(func() {
		path, err := compileBinary(binary)
		if err != nil {
			t.Fatalf("compile %s: %v", binary, err)
		}
		buildPath.Store(binary, path)
	})
	v, ok := buildPath.Load(binary)
	if !ok {
		t.Fatalf("compile %s: cached path missing", binary)
	}
	return v.(string)
}

func compileBinary(binary string) (string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "podium-cmdharness-")
	if err != nil {
		return "", err
	}
	out := filepath.Join(tmp, binary)
	pkg := "./cmd/" + binary
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = root
	if buf, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build %s: %v\n%s", binary, err, buf)
	}
	return out, nil
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
			return "", fmt.Errorf("repo root not found from %s", dir)
		}
		dir = parent
	}
}
