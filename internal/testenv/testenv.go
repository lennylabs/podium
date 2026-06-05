// Package testenv loads an optional env file so a single file can supply the
// credentials the integration and live tests read. It is test-support code;
// only test binaries import it.
//
// The file lists the variables the live and integration suites consult
// (Postgres, S3, the managed vector backends, the embedding providers, and the
// live-lane switches). Each test still gates itself on its own variables and
// skips when they are absent, so the file is optional: a run with no file
// behaves exactly as before.
package testenv

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultFile is the env file name searched for when PODIUM_TEST_ENV_FILE is
// unset. It sits at the module root, beside go.mod, and is gitignored.
const DefaultFile = "test.env"

var once sync.Once

// Load reads the env file and sets any variable it defines that is not already
// present in the process environment, so an explicit export or a CI secret
// takes precedence over the file. The path comes from PODIUM_TEST_ENV_FILE;
// when that is empty, Load searches upward from the working directory for a
// file named test.env at the module root. A missing or unreadable file is not
// an error. Load runs once per process and is safe to call from many TestMains.
func Load() { once.Do(load) }

func load() {
	path := os.Getenv("PODIUM_TEST_ENV_FILE")
	if path == "" {
		path = findUpward(DefaultFile)
	}
	if path != "" {
		loadFile(path)
	}
}

// loadFile reads path and sets each variable it defines that is not already in
// the environment. A missing or unreadable file is a no-op.
func loadFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, val, ok := parseLine(sc.Text())
		if !ok {
			continue
		}
		if _, present := os.LookupEnv(key); present {
			continue // the existing environment wins
		}
		_ = os.Setenv(key, val)
	}
}

// findUpward walks from the working directory toward the filesystem root
// looking for name. It stops at the directory that holds go.mod (the module
// root): the file lives there, so once go.mod is seen without the file beside
// it the search is over.
func findUpward(name string) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// parseLine parses one KEY=VALUE line. It ignores blank lines, comment lines
// beginning with '#', and a leading "export ". A value wrapped in matching
// single or double quotes has the quotes stripped. A line without '=' or with
// an empty key is skipped.
func parseLine(line string) (key, value string, ok bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", "", false
	}
	s = strings.TrimPrefix(s, "export ")
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(s[:eq])
	value = strings.TrimSpace(s[eq+1:])
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	if key == "" {
		return "", "", false
	}
	return key, value, true
}
