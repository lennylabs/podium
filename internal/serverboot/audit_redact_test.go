package serverboot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/registry/core"
)

// spec: §8.2 — the audit adapter scrubs the free-text search query before
// the event reaches the sink (default-on), so PII never lands on disk.
func TestAuditEmitterFor_ScrubsQuery(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	emit := auditEmitterFor(sink, audit.NewPIIScrubber(), nil)
	emit(context.Background(), core.AuditEvent{
		Type:    "artifacts.searched",
		Caller:  "alice",
		Context: map[string]string{"query": "find ssn 123-45-6789 for bob@acme.com", "scope": "finance"},
	})

	body, err := readBytes(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, leaked := range []string{"123-45-6789", "bob@acme.com"} {
		if strings.Contains(got, leaked) {
			t.Errorf("audit log leaked PII %q:\n%s", leaked, got)
		}
	}
	if !strings.Contains(got, "[ssn-redacted]") || !strings.Contains(got, "[email-redacted]") {
		t.Errorf("audit log missing redaction placeholders:\n%s", got)
	}
	if !strings.Contains(got, "\"scope\":\"finance\"") {
		t.Errorf("non-query field dropped:\n%s", got)
	}
}

// spec: §8.2 — a nil scrubber (PODIUM_PII_REDACTION=false) writes the query
// unredacted. The toggle must be honored.
func TestAuditEmitterFor_ScrubDisabled(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	emit := auditEmitterFor(sink, nil, nil) // scrubbing disabled
	emit(context.Background(), core.AuditEvent{
		Type:    "domains.searched",
		Context: map[string]string{"query": "ssn 123-45-6789"},
	})
	body, _ := readBytes(path)
	if !strings.Contains(string(body), "123-45-6789") {
		t.Errorf("disabled scrub should write the raw query:\n%s", body)
	}
}

// spec: §8.2 — manifest-declared redaction: the adapter masks the context
// keys named in RedactKeys before the event reaches the sink.
func TestAuditEmitterFor_RedactsManifestFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	emit := auditEmitterFor(sink, audit.NewPIIScrubber(), nil)
	emit(context.Background(), core.AuditEvent{
		Type:       "artifact.loaded",
		Target:     "finance/payroll",
		Context:    map[string]string{"version": "9.9.9", "layer": "L"},
		RedactKeys: []string{"version"},
	})
	body, _ := readBytes(path)
	got := string(body)
	if strings.Contains(got, "9.9.9") {
		t.Errorf("redacted field value leaked:\n%s", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Errorf("missing [redacted] placeholder:\n%s", got)
	}
	if !strings.Contains(got, "\"layer\":\"L\"") {
		t.Errorf("non-redacted field dropped:\n%s", got)
	}
}

// spec: §8.2 — PIIRedactionConfig resolution: env PODIUM_PII_REDACTION
// wins over registry.yaml; absent config is default-on.
func TestPIIRedactionConfig_Resolution(t *testing.T) {
	t.Run("default on when unset", func(t *testing.T) {
		c := &Config{}
		if !c.piiRedaction.Active() {
			t.Errorf("default config not active")
		}
	})
	t.Run("env false disables", func(t *testing.T) {
		t.Setenv("PODIUM_PII_REDACTION", "false")
		c := LoadConfig()
		if c.piiRedaction.Active() {
			t.Errorf("PODIUM_PII_REDACTION=false did not disable")
		}
	})
	t.Run("env wins over yaml", func(t *testing.T) {
		t.Setenv("PODIUM_PII_REDACTION", "true")
		c := &Config{piiRedaction: audit.PIIRedactionConfig{Enabled: boolPtrSB(true)}}
		// env already resolved Enabled; yaml false must not override it.
		applyYAML(c, &yamlConfig{PIIRedaction: audit.PIIRedactionConfig{Enabled: boolPtrSB(false)}})
		if !c.piiRedaction.Active() {
			t.Errorf("yaml overrode env-set enable toggle")
		}
	})
	t.Run("yaml fills when env unset", func(t *testing.T) {
		c := &Config{}
		applyYAML(c, &yamlConfig{PIIRedaction: audit.PIIRedactionConfig{Enabled: boolPtrSB(false)}})
		if c.piiRedaction.Active() {
			t.Errorf("yaml enabled:false not applied when env unset")
		}
	})
	t.Run("custom patterns overlay from yaml", func(t *testing.T) {
		c := &Config{}
		applyYAML(c, &yamlConfig{PIIRedaction: audit.PIIRedactionConfig{
			Patterns: []audit.PIIPattern{{Name: "badge", Regex: `BADGE-\d+`, Replacement: "[b]"}},
		}})
		if len(c.piiRedaction.Patterns) != 1 {
			t.Fatalf("custom patterns not overlaid: %v", c.piiRedaction.Patterns)
		}
		s, err := c.piiRedaction.BuildScrubber()
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Scrub("BADGE-12"); got != "[b]" {
			t.Errorf("custom yaml pattern not applied: %q", got)
		}
	})
}

func boolPtrSB(b bool) *bool { return &b }
