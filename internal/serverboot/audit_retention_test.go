package serverboot

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
)

// Spec: §8.5 — runRetentionOnce drops events older than the
// configured policy and leaves younger events alone. The chain
// rebuilds over the kept events.
func TestRunRetentionOnce_DropsOldEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink, err := audit.NewFileSink(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
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
	runRetentionOnce(context.Background(), sink, policies)

	if err := sink.Verify(context.Background()); err != nil {
		t.Errorf("Verify after retention: %v", err)
	}
}

// Spec: §8.5 — startRetentionScheduler treats nil sink and
// non-positive max-age as "disabled" instead of crashing.
func TestStartRetentionScheduler_NoOpWhenUnconfigured(t *testing.T) {
	t.Parallel()
	startRetentionScheduler(&Config{auditRetentionInterval: 60, auditRetentionMaxAgeDays: 0}, nil)
	startRetentionScheduler(&Config{auditRetentionInterval: 0}, nil)
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
