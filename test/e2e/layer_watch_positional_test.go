package e2e

// End-to-end coverage of the `podium layer watch <id>` invocation the spec
// writes: the layer ID is a positional operand, flags may follow it, and the
// --id flag the documentation showed earlier still names the layer. Giving
// both forms with different values is a usage error, because either choice
// would poll a layer the caller may not have meant.
//
// Spec: §7.3.1 (`podium layer watch <id> [--interval <duration>]`), §4.6
// (`git` and `local` sources polled by `podium layer watch <id>`).

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// startLayerWatch launches `podium layer watch` with args in the background
// and returns a watchProc the test stops through t.Cleanup.
func startLayerWatch(t *testing.T, args ...string) *watchProc {
	t.Helper()
	logf, err := os.CreateTemp(t.TempDir(), "layerwatch-*.log")
	if err != nil {
		t.Fatalf("watch log: %v", err)
	}
	cmd := exec.Command(cmdharness.Bin(t, "podium"), append([]string{"layer", "watch"}, args...)...)
	cmd.Env = mergeEnv("PODIUM_NO_AUTOSTANDALONE=1", "HOME="+t.TempDir())
	cmd.Stdin = bytes.NewReader(nil)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start layer watch: %v", err)
	}
	w := &watchProc{cmd: cmd, logPath: logf.Name()}
	t.Cleanup(func() { stopProc(w.cmd) })
	return w
}

// Spec: §7.3.1 — the positional form polls the named layer, with the flags
// written after the operand as the spec writes them.
func TestLayerWatch_PositionalID(t *testing.T) {
	t.Parallel()
	reg := orgLocalReg(t)
	srv := startServer(t, reg)
	orgMustRegisterLayer(t, srv.BaseURL, "watch-pos", reg)

	w := startLayerWatch(t, "watch-pos", "--interval", "1s", "--registry", srv.BaseURL)
	if !cliPollLog(w, "[reingest watch-pos]", 8*time.Second) {
		t.Errorf("layer watch <id> did not poll reingest within 8s\nlog:\n%s", w.log())
	}
}

// Spec: §7.3.1 — the positional and --id naming the same layer is one layer.
func TestLayerWatch_PositionalAndMatchingIDFlag(t *testing.T) {
	t.Parallel()
	reg := orgLocalReg(t)
	srv := startServer(t, reg)
	orgMustRegisterLayer(t, srv.BaseURL, "watch-both", reg)

	w := startLayerWatch(t, "--id", "watch-both", "watch-both", "--interval", "1s", "--registry", srv.BaseURL)
	if !cliPollLog(w, "[reingest watch-both]", 8*time.Second) {
		t.Errorf("layer watch did not poll reingest within 8s\nlog:\n%s", w.log())
	}
}

// Spec: §7.3.1 — a positional and an --id that name different layers are
// refused as a usage error before any request is sent.
func TestLayerWatch_ConflictingIDsExit2(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{"PODIUM_REGISTRY=http://127.0.0.1:1", "HOME=" + t.TempDir()},
		"layer", "watch", "alpha", "--id", "beta", "--interval", "1s")
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, `layer id "alpha" conflicts with --id "beta"`) {
		t.Errorf("stderr missing the conflict message:\n%s", res.Stderr)
	}
	if strings.Contains(res.Stdout, "[reingest") {
		t.Errorf("conflicting invocation polled a layer:\n%s", res.Stdout)
	}
}

// Spec: §7.3.1 — the command takes one layer ID, so a second operand is a
// usage error.
func TestLayerWatch_TwoPositionalsExit2(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", []string{"PODIUM_REGISTRY=http://127.0.0.1:1", "HOME=" + t.TempDir()},
		"layer", "watch", "alpha", "beta")
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "usage: podium layer watch <id>") {
		t.Errorf("stderr missing usage line:\n%s", res.Stderr)
	}
}
