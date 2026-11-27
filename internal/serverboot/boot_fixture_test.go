package serverboot

import (
	"net"
	"net/http"
	"strings"
	"testing"
)

// A process that took the fixture's port after freeAddr released it answers
// /healthz, but the fixture does not read that answer as this registry being
// ready. The boot runs to its own bind refusal instead of being cancelled in
// the middle of its start.
func TestBootFixture_ForeignListenerIsNotReadiness(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	foreign := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = foreign.Serve(ln) }()
	t.Cleanup(func() { _ = foreign.Close() })

	f := newBootFixture(t)
	f.addr = ln.Addr().String()
	t.Setenv("PODIUM_BIND", f.addr)
	_, runErr := f.boot(t)
	if runErr == nil || !strings.Contains(runErr.Error(), "serve: bind "+f.addr) {
		t.Fatalf("run = %v, want the bind refusal for %s", runErr, f.addr)
	}
}
