package serverboot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/registry/core"
)

// Spec: §8.4/F-8.4.6 — the 1-year audit-event default must apply out of
// the box, so the enforcement interval defaults to a positive value
// (one day) rather than 0 (disabled).
func TestLoadConfig_RetentionEnabledByDefault(t *testing.T) {
	t.Setenv("PODIUM_AUDIT_RETENTION_INTERVAL_SECONDS", "")
	t.Setenv("PODIUM_AUDIT_RETENTION_MAX_AGE_DAYS", "")
	cfg := LoadConfig()
	if cfg.auditRetentionInterval <= 0 {
		t.Errorf("auditRetentionInterval = %d, want > 0 by default", cfg.auditRetentionInterval)
	}
	if cfg.auditRetentionMaxAgeDays != 365 {
		t.Errorf("auditRetentionMaxAgeDays = %d, want 365", cfg.auditRetentionMaxAgeDays)
	}
}

// Spec: §8.4/F-8.4.6 — an operator can still disable retention by
// setting the interval to 0.
func TestLoadConfig_RetentionDisableableByOperator(t *testing.T) {
	t.Setenv("PODIUM_AUDIT_RETENTION_INTERVAL_SECONDS", "0")
	if cfg := LoadConfig(); cfg.auditRetentionInterval != 0 {
		t.Errorf("auditRetentionInterval = %d, want 0 when operator opts out", cfg.auditRetentionInterval)
	}
}

// Spec: §8.6 (F-8.6.1) — gap detection must be "automated and alerted",
// so the verification scheduler defaults to a positive interval (one hour)
// out of the box and an operator can disable it with 0.
func TestLoadConfig_VerifyEnabledByDefault(t *testing.T) {
	t.Setenv("PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS", "")
	if cfg := LoadConfig(); cfg.auditVerifyInterval <= 0 {
		t.Errorf("auditVerifyInterval = %d, want > 0 by default", cfg.auditVerifyInterval)
	}
	t.Setenv("PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS", "0")
	if cfg := LoadConfig(); cfg.auditVerifyInterval != 0 {
		t.Errorf("auditVerifyInterval = %d, want 0 when operator opts out", cfg.auditVerifyInterval)
	}
}

// Spec: §8.4/F-8.4.5 — parseAuditSampleRates parses the
// "TYPE=RATE,TYPE=RATE" spec and skips malformed or out-of-range entries.
func TestParseAuditSampleRates(t *testing.T) {
	t.Parallel()
	got := parseAuditSampleRates("domain.loaded=0.1, artifacts.searched=0.5")
	if got[audit.EventDomainLoaded] != 0.1 {
		t.Errorf("domain.loaded = %v, want 0.1", got[audit.EventDomainLoaded])
	}
	if got[audit.EventArtifactsSearched] != 0.5 {
		t.Errorf("artifacts.searched = %v, want 0.5", got[audit.EventArtifactsSearched])
	}
	// Empty input and only-malformed input both yield nil.
	if parseAuditSampleRates("") != nil {
		t.Errorf("empty input should yield nil")
	}
	if parseAuditSampleRates("garbage,domain.loaded=2.0,foo=-1") != nil {
		t.Errorf("all-invalid input should yield nil (rates outside [0,1] skipped)")
	}
	// A type with no '=' is skipped but valid neighbors survive.
	mixed := parseAuditSampleRates("domain.loaded=0.2,bogus")
	if len(mixed) != 1 || mixed[audit.EventDomainLoaded] != 0.2 {
		t.Errorf("mixed parse = %v, want only domain.loaded=0.2", mixed)
	}
}

// Spec: §8.4/F-8.4.5 — the registry audit emitter consults the sampler
// at write time and drops sampled-out events before they enter the
// hash chain; unconfigured types are written unchanged.
func TestAuditEmitter_SamplerDropsBeforeChain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	// domain.loaded sampled at rate 0 (always dropped); artifact.published
	// has no rate and is always kept.
	sampler := audit.NewSampler(map[audit.EventType]float64{audit.EventDomainLoaded: 0})
	emit := auditEmitterFor(sink, audit.NewPIIScrubber(), sampler)

	emit(context.Background(), core.AuditEvent{Type: "domain.loaded", Caller: "alice", Target: "team/finance"})
	emit(context.Background(), core.AuditEvent{Type: "artifact.published", Caller: "alice", Target: "skill/x"})

	body, err := readBytes(path)
	if err != nil {
		t.Fatalf("readBytes: %v", err)
	}
	got := string(body)
	if strings.Contains(got, "domain.loaded") {
		t.Errorf("sampled-out domain.loaded was written:\n%s", got)
	}
	if !strings.Contains(got, "artifact.published") {
		t.Errorf("kept artifact.published missing:\n%s", got)
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Errorf("chain broken after sampled write: %v", err)
	}
}
