package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMaskedToken(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                           "(unset)",
		"   ":                        "(unset)",
		"short":                      "(set)",
		"twelvechars1":                "(set)",
		"abcdefghijklm":              "abcdefgh…",
		"verylongjwttokenpayload":    "verylong…",
	}
	for in, want := range cases {
		if got := maskedToken(in); got != want {
			t.Errorf("maskedToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnvOr(t *testing.T) {
	const key = "PODIUM_TEST_STATUS_ENVOR_XYZZY"
	t.Setenv(key, "")
	if got := envOr(key, "fb"); got != "fb" {
		t.Errorf("unset: %q", got)
	}
	t.Setenv(key, "from-env")
	if got := envOr(key, "fb"); got != "from-env" {
		t.Errorf("set: %q", got)
	}
}

func TestOrMissing(t *testing.T) {
	t.Parallel()
	if got := orMissing(""); !strings.Contains(got, "unset") {
		t.Errorf("empty: %q", got)
	}
	if got := orMissing("http://x"); got != "http://x" {
		t.Errorf("set: %q", got)
	}
}

func TestStatusCmd_OutputsTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ready":true,"mode":"ready"}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	out := captureStdout(t, func() {
		withStderr(t, func() {
			if rc := statusCmd(nil); rc != 0 {
				t.Errorf("statusCmd = %d", rc)
			}
		})
	})
	for _, want := range []string{"registry:", "harness:", "reachability:", "OK", "registry mode:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestStatusCmd_NoRegistryStillOK(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "")
	out := captureStdout(t, func() {
		withStderr(t, func() {
			if rc := statusCmd(nil); rc != 0 {
				t.Errorf("statusCmd = %d", rc)
			}
		})
	})
	if !strings.Contains(out, "registry:") {
		t.Errorf("missing registry header:\n%s", out)
	}
}

func TestStatusCmd_RegistryUnreachable(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1") // closed port
	out := captureStdout(t, func() {
		withStderr(t, func() {
			_ = statusCmd(nil)
		})
	})
	if !strings.Contains(out, "UNREACHABLE") {
		t.Errorf("expected UNREACHABLE marker:\n%s", out)
	}
}

func TestStatusCmd_RegistryNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", srv.URL)
	out := captureStdout(t, func() {
		withStderr(t, func() {
			_ = statusCmd(nil)
		})
	})
	if !strings.Contains(out, "HTTP 503") {
		t.Errorf("expected HTTP 503:\n%s", out)
	}
}

func TestDecodeHealthMode(t *testing.T) {
	t.Parallel()
	// Construct a real response with a JSON body.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"mode":"ready","ready":true}`))
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := decodeHealthMode(resp); got != "ready" {
		t.Errorf("got %q, want ready", got)
	}
}

func TestDecodeHealthMode_InvalidBodyReturnsEmpty(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := decodeHealthMode(resp); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
