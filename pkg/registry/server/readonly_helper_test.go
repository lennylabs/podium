package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// spec: §13.2.1 — rejectIfReadOnly is the single choke point
// for the write-rejection code. It emits HTTP 503 with the registry.read_only
// envelope (the spec defines no config.read_only) and reports the rejection
// so the handler returns. The §6.10 retryable flag is true because read-only
// mode clears on its own once the primary recovers.
func TestRejectIfReadOnly(t *testing.T) {
	t.Parallel()

	t.Run("nil tracker does not reject", func(t *testing.T) {
		rec := httptest.NewRecorder()
		if rejectIfReadOnly(rec, nil) {
			t.Fatal("nil tracker rejected")
		}
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Fatalf("nil tracker wrote a response: status=%d body=%q", rec.Code, rec.Body)
		}
	})

	t.Run("ready mode does not reject", func(t *testing.T) {
		m := NewModeTracker() // defaults to ModeReady
		rec := httptest.NewRecorder()
		if rejectIfReadOnly(rec, m) {
			t.Fatal("ready mode rejected")
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("ready mode wrote a response: %q", rec.Body)
		}
	})

	t.Run("read-only mode rejects with registry.read_only", func(t *testing.T) {
		m := NewModeTracker()
		m.Set(ModeReadOnly)
		rec := httptest.NewRecorder()
		if !rejectIfReadOnly(rec, m) {
			t.Fatal("read-only mode did not reject")
		}
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", rec.Code)
		}
		var env ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope %q: %v", rec.Body, err)
		}
		if env.Code != "registry.read_only" {
			t.Errorf("code = %q, want registry.read_only", env.Code)
		}
		if !env.Retryable {
			t.Error("retryable = false, want true for a transient read-only rejection")
		}
	})
}
