package server

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
)

func TestErrorEnvelopeFor_MapsCoreErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"not-found", core.ErrNotFound, "registry.not_found"},
		{"wrapped-not-found", &wrapErr{err: core.ErrNotFound}, "registry.not_found"},
		{"unavailable", core.ErrUnavailable, "registry.unavailable"},
		{"invalid-argument", core.ErrInvalidArgument, "registry.invalid_argument"},
		{"unknown", errors.New("mystery"), "registry.unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := errorEnvelopeFor(c.err)
			if env == nil || env.Code != c.want {
				t.Errorf("got %+v, want code=%s", env, c.want)
			}
		})
	}
}

func TestErrorEnvelopeFor_UnavailableMarksRetryable(t *testing.T) {
	t.Parallel()
	env := errorEnvelopeFor(core.ErrUnavailable)
	if !env.Retryable {
		t.Errorf("Retryable = false; want true for ErrUnavailable")
	}
}

type wrapErr struct {
	err error
}

func (w *wrapErr) Error() string { return "wrapped: " + w.err.Error() }
func (w *wrapErr) Unwrap() error { return w.err }

// spec: SS 6.10 — codes for transient conditions report
// retryable=true; hard-cap and not-found codes report false.
func TestEnrichEnvelope_RetryableByCode(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"registry.unavailable":            true,
		"quota.search_qps_exceeded":       true,
		"quota.materialize_rate_exceeded": true,
		"registry.read_only":              true,
		"quota.layer_count_exceeded":      false,
		"domain.not_found":                false,
		"registry.not_found":              false,
		"registry.invalid_argument":       false,
		"auth.forbidden":                  false,
	}
	for code, want := range cases {
		e := &ErrorResponse{Code: code, Message: "x"}
		enrichEnvelope(e)
		if e.Retryable != want {
			t.Errorf("code %q: retryable=%v, want %v", code, e.Retryable, want)
		}
	}
}

// spec: SS 6.10 — auth.untrusted_runtime carries the verbatim
// remediation hint from the spec example.
func TestEnrichEnvelope_SuggestedActionSpecExample(t *testing.T) {
	t.Parallel()
	e := &ErrorResponse{Code: "auth.untrusted_runtime", Message: "untrusted"}
	enrichEnvelope(e)
	const want = "Register the runtime's signing key via 'podium admin runtime register'."
	if e.SuggestedAction != want {
		t.Errorf("suggested_action = %q, want %q", e.SuggestedAction, want)
	}
}

// spec: SS 6.10 — the quota and unavailable codes all carry a
// non-empty operator remediation; an unmapped code carries none and is
// left untouched.
func TestEnrichEnvelope_SuggestedActionCoverage(t *testing.T) {
	t.Parallel()
	withHint := []string{
		"registry.unavailable",
		"quota.search_qps_exceeded",
		"quota.materialize_rate_exceeded",
		"quota.layer_count_exceeded",
		"quota.storage_exceeded",
	}
	for _, code := range withHint {
		e := &ErrorResponse{Code: code, Message: "x"}
		enrichEnvelope(e)
		if e.SuggestedAction == "" {
			t.Errorf("code %q: suggested_action empty, want a remediation hint", code)
		}
	}
	e := &ErrorResponse{Code: "registry.not_found", Message: "x"}
	enrichEnvelope(e)
	if e.SuggestedAction != "" || e.Retryable {
		t.Errorf("registry.not_found enriched unexpectedly: %+v", e)
	}
}

// enrichEnvelope must not downgrade a retryable flag a caller already set,
// nor overwrite a caller-supplied suggested_action.
func TestEnrichEnvelope_PreservesCallerValues(t *testing.T) {
	t.Parallel()
	e := &ErrorResponse{Code: "registry.not_found", Message: "x", Retryable: true}
	enrichEnvelope(e)
	if !e.Retryable {
		t.Error("explicit retryable=true was downgraded")
	}
	e2 := &ErrorResponse{Code: "registry.unavailable", Message: "x", SuggestedAction: "custom"}
	enrichEnvelope(e2)
	if e2.SuggestedAction != "custom" {
		t.Errorf("caller suggested_action overwritten: %q", e2.SuggestedAction)
	}
}

// spec: SS 6.10 — the batch-load per-item envelope now also
// carries the remediation hint for retryable conditions.
func TestErrorEnvelopeFor_AssignsSuggestedAction(t *testing.T) {
	t.Parallel()
	if env := errorEnvelopeFor(core.ErrUnavailable); env.SuggestedAction == "" {
		t.Errorf("ErrUnavailable envelope missing suggested_action: %+v", env)
	}
}

// spec: SS 6.10 — writeCoreError's default branch maps an
// unclassified failure to registry.unavailable, which the envelope must
// report as retryable. Before the fix this branch emitted retryable=false.
func TestWriteCoreError_DefaultBranchRetryable(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	(&Server{}).writeCoreError(rec, errors.New("backend exploded"))
	var got ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Code != "registry.unavailable" {
		t.Fatalf("code = %q, want registry.unavailable", got.Code)
	}
	if !got.Retryable {
		t.Errorf("retryable = false, want true for registry.unavailable")
	}
	if got.SuggestedAction == "" {
		t.Errorf("suggested_action empty, want a remediation hint")
	}
}
