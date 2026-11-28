//go:build podiumtest

package main

import (
	"errors"
	"fmt"
	"os"
)

// applyTestSeam applies the hidden test flags of a binary built with the
// podiumtest tag, which internal/testharness/cmdharness sets for end-to-end
// tests. --test-account-home replaces the account home; an empty value
// makes the lookup fail. --test-machine-config-dir replaces the directory
// of the machine file, and its check then also trusts the user of the
// process and a sticky temporary directory, because a test cannot create
// files that root owns. The release workflow refuses a binary built with
// the tag.
//
// Spec: §4.7.11 (Test seam)
func applyTestSeam(args []string, paths *systemPaths) error {
	for i := range args {
		name, value, hasValue, ok := testFlag(args, i)
		if !ok {
			continue
		}
		if !hasValue {
			return fmt.Errorf("flag needs an argument: -%s", name)
		}
		switch name {
		case "test-account-home":
			home := value
			paths.accountHome = func() (string, error) {
				if home == "" {
					return "", errors.New("test: the account lookup fails")
				}
				return home, nil
			}
		case "test-machine-config-dir":
			paths.machine.Dir = value
			paths.machine.TrustedUIDs = []uint32{uint32(os.Getuid())}
			paths.machine.AllowSticky = true
		}
	}
	return nil
}
