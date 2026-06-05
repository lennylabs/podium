package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// spec: SS 6.10 — a message prefixed with a namespaced code is
// split into the structured envelope: discrete code/message fields plus
// the human-readable summary on `error`. Bridge-originated codes also get
// their retryable flag and remediation hint filled in.
func TestErrorResult_NamespacedCodeEnvelope(t *testing.T) {
	t.Parallel()
	m := errorResult("network.registry_unreachable: dial tcp: refused")
	if m["code"] != "network.registry_unreachable" {
		t.Errorf("code = %v, want network.registry_unreachable", m["code"])
	}
	if m["message"] != "dial tcp: refused" {
		t.Errorf("message = %v, want the text after the code prefix", m["message"])
	}
	if m["error"] != "network.registry_unreachable: dial tcp: refused" {
		t.Errorf("error summary = %v, want the full code: message string", m["error"])
	}
	if m["retryable"] != true {
		t.Errorf("retryable = %v, want true for network.registry_unreachable", m["retryable"])
	}
	if s, _ := m["suggested_action"].(string); s == "" {
		t.Errorf("suggested_action empty, want a remediation hint")
	}
}

// Messages without a namespaced code prefix are internal errors outside
// the §6.10 taxonomy and stay a bare {"error": "<msg>"}.
func TestErrorResult_NonNamespacedStaysBare(t *testing.T) {
	t.Parallel()
	for _, msg := range []string{"unknown tool: load_x", "decode load_artifact: boom", "bad"} {
		m := errorResult(msg)
		if m["error"] != msg {
			t.Errorf("error = %v, want %q", m["error"], msg)
		}
		if _, has := m["code"]; has {
			t.Errorf("msg %q unexpectedly produced a code field: %v", msg, m)
		}
	}
}

// spec: SS 6.10 — errorResultFrom propagates a
// decoded registry envelope verbatim, including the details object the
// spec example carries (runtime_iss for auth.untrusted_runtime).
func TestErrorResultFrom_PropagatesRegistryEnvelope(t *testing.T) {
	t.Parallel()
	re := &registryError{
		Status:          http.StatusForbidden,
		Code:            "auth.untrusted_runtime",
		Message:         "Runtime 'managed-runtime-x' is not registered with the registry.",
		Details:         map[string]any{"runtime_iss": "managed-runtime-x"},
		Retryable:       false,
		SuggestedAction: "Register the runtime's signing key via 'podium admin runtime register'.",
	}
	m := errorResultFrom(re)
	if m["code"] != "auth.untrusted_runtime" {
		t.Errorf("code = %v, want auth.untrusted_runtime", m["code"])
	}
	details, ok := m["details"].(map[string]any)
	if !ok || details["runtime_iss"] != "managed-runtime-x" {
		t.Errorf("details = %v, want runtime_iss=managed-runtime-x", m["details"])
	}
	if m["suggested_action"] != re.SuggestedAction {
		t.Errorf("suggested_action = %v, want %q", m["suggested_action"], re.SuggestedAction)
	}
}

// A non-registryError falls back to errorResult on its message.
func TestErrorResultFrom_NonRegistryError(t *testing.T) {
	t.Parallel()
	m := errorResultFrom(errPlain("config.unknown_harness: no adapter for 'bogus'"))
	if m["code"] != "config.unknown_harness" {
		t.Errorf("code = %v, want config.unknown_harness", m["code"])
	}
}

type errPlain string

func (e errPlain) Error() string { return string(e) }

// spec: SS 6.10 — parseRegistryError decodes a structured
// envelope and falls back to an HTTP status string for non-envelope
// bodies.
func TestParseRegistryError(t *testing.T) {
	t.Parallel()
	env := `{"code":"registry.not_found","message":"no such artifact","retryable":false,"suggested_action":"check the id"}`
	err := parseRegistryError(http.StatusNotFound, []byte(env))
	re, ok := err.(*registryError)
	if !ok {
		t.Fatalf("got %T, want *registryError", err)
	}
	if re.Code != "registry.not_found" || re.Message != "no such artifact" {
		t.Errorf("decoded = %+v, want code/message from envelope", re)
	}
	if re.SuggestedAction != "check the id" {
		t.Errorf("suggested_action = %q, want passthrough", re.SuggestedAction)
	}

	// Non-envelope body: keep the status in the message.
	fb := parseRegistryError(http.StatusBadGateway, []byte("upstream boom"))
	fre, _ := fb.(*registryError)
	if fre == nil || fre.Code != "" {
		t.Fatalf("fallback = %+v, want empty code", fre)
	}
	if got := fre.Error(); got != "HTTP 502: upstream boom" {
		t.Errorf("fallback Error() = %q, want HTTP 502 prefix", got)
	}
}

func TestSplitNamespacedCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantCode string
		wantRest string
	}{
		{"network.registry_unreachable: x", "network.registry_unreachable", "x"},
		{"materialize.signature_invalid: bad sig", "materialize.signature_invalid", "bad sig"},
		{"unknown tool: load_x", "", "unknown tool: load_x"},
		{"decode load_artifact: boom", "", "decode load_artifact: boom"},
		{"no colon here", "", "no colon here"},
		{"single: word", "", "single: word"},
	}
	for _, c := range cases {
		code, rest := splitNamespacedCode(c.in)
		if code != c.wantCode || rest != c.wantRest {
			t.Errorf("splitNamespacedCode(%q) = (%q,%q), want (%q,%q)", c.in, code, rest, c.wantCode, c.wantRest)
		}
	}
}

// spec: SS 6.10 — end to end: when the registry returns a 4xx
// with a structured envelope, the bridge's tool-call result carries the
// discrete code, details, retryable, and suggested_action fields rather
// than collapsing them into an opaque "HTTP <status>: <body>" string.
func TestProxyGet_DecodesRegistryEnvelopeEndToEnd(t *testing.T) {
	t.Parallel()
	const envBody = `{"code":"auth.untrusted_runtime",` +
		`"message":"Runtime 'managed-runtime-x' is not registered with the registry.",` +
		`"details":{"runtime_iss":"managed-runtime-x"},` +
		`"retryable":false,` +
		`"suggested_action":"Register the runtime's signing key via 'podium admin runtime register'."}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(envBody))
	}))
	t.Cleanup(ts.Close)

	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	out := srv.proxyGet("/v1/load_domain", map[string]any{"path": "finance"}, nil)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("proxyGet result = %T, want map", out)
	}
	if m["code"] != "auth.untrusted_runtime" {
		t.Errorf("code = %v, want auth.untrusted_runtime", m["code"])
	}
	details, ok := m["details"].(map[string]any)
	if !ok || details["runtime_iss"] != "managed-runtime-x" {
		t.Errorf("details = %v, want runtime_iss=managed-runtime-x", m["details"])
	}
	if _, ok := m["retryable"].(bool); !ok {
		t.Errorf("retryable missing/!bool: %v", m["retryable"])
	}
	if m["suggested_action"] == "" || m["suggested_action"] == nil {
		t.Errorf("suggested_action empty, want passthrough from envelope")
	}
	// The human-readable summary remains a string for host display.
	if _, ok := m["error"].(string); !ok {
		t.Errorf("error summary missing/!string: %v", m["error"])
	}
}
