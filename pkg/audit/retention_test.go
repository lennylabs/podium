package audit_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/audit"
)

// Spec: §8.4 — Enforce drops events older than the per-type policy
// and rewrites the hash chain over the survivors. Verify still
// passes after rewrite.
// Phase: 16
func TestEnforce_DropsExpiredEventsAndRebuildsChain(t *testing.T) {
	testharness.RequirePhase(t, 16)
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	now := time.Now().UTC()
	old := now.Add(-2 * 365 * 24 * time.Hour)
	recent := now.Add(-1 * time.Hour)
	for i, ts := range []time.Time{old, recent, recent} {
		_ = sink.Append(context.Background(), audit.Event{
			Type:      audit.EventArtifactsSearched,
			Timestamp: ts,
			Caller:    "u",
			Target:    "q",
			Context:   map[string]string{"i": string(rune('a' + i))},
		})
	}
	dropped, err := audit.Enforce(context.Background(), sink, now,
		[]audit.Policy{{Type: audit.EventArtifactsSearched, MaxAge: 30 * 24 * time.Hour}})
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Errorf("Verify after Enforce: %v", err)
	}
}

// Spec: §8.4 — most-restrictive policy wins when two cover the
// same event type.
// Phase: 16
func TestEnforce_MostRestrictivePolicyWins(t *testing.T) {
	testharness.RequirePhase(t, 16)
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, _ := audit.NewFileSink(path)
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		_ = sink.Append(context.Background(), audit.Event{
			Type:      audit.EventDomainLoaded,
			Timestamp: now.Add(time.Duration(-i-1) * 24 * time.Hour),
			Caller:    "u",
		})
	}
	dropped, err := audit.Enforce(context.Background(), sink, now, []audit.Policy{
		{Type: audit.EventDomainLoaded, MaxAge: 7 * 24 * time.Hour},
		{Type: audit.EventDomainLoaded, MaxAge: 36 * time.Hour},
	})
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	// 36h policy wins; events at -1d (24h, kept), -2d (48h, dropped),
	// -3d (72h, dropped).
	if dropped != 2 {
		t.Errorf("dropped = %d, want 2", dropped)
	}
}

// Spec: §8.5 — EraseUser replaces every Caller and userID-bearing
// Context value with a salted tombstone, appends a user.erased
// event, and rebuilds the chain.
// Phase: 16
func TestEraseUser_ReplacesIdentifiersAndAppendsTombstone(t *testing.T) {
	testharness.RequirePhase(t, 16)
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, _ := audit.NewFileSink(path)
	for i := 0; i < 3; i++ {
		_ = sink.Append(context.Background(), audit.Event{
			Type:      audit.EventArtifactLoaded,
			Caller:    "joan@example.com",
			Target:    "skill/x",
			Timestamp: time.Now().UTC(),
		})
	}
	// One unrelated event.
	_ = sink.Append(context.Background(), audit.Event{
		Type:      audit.EventArtifactLoaded,
		Caller:    "other@example.com",
		Target:    "skill/x",
		Timestamp: time.Now().UTC(),
	})
	transformed, err := audit.EraseUser(context.Background(), sink,
		"joan@example.com", "tenant-salt")
	if err != nil {
		t.Fatalf("EraseUser: %v", err)
	}
	if transformed != 3 {
		t.Errorf("transformed = %d, want 3", transformed)
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Errorf("Verify after EraseUser: %v", err)
	}
	// The tombstone, user.erased event, and unrelated caller all
	// remain. The original userID must be absent.
	data, err := readSinkBytes(path)
	if err != nil {
		t.Fatalf("read sink: %v", err)
	}
	if containsBytes(data, []byte("joan@example.com")) {
		t.Errorf("original userID still present in audit log")
	}
	if !containsBytes(data, []byte("user.erased")) {
		t.Errorf("user.erased event not appended")
	}
	if !containsBytes(data, []byte("other@example.com")) {
		t.Errorf("unrelated caller was incorrectly redacted")
	}
}

// Helpers shared with the other audit tests.

func readSinkBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func containsBytes(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
