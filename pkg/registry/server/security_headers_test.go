package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §13.10 — every response from the origin the web UI and the browser
// session share carries the hardening headers, so the client-side markdown
// sanitizer is not the only barrier between an artifact body and that origin.
func TestSecurityHeaders_SetOnEveryResponse(t *testing.T) {
	t.Parallel()
	handler := server.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html>"))
	}))

	for _, target := range []string{"/ui/", "/v1/search_artifacts"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		for header, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "no-referrer",
		} {
			if got := rec.Header().Get(header); got != want {
				t.Errorf("%s: %s = %q, want %q", target, header, got, want)
			}
		}
		csp := rec.Header().Get("Content-Security-Policy")
		for _, directive := range []string{
			"default-src 'self'",
			"script-src 'self'",
			"img-src 'self' data:",
			"object-src 'none'",
			"base-uri 'self'",
			"form-action 'self'",
			"frame-ancestors 'none'",
		} {
			if !strings.Contains(csp, directive) {
				t.Errorf("%s: Content-Security-Policy %q omits %q", target, csp, directive)
			}
		}
		// The panel sets a custom property through a style attribute, which a
		// browser attributes to style-src, so the policy admits inline style
		// and no foreign stylesheet.
		if !strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
			t.Errorf("%s: Content-Security-Policy %q does not admit the panel's inline style", target, csp)
		}
		if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
			t.Errorf("%s: Content-Security-Policy %q admits inline script", target, csp)
		}
	}
}

// Spec: §13.10 — the headers are written before the wrapped handler runs, so
// a handler that writes its status and body still sends them. A middleware
// that set them afterwards would send none of them.
func TestSecurityHeaders_SetBeforeTheHandlerWrites(t *testing.T) {
	t.Parallel()
	var seen string
	handler := server.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		seen = w.Header().Get("Content-Security-Policy")
		w.WriteHeader(http.StatusForbidden)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/layers", nil))
	if seen == "" {
		t.Fatal("the wrapped handler saw no Content-Security-Policy, so it was set too late")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("a refused response carries no X-Frame-Options")
	}
}
