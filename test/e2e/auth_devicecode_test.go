package e2e

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The device-code login flow must never launch the system
// browser during tests. The test harness pins PODIUM_NO_BROWSER=1 (see
// mergeEnv); this test proves it end-to-end by putting a fake `open` /
// `xdg-open` launcher on PATH that records any invocation, running a login that
// reaches the device-auth step (where login would auto-open the verification
// URL), and asserting the launcher was never called.
//
// A control sub-test clears PODIUM_NO_BROWSER and asserts the launcher IS
// invoked. That proves the shim genuinely intercepts the browser open, so the
// suppressed case is a real regression guard rather than a no-op. The fake
// launcher shadows the real `open`/`xdg-open`, so no real browser opens in
// either case.
func TestAuthDeviceCode_DoesNotOpenBrowser(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("login uses rundll32 on Windows; the fake-launcher shim is POSIX-only")
	}

	// installShim writes a fake open/xdg-open that records each call to a
	// marker file, returning the bin dir and the marker path.
	installShim := func(t *testing.T) (binDir, marker string) {
		t.Helper()
		dir := t.TempDir()
		marker = filepath.Join(dir, "browser-was-opened")
		binDir = filepath.Join(dir, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatalf("mkdir bin: %v", err)
		}
		quoted := "'" + strings.ReplaceAll(marker, "'", `'\''`) + "'"
		shim := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + quoted + "\n"
		for _, name := range []string{"open", "xdg-open"} {
			if err := os.WriteFile(filepath.Join(binDir, name), []byte(shim), 0o755); err != nil {
				t.Fatalf("write fake %s: %v", name, err)
			}
		}
		return binDir, marker
	}

	// runLogin runs a login that stays in the post-device-auth poll loop (the
	// stub keeps returning authorization_pending) until the bounded context
	// kills it, with the shim dir prepended to PATH and the given extra env.
	runLogin := func(t *testing.T, binDir string, extraEnv ...string) cliResult {
		t.Helper()
		stub := newOIDCStub(oidcStubConfig{tokenResponses: []string{`{"error":"authorization_pending"}`}})
		t.Cleanup(stub.Stop)
		env := append([]string{"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH")}, extraEnv...)
		res := oidcRunLogin(t, stub, env, 5*time.Second)
		// Sanity: login reached the verification-URL step where it would open
		// the browser, so a non-suppressed build would have invoked the shim.
		if !strings.Contains(res.Stderr, "Direct link:") {
			t.Fatalf("login did not reach the verification-URL step; stderr:\n%s", res.Stderr)
		}
		return res
	}

	t.Run("suppressed", func(t *testing.T) {
		t.Parallel()
		binDir, marker := installShim(t)
		runLogin(t, binDir) // harness pins PODIUM_NO_BROWSER=1
		if _, err := os.Stat(marker); err == nil {
			data, _ := os.ReadFile(marker)
			t.Errorf("login launched the browser despite suppression (marker present): %q", string(data))
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat marker: %v", err)
		}
	})

	t.Run("control_not_suppressed", func(t *testing.T) {
		t.Parallel()
		binDir, marker := installShim(t)
		// Explicitly clear the harness pin so the browser open fires into the
		// shim (never a real browser, which the shim shadows).
		runLogin(t, binDir, "PODIUM_NO_BROWSER=")
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("control: launcher not invoked when suppression is cleared (the guard would not catch a regression): %v", err)
		}
	})
}
