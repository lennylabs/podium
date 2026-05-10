package audit_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
)

// Spec: §8.3 — FileSink writes events as JSON Lines.
func TestFileSink_AppendPersistsAsJSONLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	for _, evtType := range []audit.EventType{
		audit.EventArtifactPublished, audit.EventArtifactLoaded,
	} {
		if err := sink.Append(context.Background(), audit.Event{
			Type: evtType, Caller: "joan", Target: "x",
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Count(string(data), "\n")
	if lines != 2 {
		t.Errorf("got %d lines, want 2", lines)
	}
	if !strings.Contains(string(data), `"type":"artifact.published"`) {
		t.Errorf("missing artifact.published in: %s", data)
	}
}

// Spec: §8.6 — FileSink Verify walks the hash chain.
func TestFileSink_VerifyDetectsTampering(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	for _, target := range []string{"a", "b", "c"} {
		_ = sink.Append(context.Background(), audit.Event{
			Type: audit.EventArtifactLoaded, Target: target,
		})
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Errorf("Verify clean: %v", err)
	}

	// Tamper: rewrite the second line's target.
	data, _ := os.ReadFile(path)
	tampered := strings.Replace(string(data), `"target":"b"`, `"target":"c"`, 1)
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	freshSink, _ := audit.NewFileSink(path)
	err = freshSink.Verify(context.Background())
	if !errors.Is(err, audit.ErrChainBroken) {
		t.Errorf("got %v, want ErrChainBroken", err)
	}
}

// Spec: §8.2 — query text is regex-scrubbed for common PII patterns.
func TestPIIScrubber_DefaultPatterns(t *testing.T) {
	t.Parallel()
	s := audit.NewPIIScrubber()
	cases := []struct {
		in, want string
	}{
		{
			"contact us at finance@acme.com please",
			"contact us at [email-redacted] please",
		},
		{
			"my SSN is 123-45-6789",
			"my SSN is [ssn-redacted]",
		},
		{
			"call (415) 555-1234",
			"call [phone-redacted]",
		},
	}
	for _, c := range cases {
		got := s.Scrub(c.in)
		if got != c.want {
			t.Errorf("Scrub(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Spec: §8.2 — manifest-declared redactKeys replace the fields with
// [redacted].
func TestRedactFields(t *testing.T) {
	t.Parallel()
	got := audit.RedactFields(map[string]string{
		"bank_account": "12345",
		"description":  "ok",
	}, []string{"bank_account"})
	if got["bank_account"] != "[redacted]" {
		t.Errorf("bank_account = %q", got["bank_account"])
	}
	if got["description"] != "ok" {
		t.Errorf("description leak: %q", got["description"])
	}
}

// Spec: §8.2 — custom regex patterns can extend the scrubber.
func TestPIIScrubber_CustomPattern(t *testing.T) {
	t.Parallel()
	s := audit.NewPIIScrubber()
	s.Add("api-key", regexp.MustCompile(`sk-[A-Za-z0-9]{16}`), "[api-key-redacted]")
	in := "key=sk-abcdefghijklmnop trailing"
	got := s.Scrub(in)
	if !strings.Contains(got, "[api-key-redacted]") {
		t.Errorf("got %q, want redaction", got)
	}
}
