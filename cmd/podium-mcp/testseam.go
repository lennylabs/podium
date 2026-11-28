package main

import (
	"strings"

	synccfg "github.com/lennylabs/podium/pkg/sync"
)

// systemPaths are the locations the MCP server reads its user sources
// from: the account home of the process and the machine file. The
// configuration loader receives them, so a unit test passes its own and no
// test reads or writes the home of the developer or of the CI runner.
type systemPaths struct {
	// accountHome resolves the home directory the account database records
	// for the user of the process.
	accountHome func() (string, error)
	// machine selects the machine file and the owners its check trusts.
	machine synccfg.MachineFileOptions
	// tufMirror replaces the Sigstore TUF repository; empty selects the
	// production repository. Only a test sets it.
	tufMirror string
}

// productionPaths are the system paths of a shipped binary.
func productionPaths() systemPaths {
	return systemPaths{accountHome: accountHome}
}

// testFlagNames are the hidden flags of a binary built with the podiumtest
// tag. A release build refuses each of them as an undefined flag.
//
// Spec: §4.7.11 (Test seam)
var testFlagNames = []string{"test-account-home", "test-machine-config-dir"}

// testFlag returns the test flag that tok names and its value, and whether
// tok names one: --name, -name, --name=value, or -name=value.
func testFlag(args []string, i int) (name, value string, hasValue, ok bool) {
	tok := args[i]
	if !strings.HasPrefix(tok, "-") {
		return "", "", false, false
	}
	key, val, eq := strings.Cut(strings.TrimLeft(tok, "-"), "=")
	for _, n := range testFlagNames {
		if key != n {
			continue
		}
		if eq {
			return n, val, true, true
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			return n, args[i+1], true, true
		}
		return n, "", false, true
	}
	return "", "", false, false
}
