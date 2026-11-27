package serverboot

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// A listener that holds the configured bind address answers /healthz, and
// the fixture does not read that answer as this registry being ready: it
// waits for run's own listening line, so the boot runs to its bind refusal
// rather than being cancelled in the middle of its start.
func TestBootFixture_ForeignListenerIsNotReadiness(t *testing.T) {
	f := newBootFixture(t)
	held := f.holdBind(t)
	foreign := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = foreign.Serve(held) }()
	t.Cleanup(func() { _ = foreign.Close() })

	_, runErr := f.boot(t)
	if runErr == nil || !strings.Contains(runErr.Error(), "serve: bind "+f.addr) {
		t.Fatalf("run = %v, want the bind refusal for %s", runErr, f.addr)
	}
}

// The fixture binds port 0 and reads the address run bound from its
// listening line, so the address it hands to serving names the port the
// kernel picked rather than port 0.
func TestBootFixture_ReadsTheBoundAddress(t *testing.T) {
	f := newBootFixture(t)
	if _, err := f.bootWith(t, func() {
		if !healthOK(f.addr, time.Second) {
			t.Errorf("no /healthz answer at the address read from the log, %q", f.addr)
		}
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(f.addr, "127.0.0.1:") || strings.HasSuffix(f.addr, ":0") {
		t.Errorf("fixture address = %q, want the loopback port run bound", f.addr)
	}
}
