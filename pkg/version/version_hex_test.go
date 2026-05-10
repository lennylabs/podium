package version

import (
	"errors"
	"strings"
	"testing"
)

// Spec: §4.7.6 — content_hash pins are SHA-256 digests; ParsePin
// must reject 64-char strings that contain non-hex characters
// even though the length matches.
func TestParsePin_RejectsNonHexHash(t *testing.T) {
	t.Parallel()
	bad := "sha256:" + strings.Repeat("g", 64) // length 64 but g is not hex
	_, err := ParsePin(bad)
	if !errors.Is(err, ErrInvalidPin) {
		t.Errorf("ParsePin(%q) = %v, want ErrInvalidPin", bad, err)
	}
}

// Spec: §4.7.6 — ParsePin accepts canonical hex-only digests.
func TestParsePin_AcceptsHexHash(t *testing.T) {
	t.Parallel()
	good := "sha256:" + strings.Repeat("a", 64)
	pin, err := ParsePin(good)
	if err != nil {
		t.Fatalf("ParsePin(%q): %v", good, err)
	}
	if pin.Kind != PinContentHash {
		t.Errorf("Kind = %v, want PinContentHash", pin.Kind)
	}
}
