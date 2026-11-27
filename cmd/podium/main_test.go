package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain pins PODIUM_NO_BROWSER for the whole cmd/podium test binary so an
// in-process loginCmd test never launches the system browser. The device-code
// login flow auto-opens the verification URL (openBrowser); a test that drives
// loginCmd past the device-auth step (for example TestLoginCmd_PollFailureExits1,
// whose stub returns verification_uri https://x) would otherwise exec
// `open https://x`. Subprocess tests are covered by the e2e harness's
// mergeEnv and by cmdharness.Run; this covers the in-process callers here.
//
// It also points HOME (and USERPROFILE) at a temporary directory for the
// binary, so an in-process registry start, such as bootServer's, writes its
// default SQLite store, signing key, and standalone files there rather than
// under the developer's real home. A test that sets HOME itself still wins.
func TestMain(m *testing.M) {
	if os.Getenv("PODIUM_NO_BROWSER") == "" {
		_ = os.Setenv("PODIUM_NO_BROWSER", "1")
	}
	home, err := os.MkdirTemp("", "podium-cmd-home-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: temporary home: %v\n", err)
		os.Exit(1)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("USERPROFILE", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
