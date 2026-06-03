package serverboot

import (
	"context"
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
	startRetentionScheduler(&Config{}, nil, nil)
}

// Spec: §8.6 (F-8.6.1) — startVerifyScheduler tolerates a nil sink by
// logging and returning without launching a goroutine.
func TestStartVerifyScheduler_NilSink(t *testing.T) {
	t.Parallel()
	startVerifyScheduler(&Config{auditVerifyInterval: 1}, nil)
}

// Spec: §8.6 (F-8.6.1) — with a sink and a short interval the verify
// scheduler runs its immediate pass without panicking. A clean chain
// raises no alert.
func TestStartVerifyScheduler_Runs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, _ := audit.NewFileSink(filepath.Join(dir, "audit.log"))
	_ = sink.Append(context.Background(), audit.Event{Type: audit.EventArtifactLoaded, Caller: "alice"})
	startVerifyScheduler(&Config{auditVerifyInterval: 1}, sink)
	// Allow the immediate pass to run, then let process exit reclaim the
	// goroutine.
	time.Sleep(40 * time.Millisecond)
}

func TestStartRetentionScheduler_ZeroMaxAge(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, _ := audit.NewFileSink(filepath.Join(dir, "audit.log"))
	startRetentionScheduler(&Config{auditRetentionMaxAgeDays: 0}, sink, nil)
}

func TestStartRetentionScheduler_ZeroInterval(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, _ := audit.NewFileSink(filepath.Join(dir, "audit.log"))
	cfg := &Config{auditRetentionMaxAgeDays: 1, auditRetentionInterval: 0}
	startRetentionScheduler(cfg, sink, nil)
}

func TestStartRetentionScheduler_Runs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, _ := audit.NewFileSink(filepath.Join(dir, "audit.log"))
	cfg := &Config{auditRetentionMaxAgeDays: 1, auditRetentionInterval: 1}
	startRetentionScheduler(cfg, sink, nil)
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

// Spec: §8.4 (F-8.4.3) — every audit.EventType is classified as either
// operational (subject to the 1-year metadata default) or retained
// indefinitely (integrity / GDPR-erasure accountability), with no type in
// both and none left unclassified. This guards against a newly added event
// type being silently omitted from the default retention policy.
func TestRetentionClassification_CoversEveryEventType(t *testing.T) {
	t.Parallel()
	operational := map[audit.EventType]bool{}
	for _, ty := range operationalEventTypes {
		if operational[ty] {
			t.Errorf("duplicate operational type %q", ty)
		}
		operational[ty] = true
	}
	retained := map[audit.EventType]bool{}
	for _, ty := range retainedIndefinitelyEventTypes {
		if retained[ty] {
			t.Errorf("duplicate retained-indefinitely type %q", ty)
		}
		if operational[ty] {
			t.Errorf("type %q is both operational and retained-indefinitely", ty)
		}
		retained[ty] = true
	}
	for _, ty := range audit.AllEventTypes() {
		if !operational[ty] && !retained[ty] {
			t.Errorf("event type %q is unclassified: add it to operationalEventTypes "+
				"(1-year metadata default) or retainedIndefinitelyEventTypes", ty)
		}
	}
	// No extra types beyond the package's full set.
	all := map[audit.EventType]bool{}
	for _, ty := range audit.AllEventTypes() {
		all[ty] = true
	}
	for ty := range operational {
		if !all[ty] {
			t.Errorf("operational type %q is not in audit.AllEventTypes()", ty)
		}
	}
	for ty := range retained {
		if !all[ty] {
			t.Errorf("retained type %q is not in audit.AllEventTypes()", ty)
		}
	}
}

// Spec: §8.4 (F-8.4.3) — the retained-indefinitely integrity and erasure
// events carry no default policy, so audit.Enforce never drops them even
// when they are older than the metadata window.
func TestRetentionPolicy_OmitsIntegrityAndErasureTypes(t *testing.T) {
	t.Parallel()
	policies := defaultRetentionPolicies(365 * 24 * time.Hour)
	covered := map[audit.EventType]bool{}
	for _, p := range policies {
		covered[p.Type] = true
	}
	for _, ty := range retainedIndefinitelyEventTypes {
		if covered[ty] {
			t.Errorf("integrity/erasure type %q must not have a default retention policy", ty)
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
	sink, file := openAuditSink(cfg)
	if sink == nil {
		t.Errorf("openAuditSink returned nil emit sink for writable path")
	}
	if file == nil {
		t.Errorf("openAuditSink returned nil file sink for a filesystem path")
	}
}

// Spec: §8.3 (F-8.3.1) — an http(s) PODIUM_AUDIT_LOG_PATH redirects the
// registry sink to an external endpoint, mirroring the local sink. The emit
// sink is the EndpointSink; the file form is nil so the file-only schedulers
// (anchor/verify/retention) and the §8.5 erasure pass stay disabled.
func TestOpenAuditSink_EndpointRedirect(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"http://siem.example/audit", "https://siem.example/audit"} {
		cfg := &Config{auditLogPath: raw}
		sink, file := openAuditSink(cfg)
		if sink == nil {
			t.Errorf("%s: emit sink is nil, want EndpointSink", raw)
		}
		if _, ok := sink.(*audit.EndpointSink); !ok {
			t.Errorf("%s: emit sink is %T, want *audit.EndpointSink", raw, sink)
		}
		if file != nil {
			t.Errorf("%s: file sink is non-nil for an endpoint redirect", raw)
		}
	}
}

// Spec: §8.3 (F-8.3.1) — a malformed endpoint value disables the sink
// (both returns nil) rather than falling back to a file named like a URL.
func TestOpenAuditSink_BadEndpointDisables(t *testing.T) {
	t.Parallel()
	cfg := &Config{auditLogPath: "http://%zz"}
	sink, file := openAuditSink(cfg)
	if sink != nil || file != nil {
		t.Errorf("bad endpoint: got (%v, %v), want (nil, nil)", sink, file)
	}
}
