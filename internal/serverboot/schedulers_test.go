package serverboot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/metrics"
)

// Spec: §8.6 — startAnchorScheduler runs the periodic anchor with the signer
// loadAnchorSigner loaded, and its goroutine exits when the context ends.
func TestStartAnchorScheduler_RunsWithLoadedSigner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, err := audit.OpenFile(filepath.Join(dir, "audit.log"), audit.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	signer, _, err := loadOrGenerateAuditSigner(filepath.Join(dir, "audit.key"))
	if err != nil {
		t.Fatalf("loadOrGenerateAuditSigner: %v", err)
	}
	cfg := &Config{auditAnchorInterval: 1}
	startAnchorScheduler(t.Context(), cfg, sink, signer)
	// Give the scheduler a beat to run its immediate pass; the test context's
	// cancellation then stops the goroutine.
	time.Sleep(20 * time.Millisecond)
}

// Spec: §8.4 — a nil stream reaches the scheduler's file check through the
// nil-receiver-safe File, so retention is disabled without a panic.
func TestStartRetentionScheduler_NilSink(t *testing.T) {
	t.Parallel()
	startRetentionScheduler(t.Context(), &Config{}, nil, nil)
}

// Spec: §8.6 — startVerifyScheduler tolerates a nil stream, through the
// nil-receiver-safe File, by logging and returning without launching a
// goroutine.
func TestStartVerifyScheduler_NilSink(t *testing.T) {
	t.Parallel()
	startVerifyScheduler(t.Context(), &Config{auditVerifyInterval: 1}, nil)
}

// Spec: §8.6 — with a sink and a short interval the verify
// scheduler runs its immediate pass without panicking. A clean chain
// raises no alert.
func TestStartVerifyScheduler_Runs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, _ := audit.OpenFile(filepath.Join(dir, "audit.log"), audit.OpenOptions{})
	_ = sink.Append(context.Background(), audit.Event{Type: audit.EventArtifactLoaded, Caller: "alice"})
	startVerifyScheduler(t.Context(), &Config{auditVerifyInterval: 1}, sink)
	// Allow the immediate pass to run, then let process exit reclaim the
	// goroutine.
	time.Sleep(40 * time.Millisecond)
}

func TestStartRetentionScheduler_ZeroMaxAge(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, _ := audit.OpenFile(filepath.Join(dir, "audit.log"), audit.OpenOptions{})
	startRetentionScheduler(t.Context(), &Config{auditRetentionMaxAgeDays: 0}, sink, nil)
}

func TestStartRetentionScheduler_ZeroInterval(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, _ := audit.OpenFile(filepath.Join(dir, "audit.log"), audit.OpenOptions{})
	cfg := &Config{auditRetentionMaxAgeDays: 1, auditRetentionInterval: 0}
	startRetentionScheduler(t.Context(), cfg, sink, nil)
}

func TestStartRetentionScheduler_Runs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, _ := audit.OpenFile(filepath.Join(dir, "audit.log"), audit.OpenOptions{})
	cfg := &Config{auditRetentionMaxAgeDays: 1, auditRetentionInterval: 1}
	startRetentionScheduler(t.Context(), cfg, sink, nil)
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

// Spec: §8.4 — every audit.EventType is classified as either
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

// Spec: §8.4 — the retained-indefinitely integrity and erasure
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

// openStreamForTest opens the registry stream for cfg and shuts it down when
// the test ends.
func openStreamForTest(t *testing.T, cfg *Config) *audit.Stream {
	t.Helper()
	stream, err := openAuditStream(cfg, nil)
	if err != nil {
		t.Fatalf("openAuditStream: %v", err)
	}
	t.Cleanup(stream.Shutdown)
	return stream
}

// Spec: §8.3 — a filesystem PODIUM_AUDIT_LOG_PATH opens a stream whose
// file destination writes that path.
func TestOpenAuditStream_WithExplicitPath(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "x.log")
	stream := openStreamForTest(t, &Config{auditLogPath: path})
	if stream.File() == nil || stream.File().Path() != path {
		t.Errorf("file destination = %v, want %s", stream.File(), path)
	}
}

// Spec: §8.3 — an http(s) PODIUM_AUDIT_LOG_PATH opens a stream with no file
// destination, so the file-only schedulers (anchor, verify, and retention)
// and the §8.5 erasure rewrite stay disabled.
func TestOpenAuditStream_EndpointRedirect(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"http://siem.example/audit", "https://siem.example/audit"} {
		stream := openStreamForTest(t, &Config{auditLogPath: raw})
		if stream == nil {
			t.Fatalf("%s: stream is nil", raw)
		}
		if stream.File() != nil {
			t.Errorf("%s: file destination is non-nil for an HTTP destination", raw)
		}
	}
}

// Spec: §8.3, §13.12 — a URL element that does not parse refuses rather than
// opening a file named like a URL. run refuses the error whatever the
// anchoring setting.
func TestOpenAuditStream_UnparseableURLRefuses(t *testing.T) {
	t.Parallel()
	stream, err := openAuditStream(&Config{auditLogPath: "http://%zz"}, nil)
	if stream != nil || !errors.Is(err, audit.ErrConfig) {
		t.Errorf("openAuditStream = (%v, %v), want (nil, an audit.ErrConfig error)", stream, err)
	}
}

// Spec: §8.6, §13.12 — a file destination that NewFileSink cannot open
// returns an audit.ErrConfig error naming PODIUM_AUDIT_LOG_PATH and the path,
// with a nil stream. run refuses the error whatever the anchoring setting,
// so no registry runs with no audit stream.
func TestOpenAuditStream_UnopenableFileRefuses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"existing directory":  dir,
		"regular-file parent": filepath.Join(blocker, "audit.log"),
	} {
		stream, err := openAuditStream(&Config{auditLogPath: path}, nil)
		if stream != nil {
			t.Errorf("%s: stream = %v, want nil", name, stream)
		}
		if !errors.Is(err, audit.ErrConfig) || !strings.Contains(err.Error(), "PODIUM_AUDIT_LOG_PATH") || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: err = %v, want an audit.ErrConfig error naming PODIUM_AUDIT_LOG_PATH and %s", name, err, path)
		}
	}
}

// Spec: §8.6, §13.12 — with PODIUM_AUDIT_LOG_PATH unset and no resolvable
// home directory, the default ~/.podium/audit.log cannot be resolved, and
// openAuditStream returns an audit.ErrConfig error with a nil stream. run
// refuses the error whatever the anchoring setting.
func TestOpenAuditStream_UnresolvableHomeRefuses(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	stream, err := openAuditStream(&Config{}, nil)
	if stream != nil {
		t.Errorf("stream = %v, want nil", stream)
	}
	if !errors.Is(err, audit.ErrConfig) || !strings.Contains(err.Error(), "PODIUM_AUDIT_LOG_PATH") || !strings.Contains(err.Error(), "~/.podium/audit.log") {
		t.Errorf("err = %v, want an audit.ErrConfig error naming PODIUM_AUDIT_LOG_PATH and ~/.podium/audit.log", err)
	}
}

// Spec: §13.12 — an unusable PODIUM_AUDIT_QUEUE_MAX_BYTES refuses with an
// audit.ErrConfig error naming the variable.
func TestOpenAuditStream_UnusableQueueBoundRefuses(t *testing.T) {
	t.Setenv("PODIUM_AUDIT_QUEUE_MAX_BYTES", "0")
	stream, err := openAuditStream(&Config{auditLogPath: filepath.Join(t.TempDir(), "a.log")}, nil)
	if stream != nil || !errors.Is(err, audit.ErrConfig) || !strings.Contains(err.Error(), "PODIUM_AUDIT_QUEUE_MAX_BYTES") {
		t.Errorf("openAuditStream = (%v, %v), want (nil, an audit.ErrConfig error naming PODIUM_AUDIT_QUEUE_MAX_BYTES)", stream, err)
	}
}

// Spec: §8.3, §13.8 — with metrics off the stream's observer is a nil
// interface, never a typed-nil registry that would panic on the first count.
func TestAuditObserver_NilRegistryIsNilInterface(t *testing.T) {
	t.Parallel()
	if obs := auditObserver(nil); obs != nil {
		t.Errorf("auditObserver(nil) = %#v, want a nil interface", obs)
	}
	if obs := auditObserver(metrics.New()); obs == nil {
		t.Error("auditObserver(registry) = nil, want the registry")
	}
}
