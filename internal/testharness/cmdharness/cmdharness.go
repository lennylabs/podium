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
	// HOME (and USERPROFILE on Windows) and the working directory are both
	// pinned to a per-test temp dir so neither §7.5.2 config scope leaks the
	// developer's real ~/.podium into a test that assumes none: HOME isolates
	// the user-global scope, and running in an empty temp dir keeps workspace
	// discovery from walking up out of the repo into ~/.podium and reading it
	// as a project-shared config. The dir is stable across Run calls in one
	// test, so global config a test writes in one call is visible to the next.
	home := IsolatedHome(t)
	if cwd == "" {
		cwd = home
	}
	cmd.Dir = cwd
	// PODIUM_NO_BROWSER suppresses the login command's verification-URL
	// browser auto-open so a device-code test never launches the system
	// browser; PODIUM_NO_AUTOSTANDALONE keeps a CLI path from spawning a daemon.
	cmd.Env = append(os.Environ(),
		"PODIUM_NO_AUTOSTANDALONE=1", "PODIUM_NO_BROWSER=1",
		"HOME="+home, "USERPROFILE="+home,
	)

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

// Bin builds the named binary once per test process and returns its
// path. Callers that need custom environment, stdin, or their own
// process lifecycle (background servers, MCP stdio, SIGINT teardown)
// construct their own exec.Command from this path; Run covers the
// common single-shot case.
func Bin(t testing.TB, binary string) string {
	t.Helper()
	return buildBinary(t, binary)
}

var (
	buildOnce sync.Map // map[string]*sync.Once
	buildPath sync.Map // map[string]string
	testHome  sync.Map // map[string]string: test name -> per-test temp HOME
)

// IsolatedHome returns a temporary HOME for the calling test, stable across the
// test's Run calls and isolated from the developer's real ~/.podium and from
// other tests. This keeps the §7.5.2 user-global config scope empty for the
// subprocess unless the test itself populates it under this HOME.
func IsolatedHome(t testing.TB) string {
	if v, ok := testHome.Load(t.Name()); ok {
		return v.(string)
	}
	h := t.TempDir()
	actual, _ := testHome.LoadOrStore(t.Name(), h)
	return actual.(string)
}

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
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repo root not found from %s", dir)
		}
		dir = parent
	}
}
