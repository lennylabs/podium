package e2e

// End-to-end coverage of the registry's §8.3 audit destinations that only the
// compiled binary shows: the stdout destination, a broken standard-output
// pipe, and the shutdown flush after SIGINT.

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/auditharness"
	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// launchWithStdout starts `podium serve --standalone` on an ephemeral port
// with its standard output written to stdout and its standard error to a log
// file the readiness check reads. launchServer cannot be used here, because
// it merges both streams into one log.
func launchWithStdout(t *testing.T, env []string, stdout *os.File) *serverProc {
	t.Helper()
	reg := writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")})
	logf, err := os.CreateTemp(t.TempDir(), "server-*.log")
	if err != nil {
		t.Fatalf("server log: %v", err)
	}
	cmd := exec.Command(cmdharness.Bin(t, "podium"), "serve", "--standalone", "--layer-path", reg, "--bind", "127.0.0.1:0")
	cmd.Env = mergeEnv(append([]string{"HOME=" + t.TempDir()}, env...)...)
	cmd.Stdout = stdout
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	s := &serverProc{logPath: logf.Name(), cmd: cmd, exited: reapOnExit(cmd)}
	t.Cleanup(func() { stopProc(s.cmd) })
	s.awaitReady(t)
	return s
}

// interruptAndWait sends SIGINT to s and waits up to within for it to exit.
// It returns the exit status and the time the exit took.
func interruptAndWait(t *testing.T, s *serverProc, within time.Duration) (int, time.Duration) {
	t.Helper()
	start := time.Now()
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal server: %v", err)
	}
	select {
	case <-s.exited:
		return s.cmd.ProcessState.ExitCode(), time.Since(start)
	case <-time.After(within):
		t.Fatalf("server did not exit within %s of SIGINT\nlog:\n%s", within, s.log())
		return -1, 0
	}
}

// loadDomain makes one audited read against s and fails the test unless it
// answers 200.
func loadDomain(t *testing.T, s *serverProc) {
	t.Helper()
	if code, body := getRaw(t, s.BaseURL+"/v1/load_domain?path=finance"); code != http.StatusOK {
		t.Fatalf("load_domain = HTTP %d, want 200\nbody: %s", code, body)
	}
}

// Spec: §8.3 — with PODIUM_AUDIT_LOG_PATH=stdout, standard output carries
// audit records only: every line parses as one record with schema_version 1.
func TestAuditDestinations_StdoutCarriesOnlyAuditLines(t *testing.T) {
	t.Parallel()
	out, err := os.CreateTemp(t.TempDir(), "stdout-*.log")
	if err != nil {
		t.Fatal(err)
	}
	s := launchWithStdout(t, []string{"PODIUM_AUDIT_LOG_PATH=stdout"}, out)
	loadDomain(t, s)
	if code, _ := interruptAndWait(t, s, 20*time.Second); code != 0 {
		t.Fatalf("server exit status = %d, want 0\nlog:\n%s", code, s.log())
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var rec struct {
			Type          string `json:"type"`
			SchemaVersion *int   `json:"schema_version"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil || rec.Type == "" || rec.SchemaVersion == nil || *rec.SchemaVersion != 1 {
			t.Errorf("stdout line is not an audit record with schema_version 1: %s", sc.Text())
		}
		lines++
	}
	if lines == 0 || !strings.Contains(string(data), `"type":"domain.loaded"`) {
		t.Errorf("stdout holds %d line(s) and no domain.loaded record:\n%s", lines, data)
	}
}

// Spec: §8.3 — a reader of standard output that goes away does not end the
// registry. After the read end of the pipe closes, an audited call succeeds,
// /healthz answers, and the failed writes are counted as write_error drops.
func TestAuditDestinations_StdoutBrokenPipeKeepsServing(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	s := launchWithStdout(t, []string{"PODIUM_AUDIT_LOG_PATH=stdout"}, w)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	loadDomain(t, s)
	loadDomain(t, s)
	if code := getStatus(t, s.BaseURL+"/healthz"); code != http.StatusOK {
		t.Errorf("healthz = %d, want 200 after the stdout reader went away", code)
	}
	_, body := getRaw(t, s.BaseURL+"/metrics")
	if n := metricValue(string(body), `podium_audit_dropped_total{destination="stdout",reason="write_error"}`); n <= 0 {
		t.Errorf("podium_audit_dropped_total{stdout,write_error} = %v, want above 0\nlog:\n%s", n, s.log())
	}
	if s.hasExited() {
		t.Errorf("the registry exited after the stdout reader went away\nlog:\n%s", s.log())
	}
}

// metricValue returns the value of the sample whose name and labels are
// series in a Prometheus text exposition, or -1 when the sample is absent.
func metricValue(exposition, series string) float64 {
	for _, line := range strings.Split(exposition, "\n") {
		if rest, ok := strings.CutPrefix(line, series+" "); ok {
			v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
			if err == nil {
				return v
			}
		}
	}
	return -1
}

// Spec: §8.3 — SIGINT runs the shutdown flush. A record the receiver refused
// under a Retry-After that outlasts the test is delivered by the flush once
// the receiver accepts, and a receiver that stays down does not hold the exit
// past the 5-second flush budget. Both cases make their audited call before
// the signal.
func TestAuditDestinations_ShutdownFlushes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test cannot deliver SIGINT to a process on Windows")
	}
	t.Parallel()
	t.Run("delivered", func(t *testing.T) {
		t.Parallel()
		rcv := auditharness.NewReceiver(t)
		rcv.SetResponse(http.StatusServiceUnavailable, "60")
		s := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_AUDIT_LOG_PATH=" + rcv.URL()},
			"serve", "--standalone", "--layer-path", writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
		loadDomain(t, s)
		awaitRefused(t, rcv, "domain.loaded")
		rcv.SetResponse(http.StatusOK, "")
		if code, _ := interruptAndWait(t, s, 20*time.Second); code != 0 {
			t.Errorf("server exit status = %d, want 0\nlog:\n%s", code, s.log())
		}
		if _, ok := rcv.WaitForRecord(t, 0, auditharness.OfType("domain.loaded")); !ok {
			t.Errorf("the shutdown flush did not deliver the refused record\nlog:\n%s", s.log())
		}
	})
	t.Run("receiver down", func(t *testing.T) {
		t.Parallel()
		rcv := auditharness.NewReceiver(t)
		rcv.SetResponse(http.StatusServiceUnavailable, "")
		s := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_AUDIT_LOG_PATH=" + rcv.URL()},
			"serve", "--standalone", "--layer-path", writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")}))
		loadDomain(t, s)
		awaitRefused(t, rcv, "domain.loaded")
		if _, took := interruptAndWait(t, s, 8*time.Second); took > 8*time.Second {
			t.Errorf("the server took %s to exit, want within 8s", took)
		}
	})
}

// awaitRefused waits until the receiver has refused a request carrying a
// record of type typ.
func awaitRefused(t *testing.T, rcv *auditharness.Receiver, typ string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, req := range rcv.Requests() {
			if req.Status/100 != 2 && strings.Contains(string(req.Body), `"type":"`+typ+`"`) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the receiver never refused a %s record", typ)
}
