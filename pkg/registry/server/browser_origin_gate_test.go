package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §6.3.4 — the browser-origin gate predicate: what counts as
// state-changing, what counts as cross-site evidence, which routes the
// exclusion names, and what is admitted. The boot-assembled cases, which pin
// that the gate wraps the boot mux rather than the catch-all, live in
// internal/serverboot.

// gated wraps a handler that records whether it ran.
func gated() (http.Handler, *bool) {
	ran := false
	h := server.BrowserOriginGate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ran = true
		w.WriteHeader(http.StatusOK)
	}))
	return h, &ran
}

func driveGate(t *testing.T, method, path string, headers map[string]string) (*http.Response, bool) {
	t.Helper()
	h, ran := gated()
	req := httptest.NewRequest(method, path, nil)
	req.Host = "registry.acme.com"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result(), *ran
}

// Spec: §6.3.4 — a state-changing request carrying cross-site browser-origin
// evidence is refused before the handler runs, whatever credential
// authenticated it. The credential is not a coordinate: the same evidence is
// driven under the session cookie and under the configured token header.
func TestBrowserOriginGate_RefusesCrossSiteEvidence(t *testing.T) {
	t.Parallel()
	evidence := map[string]map[string]string{
		"Sec-Fetch-Site cross-site": {"Sec-Fetch-Site": "cross-site"},
		"Sec-Fetch-Site same-site":  {"Sec-Fetch-Site": "same-site"},
		"Origin host differs":       {"Origin": "https://evil.example"},
		"Origin port differs":       {"Origin": "https://registry.acme.com:8443"},
		"Origin opaque":             {"Origin": "null"},
	}
	credentials := map[string]map[string]string{
		"session cookie": {"Cookie": server.CookieSession + "=a-token"},
		"token header":   {"Authorization": "Bearer a-token"},
		"no credential":  {},
	}
	for ename, ehdr := range evidence {
		for cname, chdr := range credentials {
			t.Run(ename+", "+cname, func(t *testing.T) {
				headers := map[string]string{}
				for k, v := range ehdr {
					headers[k] = v
				}
				for k, v := range chdr {
					headers[k] = v
				}
				resp, ran := driveGate(t, http.MethodPost, "/v1/layers", headers)
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", resp.StatusCode)
				}
				if ran {
					t.Error("the handler ran; the gate refuses before it")
				}
				e := envelopeCode(t, resp)
				if e.Code != "auth.csrf_invalid" {
					t.Errorf("code = %q, want auth.csrf_invalid", e.Code)
				}
				// The gate and the callback's transaction refusal share the
				// code, so the message is what tells them apart.
				if !strings.Contains(e.Message, "browser-origin check") {
					t.Errorf("message = %q, want the browser-origin check named", e.Message)
				}
			})
		}
	}
}

// Spec: §6.3.4 — a request that proves nothing is admitted rather than
// refused, and an Origin differing from Host only in scheme is same-site,
// because the scheme is not compared.
func TestBrowserOriginGate_Admits(t *testing.T) {
	t.Parallel()
	cases := map[string]map[string]string{
		"neither header, which is what a CLI sends": {},
		"Sec-Fetch-Site same-origin":                {"Sec-Fetch-Site": "same-origin"},
		"Sec-Fetch-Site none":                       {"Sec-Fetch-Site": "none"},
		"same-origin Origin":                        {"Origin": "https://registry.acme.com"},
		"Origin differing only in scheme":           {"Origin": "http://registry.acme.com"},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			resp, ran := driveGate(t, http.MethodPost, "/v1/layers", headers)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK || !ran {
				t.Errorf("status = %d, handler ran = %v, want the route's own success", resp.StatusCode, ran)
			}
		})
	}
}

// Spec: §6.3.4 — the predicate is the method. A safe method carrying the same
// cross-site evidence is admitted.
func TestBrowserOriginGate_SafeMethodsAdmitted(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		resp, ran := driveGate(t, method, "/v1/layers", map[string]string{"Sec-Fetch-Site": "cross-site"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !ran {
			t.Errorf("%s: status = %d, handler ran = %v, want admitted", method, resp.StatusCode, ran)
		}
	}
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		resp, _ := driveGate(t, method, "/v1/layers", map[string]string{"Sec-Fetch-Site": "cross-site"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", method, resp.StatusCode)
		}
	}
}

// Spec: §6.3.4 — sign-in and the callback are excluded by name as well as by
// method, so widening the method predicate does not pull them in and leave an
// expired session with no recovery.
func TestBrowserOriginGate_ExcludesSignInAndCallback(t *testing.T) {
	t.Parallel()
	for _, path := range []string{server.PathWebUISignIn, server.PathWebUICallback} {
		for _, headers := range []map[string]string{
			{"Sec-Fetch-Site": "cross-site"},
			{"Origin": "https://idp.example.com"},
		} {
			resp, ran := driveGate(t, http.MethodPost, path, headers)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK || !ran {
				t.Errorf("%s %v: status = %d, handler ran = %v, want admitted", path, headers, resp.StatusCode, ran)
			}
		}
	}
	// Sign-out is not excluded: its POST is what places it inside the gate.
	resp, ran := driveGate(t, http.MethodPost, server.PathWebUISignOut,
		map[string]string{"Sec-Fetch-Site": "cross-site"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || ran {
		t.Errorf("sign-out: status = %d, handler ran = %v, want 403 with no handler run", resp.StatusCode, ran)
	}
}
