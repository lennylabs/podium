package audit_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
)

func boolPtr(b bool) *bool { return &b }

// spec: §8.2 — PIIRedactionConfig is default-on (absent config scrubs).
func TestPIIRedactionConfig_DefaultOn(t *testing.T) {
	t.Parallel()
	var cfg audit.PIIRedactionConfig // zero value
	if !cfg.Active() {
		t.Fatalf("zero-value config Active() = false, want true (default-on)")
	}
	s, err := cfg.BuildScrubber()
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatalf("default-on config built a nil scrubber")
	}
	if got := s.Scrub("my SSN is 123-45-6789"); strings.Contains(got, "123-45-6789") {
		t.Errorf("default scrubber left SSN intact: %q", got)
	}
}

// spec: §8.2 — an explicit enabled:false disables scrubbing (nil scrubber).
func TestPIIRedactionConfig_Disabled(t *testing.T) {
	t.Parallel()
	cfg := audit.PIIRedactionConfig{Enabled: boolPtr(false)}
	if cfg.Active() {
		t.Fatalf("Active() = true for enabled:false")
	}
	s, err := cfg.BuildScrubber()
	if err != nil {
		t.Fatal(err)
	}
	if s != nil {
		t.Fatalf("disabled config built a non-nil scrubber")
	}
	// A nil scrubber is a no-op on ScrubEvent (writes the query unredacted).
	ev := audit.Event{Type: audit.EventArtifactsSearched, Context: map[string]string{"query": "ssn 123-45-6789"}}
	if got := s.ScrubEvent(ev); got.Context["query"] != "ssn 123-45-6789" {
		t.Errorf("nil scrubber mutated query: %q", got.Context["query"])
	}
}

// spec: §8.2 — "Patterns configurable via PIIRedactionConfig": a custom
// pattern is appended to the defaults.
func TestPIIRedactionConfig_CustomPattern(t *testing.T) {
	t.Parallel()
	cfg := audit.PIIRedactionConfig{
		Patterns: []audit.PIIPattern{{Name: "badge", Regex: `BADGE-\d{4}`, Replacement: "[badge]"}},
	}
	s, err := cfg.BuildScrubber()
	if err != nil {
		t.Fatal(err)
	}
	got := s.Scrub("user BADGE-9981 email bob@acme.com")
	if strings.Contains(got, "BADGE-9981") {
		t.Errorf("custom pattern not applied: %q", got)
	}
	if strings.Contains(got, "bob@acme.com") {
		t.Errorf("default email pattern dropped after custom add: %q", got)
	}
}

// spec: §8.2 — an invalid custom regex is a configuration error.
func TestPIIRedactionConfig_InvalidPattern(t *testing.T) {
	t.Parallel()
	cfg := audit.PIIRedactionConfig{Patterns: []audit.PIIPattern{{Name: "bad", Regex: `(unclosed`}}}
	if _, err := cfg.BuildScrubber(); err == nil {
		t.Fatalf("expected error for invalid regex, got nil")
	}
}

// spec: §8.2 — ScrubEvent scrubs the free-text query only on search events
// and leaves other event types and the shared context map untouched.
func TestScrubEvent(t *testing.T) {
	t.Parallel()
	s := audit.NewPIIScrubber()

	t.Run("search event scrubbed", func(t *testing.T) {
		shared := map[string]string{"query": "find ssn 123-45-6789", "scope": "finance"}
		ev := audit.Event{Type: audit.EventArtifactsSearched, Context: shared}
		got := s.ScrubEvent(ev)
		if strings.Contains(got.Context["query"], "123-45-6789") {
			t.Errorf("query not scrubbed: %q", got.Context["query"])
		}
		if got.Context["scope"] != "finance" {
			t.Errorf("non-query field altered: %q", got.Context["scope"])
		}
		// Original shared map must not be mutated in place.
		if shared["query"] != "find ssn 123-45-6789" {
			t.Errorf("caller's shared map mutated: %q", shared["query"])
		}
	})

	t.Run("domains.searched scrubbed", func(t *testing.T) {
		ev := audit.Event{Type: audit.EventDomainsSearched, Context: map[string]string{"query": "call (415) 555-1234"}}
		if got := s.ScrubEvent(ev); strings.Contains(got.Context["query"], "555-1234") {
			t.Errorf("query not scrubbed: %q", got.Context["query"])
		}
	})

	t.Run("non-search event untouched", func(t *testing.T) {
		ev := audit.Event{Type: audit.EventArtifactLoaded, Context: map[string]string{"version": "1.0.0", "query": "123-45-6789"}}
		got := s.ScrubEvent(ev)
		if got.Context["query"] != "123-45-6789" {
			t.Errorf("non-search event scrubbed: %q", got.Context["query"])
		}
	})

	t.Run("empty query untouched", func(t *testing.T) {
		ev := audit.Event{Type: audit.EventArtifactsSearched, Context: map[string]string{"query": ""}}
		if got := s.ScrubEvent(ev); got.Context["query"] != "" {
			t.Errorf("empty query changed: %q", got.Context["query"])
		}
	})

	t.Run("nil scrubber no-op", func(t *testing.T) {
		var nilS *audit.PIIScrubber
		ev := audit.Event{Type: audit.EventArtifactsSearched, Context: map[string]string{"query": "123-45-6789"}}
		if got := nilS.ScrubEvent(ev); got.Context["query"] != "123-45-6789" {
			t.Errorf("nil scrubber mutated query: %q", got.Context["query"])
		}
	})
}

// spec: §8.2 — a scrubbed search event written to a FileSink persists the
// placeholder, never the raw PII.
func TestScrubEvent_PersistedRedacted(t *testing.T) {
	t.Parallel()
	sink := audit.NewMemory()
	s := audit.NewPIIScrubber()
	ev := s.ScrubEvent(audit.Event{
		Type:    audit.EventArtifactsSearched,
		Context: map[string]string{"query": "ssn 123-45-6789 card 4111111111111111"},
	})
	if err := sink.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	got := sink.Events()[0].Context["query"]
	for _, raw := range []string{"123-45-6789", "4111111111111111"} {
		if strings.Contains(got, raw) {
			t.Errorf("persisted query leaked %q: %s", raw, got)
		}
	}
}

// guard against an accidental dependency: the custom Add path still works.
func TestPIIScrubber_AddStillWorks(t *testing.T) {
	t.Parallel()
	s := audit.NewPIIScrubber()
	s.Add("token", regexp.MustCompile(`tok_[a-z0-9]+`), "[tok]")
	if got := s.Scrub("tok_abc123"); got != "[tok]" {
		t.Errorf("Add pattern not applied: %q", got)
	}
}
