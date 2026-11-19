package audit

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// Spec: §8.1 — a trace ID is 32 hexadecimal characters that are not all
// zeros, recorded in lowercase.
func TestValidTraceID(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"4bf92f3577b34da6a3ce929d0e0e4736":  "4bf92f3577b34da6a3ce929d0e0e4736",
		"4BF92F3577B34DA6A3CE929D0E0E4736":  "4bf92f3577b34da6a3ce929d0e0e4736",
		"00000000000000000000000000000000":  "",
		"4bf92f3577b34da6a3ce929d0e0e473":   "",
		"4bf92f3577b34da6a3ce929d0e0e47366": "",
		"4bf92f3577b34da6a3ce929d0e0e473g":  "",
		"":                                  "",
	} {
		if got := ValidTraceID(in); got != want {
			t.Errorf("ValidTraceID(%q) = %q, want %q", in, got, want)
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

// Spec: §8.1 — NewTraceID mints a valid, distinct trace ID per call, and a
// failed or all-zero draw yields no ID rather than an invalid one.
func TestNewTraceID(t *testing.T) {
	t.Parallel()
	a, b := NewTraceID(), NewTraceID()
	if ValidTraceID(a) != a || a == "" || a == b {
		t.Errorf("NewTraceID() = %q then %q, want two distinct valid IDs", a, b)
	}
	if got := newTraceIDFrom(failingReader{}); got != "" {
		t.Errorf("failed draw = %q, want none", got)
	}
	if got := newTraceIDFrom(bytes.NewReader(make([]byte, 16))); got != "" {
		t.Errorf("all-zero draw = %q, want none", got)
	}
	if got := newTraceIDFrom(strings.NewReader(strings.Repeat("\xab", 16))); got != strings.Repeat("ab", 16) {
		t.Errorf("draw = %q", got)
	}
}
