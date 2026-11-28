//go:build !podiumtest

package main

import "fmt"

// applyTestSeam refuses the hidden test flags in a release build, which
// defines none of them. The MCP server otherwise ignores a flag it does not
// know, so without the refusal a project configuration could pass a test
// flag to a shipped binary and see no error. No environment variable
// replaces the account home or the machine file in any build.
//
// Spec: §4.7.11 (Test seam)
func applyTestSeam(args []string, _ *systemPaths) error {
	for i := range args {
		if name, _, _, ok := testFlag(args, i); ok {
			return fmt.Errorf("flag provided but not defined: -%s", name)
		}
	}
	return nil
}
