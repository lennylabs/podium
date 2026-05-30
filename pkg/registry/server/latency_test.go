package server

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// obs is one captured latency observation.
type obs struct {
	op      string
	status  int
	elapsed time.Duration
}

// recordingObserver returns a LatencyObserver plus a pointer to the slice
// it appends to. The middleware invokes the observer synchronously after
// the handler returns, in the same goroutine, so no locking is needed.
func recordingObserver() (LatencyObserver, *[]obs) {
	var got []obs
	return func(op string, status int, elapsed time.Duration) {
		got = append(got, obs{op, status, elapsed})
	}, &got
}

// Spec: §7.1 — the operation key keeps SLO-budgeted meta-tools under their
// spec names, gives the other routes a stable key, and excludes the
// liveness/readiness probes and the long-lived SSE stream (which carry no
// SLO budget) by mapping them to "".
func TestOperationName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/v1/load_domain":         "load_domain",
		"/v1/search_domains":      "search_domains",
		"/v1/search_artifacts":    "search_artifacts",
		"/v1/load_artifact":       "load_artifact",
		"/v1/artifacts:batchLoad": "batch_load",
		"/v1/dependents":          "dependents",
		"/v1/scope/preview":       "scope_preview",
		"/v1/domain/analyze":      "domain_analyze",
		"/v1/quota":               "quota",
		"/v1/admin/grants":        "admin",
		"/v1/webhooks":            "webhooks",
		"/v1/webhooks/abc":        "webhooks",
		"/objects/sha256-abc":     "objects",
		"/scim/v2/Users":          "scim",
		"/healthz":                "",
		"/readyz":                 "",
		"/v1/events":              "",
		"/unknown":                "",
		"/":                       "",
	}
	for path, want := range cases {
		if got := operationName(path); got != want {
			t.Errorf("operationName(%q) = %q, want %q", path, got, want)
		}
	}
}

// Spec: §7.1 — the middleware reports one observation per served request
// (operation, status, elapsed) so a deployment can compare against the SLO
// budgets.
func TestWithLatencyObserver_RecordsObservation(t *testing.T) {
	t.Parallel()
	fn, got := recordingObserver()
	s := &Server{latency: fn}
	h := s.withLatencyObserver(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Spend a little time so the elapsed reading is non-zero.
		time.Sleep(2 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/load_domain", nil))

	if len(*got) != 1 {
		t.Fatalf("got %d observations, want 1", len(*got))
	}
	o := (*got)[0]
	if o.op != "load_domain" {
		t.Errorf("op = %q, want load_domain", o.op)
	}
	if o.status != http.StatusOK {
		t.Errorf("status = %d, want 200", o.status)
	}
	if o.elapsed < time.Millisecond {
		t.Errorf("elapsed = %v, want >= 1ms (handler slept 2ms)", o.elapsed)
	}
}

// Spec: §7.1 — the recorder captures the real status the client sees,
// whether the handler sets it explicitly or implicitly defaults to 200.
func TestWithLatencyObserver_CapturesStatus(t *testing.T) {
	t.Parallel()
	t.Run("explicit error status", func(t *testing.T) {
		t.Parallel()
		fn, got := recordingObserver()
		s := &Server{latency: fn}
		h := s.withLatencyObserver(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusNotFound)
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/load_artifact", nil))
		if len(*got) != 1 || (*got)[0].status != http.StatusNotFound {
			t.Fatalf("observations = %+v, want one status=404", *got)
		}
	})
	t.Run("implicit 200 on body write", func(t *testing.T) {
		t.Parallel()
		fn, got := recordingObserver()
		s := &Server{latency: fn}
		h := s.withLatencyObserver(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok")) // no WriteHeader call
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/search_artifacts", nil))
		if len(*got) != 1 || (*got)[0].status != http.StatusOK {
			t.Fatalf("observations = %+v, want one status=200", *got)
		}
	})
}

// Spec: §7.1 — liveness/readiness probes and the SSE event stream are not
// SLO operations, so the middleware passes them through without recording
// (and without dropping the request).
func TestWithLatencyObserver_SkipsUnobservedPaths(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/healthz", "/readyz", "/v1/events", "/unknown"} {
		fn, got := recordingObserver()
		s := &Server{latency: fn}
		served := false
		h := s.withLatencyObserver(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			served = true
			w.WriteHeader(http.StatusOK)
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		if !served {
			t.Errorf("%s: handler was not invoked", path)
		}
		if len(*got) != 0 {
			t.Errorf("%s: recorded %d observations, want 0", path, len(*got))
		}
	}
}

// Spec: §7.1 — with no observer configured the middleware adds zero
// overhead: it returns the wrapped handler unchanged.
func TestWithLatencyObserver_NilIsPassthrough(t *testing.T) {
	t.Parallel()
	s := &Server{} // latency == nil
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	gotHandler := s.withLatencyObserver(next)
	if reflect.ValueOf(gotHandler).Pointer() != reflect.ValueOf(next).Pointer() {
		t.Fatal("nil observer should return the wrapped handler unchanged")
	}
}

// Spec: §7.1 — the status-capturing wrapper must preserve http.Flusher so
// the /v1/events SSE handler (which type-asserts http.Flusher) still works
// behind the latency middleware. httptest.ResponseRecorder implements
// Flusher, so the assertion inside the handler exercises the forwarding.
func TestLatencyRecorder_PreservesFlusher(t *testing.T) {
	t.Parallel()
	fn, got := recordingObserver()
	s := &Server{latency: fn}
	sawFlusher := false
	h := s.withLatencyObserver(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if ok {
			sawFlusher = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: x\n\n"))
			flusher.Flush()
		}
	}))
	// /objects/ is observed, so the recorder wraps the writer here.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/objects/sha256-x", nil))
	if !sawFlusher {
		t.Fatal("handler did not see http.Flusher through the latency recorder")
	}
	if len(*got) != 1 || (*got)[0].op != "objects" || (*got)[0].status != http.StatusOK {
		t.Fatalf("observations = %+v, want one objects/200", *got)
	}
}
