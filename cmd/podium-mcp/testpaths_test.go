package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	synccfg "github.com/lennylabs/podium/pkg/sync"
)

// testPaths are system paths for a unit test: the account home is the
// HOME the test set, read when the loader asks, and the machine file lies
// in an empty directory of the test, so no test reads the home of the
// developer or /etc/podium.
func testPaths(t *testing.T) systemPaths {
	t.Helper()
	machine := filepath.Join(t.TempDir(), "machine")
	return systemPaths{
		accountHome: func() (string, error) {
			if h := os.Getenv("HOME"); h != "" {
				return h, nil
			}
			return "", errors.New("test: HOME is unset")
		},
		machine: synccfg.MachineFileOptions{Dir: machine},
	}
}
