package main

import (
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
func TestMain(m *testing.M) {
	if os.Getenv("PODIUM_NO_BROWSER") == "" {
		_ = os.Setenv("PODIUM_NO_BROWSER", "1")
	}
	os.Exit(m.Run())
}
