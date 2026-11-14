package serverboot

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
)

// appendAgedSearch appends an artifacts.searched event whose query text is
// 8 days old, past the §8.4 placeholder mark and short of the drop mark, so
// a retention pass redacts it and drops nothing.
func appendAgedSearch(t *testing.T, stream *audit.Stream) {
	t.Helper()
	if err := stream.Append(context.Background(), audit.Event{
		Type:      audit.EventArtifactsSearched,
		Timestamp: time.Now().UTC().Add(-8 * 24 * time.Hour),
		Caller:    "bob",
		Context:   map[string]string{"query": "apollo"},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// epipeWriter fails every write as a vanished reader of standard output
// does.
type epipeWriter struct{}

func (epipeWriter) Write([]byte) (int, error) { return 0, syscall.EPIPE }

// Spec: §8.6 — a pass that only redacts aged query text still rewrites the
// chain, so it re-anchors the new head once. A second pass changes nothing,
// leaves the file's bytes and inode alone, and does not re-anchor.
func TestRunRetentionOnce_ReAnchorsAfterRedactionOnlyPass(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	stream, err := audit.OpenFile(path, audit.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	appendAgedSearch(t, stream)
	policies := defaultRetentionPolicies(365 * 24 * time.Hour)
	anchored := 0
	reAnchor := func(context.Context) { anchored++ }

	runRetentionOnce(context.Background(), stream, policies, reAnchor)
	if anchored != 1 {
		t.Fatalf("reAnchor invoked %d times after a redaction-only pass, want 1", anchored)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	runRetentionOnce(context.Background(), stream, policies, reAnchor)
	if anchored != 1 {
		t.Errorf("reAnchor invoked %d times after a pass that changed nothing, want 1 in total", anchored)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a pass that changed nothing rewrote the file:\nbefore %s\nafter  %s", before, after)
	}
	infoAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(infoBefore, infoAfter) {
		t.Error("a pass that changed nothing replaced the file's inode")
	}
	if err := stream.Verify(context.Background()); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// Spec: §8.6 — a stdout write that fails on the closing event is counted
// and never returned, because the file already holds the rewrite, so the
// pass re-anchors and logs no retention failure.
func TestRunRetentionOnce_ReAnchorsDespiteStdoutFailure(t *testing.T) {
	logs := captureLog(t)
	path := filepath.Join(t.TempDir(), "audit.log")
	stream, err := audit.Open(context.Background(), path+",stdout", audit.OpenOptions{Stdout: epipeWriter{}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(stream.Shutdown)
	appendAgedSearch(t, stream)
	anchored := 0

	runRetentionOnce(context.Background(), stream,
		defaultRetentionPolicies(365*24*time.Hour), func(context.Context) { anchored++ })
	if anchored != 1 {
		t.Errorf("reAnchor invoked %d times, want 1", anchored)
	}
	out := logs.String()
	if strings.Contains(out, "audit retention failure") {
		t.Errorf("the pass logged a retention failure for a stdout write error:\n%s", out)
	}
	if !strings.Contains(out, "redacted 1 query text(s)") {
		t.Errorf("the pass did not log its redaction:\n%s", out)
	}
	if err := stream.Verify(context.Background()); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// Spec: §8.5 — runRetentionOnce drops events older than the
// configured policy and leaves younger events alone. The chain
// rebuilds over the kept events.
func TestRunRetentionOnce_DropsOldEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, err := audit.OpenFile(filepath.Join(dir, "audit.log"), audit.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	now := time.Now().UTC()
	old := now.Add(-400 * 24 * time.Hour)
	young := now.Add(-30 * 24 * time.Hour)
	for _, ts := range []time.Time{old, old, young} {
		if err := sink.Append(context.Background(), audit.Event{
			Type: audit.EventArtifactLoaded, Timestamp: ts,
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	policies := defaultRetentionPolicies(365 * 24 * time.Hour)
	anchored := 0
	runRetentionOnce(context.Background(), sink, policies, func(context.Context) { anchored++ })

	if err := sink.Verify(context.Background()); err != nil {
		t.Errorf("Verify after retention: %v", err)
	}
	// spec: §8.6 — dropping events must trigger a re-anchor.
	if anchored != 1 {
		t.Errorf("reAnchor invoked %d times, want 1 after a drop", anchored)
	}
}

// Spec: §8.6 — a retention pass that drops nothing must not
// re-anchor (the chain head is unchanged, so the prior anchor still
// holds).
func TestRunRetentionOnce_NoReAnchorWhenNothingDropped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, err := audit.OpenFile(filepath.Join(dir, "audit.log"), audit.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if err := sink.Append(context.Background(), audit.Event{
		Type: audit.EventArtifactLoaded, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	anchored := 0
	runRetentionOnce(context.Background(), sink,
		defaultRetentionPolicies(365*24*time.Hour), func(context.Context) { anchored++ })
	if anchored != 0 {
		t.Errorf("reAnchor invoked %d times, want 0 when nothing dropped", anchored)
	}
}

// Spec: §8.5 — startRetentionScheduler treats nil sink and
// non-positive max-age as "disabled" instead of crashing.
func TestStartRetentionScheduler_NoOpWhenUnconfigured(t *testing.T) {
	t.Parallel()
	startRetentionScheduler(t.Context(), &Config{auditRetentionInterval: 60, auditRetentionMaxAgeDays: 0}, nil, nil)
	startRetentionScheduler(t.Context(), &Config{auditRetentionInterval: 0}, nil, nil)
	// Reaching here with no panic is the assertion.
}

// Spec: §8.5 — defaultRetentionPolicies covers every event type
// the registry emits today (signal: §8.1's full enumeration).
func TestDefaultRetentionPolicies_CoversCommonEventTypes(t *testing.T) {
	t.Parallel()
	policies := defaultRetentionPolicies(time.Hour)
	covered := map[audit.EventType]bool{}
	for _, p := range policies {
		covered[p.Type] = true
	}
	for _, want := range []audit.EventType{
		audit.EventArtifactLoaded,
		audit.EventArtifactPublished,
		audit.EventReadOnlyEntered,
		audit.EventReadOnlyExited,
		audit.EventLayerIngested,
		audit.EventAdminGranted,
	} {
		if !covered[want] {
			t.Errorf("policy missing for %s", want)
		}
	}
}
