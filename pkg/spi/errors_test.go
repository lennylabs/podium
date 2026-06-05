package spi_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lennylabs/podium/pkg/spi"
)

// spec: §9.3 "Structured errors" — New populates the envelope and Error()
// returns the message verbatim (no code prefix) so a structured sentinel
// produces the same string its errors.New predecessor did.
func TestError_Basics(t *testing.T) {
	t.Parallel()
	e := spi.New("ingest.immutable_violation", "store: immutable_violation", false)
	if e.Error() != "store: immutable_violation" {
		t.Errorf("Error() = %q, want the message verbatim", e.Error())
	}
	if e.Code != "ingest.immutable_violation" || e.Retryable {
		t.Errorf("unexpected fields: %+v", e)
	}
}

// AsError recovers the structured error directly and through fmt.Errorf
// wrapping, and reports false for a plain error and for nil.
//
// spec: §9.3 — the out-of-process boundary recovers the envelope from any
// error a built-in SPI returns.
func TestAsError(t *testing.T) {
	t.Parallel()
	sentinel := spi.New("registry.not_found", "not found", false)
	if got, ok := spi.AsError(sentinel); !ok || got != sentinel {
		t.Errorf("AsError(sentinel) = %v,%v", got, ok)
	}
	wrapped := fmt.Errorf("layer x: %w", sentinel)
	if got, ok := spi.AsError(wrapped); !ok || got != sentinel {
		t.Errorf("AsError(wrapped) = %v,%v; want the wrapped sentinel", got, ok)
	}
	if _, ok := spi.AsError(errors.New("plain")); ok {
		t.Errorf("AsError(plain) = true, want false")
	}
	if _, ok := spi.AsError(nil); ok {
		t.Errorf("AsError(nil) = true, want false")
	}
}

// errors.Is matches a package-level sentinel pointer through fmt.Errorf
// wrapping, the property the SPI sentinel conversion must preserve. A distinct
// instance with identical fields is not the sentinel.
//
// spec: §9.3 — sentinels remain identity-comparable after the conversion.
func TestErrorsIs_PointerIdentity(t *testing.T) {
	t.Parallel()
	sentinel := spi.New("ingest.source_unreachable", "source: unreachable", true)
	other := spi.New("ingest.source_unreachable", "source: unreachable", true)
	wrapped := fmt.Errorf("ingest: %w", sentinel)
	if !errors.Is(wrapped, sentinel) {
		t.Error("errors.Is(wrapped, sentinel) = false, want true")
	}
	if errors.Is(wrapped, other) {
		t.Error("errors.Is(wrapped, other-instance) = true; sentinel identity must be by pointer")
	}
}
