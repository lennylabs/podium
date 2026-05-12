package serverboot

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
)

func TestStartAnchorScheduler_NilSinkLogsAndReturns(t *testing.T) {
	t.Parallel()
	// nil sink path is the "audit anchor disabled" branch; reaching
	// it without panicking is sufficient.
	startAnchorScheduler(&Config{}, nil)
}

func TestStartAnchorScheduler_BadKeyPathLogsAndReturns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, err := audit.NewFileSink(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	// loadOrGenerateAuditSigner: bad path → generated key. Use a
	// real path inside the temp dir to exercise the success branch.
	cfg := &Config{
		auditSigningKeyPath: filepath.Join(dir, "audit.key"),
		auditAnchorInterval: 1,
	}
	startAnchorScheduler(cfg, sink)
	// Give the scheduler a beat to run, then let test cleanup
	// reclaim the goroutine via process exit.
	time.Sleep(20 * time.Millisecond)
}

func TestStartRetentionScheduler_NilSink(t *testing.T) {
	t.Parallel()
	startRetentionScheduler(&Config{}, nil)
}

func TestStartRetentionScheduler_ZeroMaxAge(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, _ := audit.NewFileSink(filepath.Join(dir, "audit.log"))
	startRetentionScheduler(&Config{auditRetentionMaxAgeDays: 0}, sink)
}

func TestStartRetentionScheduler_ZeroInterval(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, _ := audit.NewFileSink(filepath.Join(dir, "audit.log"))
	cfg := &Config{auditRetentionMaxAgeDays: 1, auditRetentionInterval: 0}
	startRetentionScheduler(cfg, sink)
}

func TestStartRetentionScheduler_Runs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, _ := audit.NewFileSink(filepath.Join(dir, "audit.log"))
	cfg := &Config{auditRetentionMaxAgeDays: 1, auditRetentionInterval: 1}
	startRetentionScheduler(cfg, sink)
	// Allow the immediate pass to run.
	time.Sleep(50 * time.Millisecond)
}

func TestDefaultRetentionPolicies_NonEmpty(t *testing.T) {
	t.Parallel()
	policies := defaultRetentionPolicies(24 * time.Hour)
	if len(policies) == 0 {
		t.Fatal("expected non-empty policy slice")
	}
	// All policies use the same MaxAge.
	for _, p := range policies {
		if p.MaxAge != 24*time.Hour {
			t.Errorf("policy %q MaxAge = %v", p.Type, p.MaxAge)
		}
	}
}

func TestKeyIDFor_StableShortHex(t *testing.T) {
	t.Parallel()
	id1 := keyIDFor([]byte{1, 2, 3})
	id2 := keyIDFor([]byte{1, 2, 3})
	if id1 != id2 {
		t.Errorf("keyIDFor not deterministic: %q vs %q", id1, id2)
	}
	if id3 := keyIDFor([]byte{9, 9, 9}); id3 == id1 {
		t.Errorf("expected different ids; got %q twice", id1)
	}
	if len(id1) != 16 {
		t.Errorf("len = %d, want 16 (hex of 8 bytes)", len(id1))
	}
}

func TestResolveAuditPath_ExpandsHome(t *testing.T) {
	t.Parallel()
	if got, err := resolveAuditPath("/explicit"); err != nil || got != "/explicit" {
		t.Errorf("explicit: got %q err %v", got, err)
	}
	got, err := resolveAuditPath("")
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if got == "" {
		t.Errorf("empty path returned for default")
	}
}

func TestOpenAuditSink_WithExplicitPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := &Config{auditLogPath: filepath.Join(dir, "x.log")}
	if sink := openAuditSink(cfg); sink == nil {
		t.Errorf("openAuditSink returned nil for writable path")
	}
}
