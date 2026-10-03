package sigstoreharness

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
)

// fatalRecorder records Fatalf instead of stopping the test, so must's
// failure path can be observed.
type fatalRecorder struct {
	testing.TB
	msg string
}

func (f *fatalRecorder) Helper() {}

func (f *fatalRecorder) Fatalf(format string, args ...any) {
	f.msg = format
	if len(args) > 0 {
		f.msg = args[0].(error).Error()
	}
}

// Spec: §4.7.9. Harness construction errors surface as a fatal setup
// failure, and the builders return errors for inputs they cannot encode.
func TestHarness_ReportsGenerationErrors(t *testing.T) {
	t.Parallel()
	rec := &fatalRecorder{TB: t}
	must(rec, errors.New("acme setup failure"))
	if rec.msg != "acme setup failure" {
		t.Fatalf("must recorded %q", rec.msg)
	}

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signNote("body\n", rsaKey); err == nil || !strings.Contains(err.Error(), "unsupported checkpoint key") {
		t.Fatalf("RSA checkpoint key: err = %v", err)
	}

	h := New(t)
	if _, err := h.fulcioInter.issue(rootSpec("acme", h.Clock()), "not a key"); err == nil {
		t.Fatal("issued a certificate over a non-key")
	}
}
