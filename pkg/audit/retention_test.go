package audit_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
)

// Spec: §8.4 — Enforce drops events older than the per-type policy
// and rewrites the hash chain over the survivors. Verify still
// passes after rewrite.
func TestEnforce_DropsExpiredEventsAndRebuildsChain(t *testing.T) {
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
		[]audit.Policy{{Type: audit.EventArtifactsSearched, MaxAge: 30 * 24 * time.Hour}}, nil)
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

// Spec: §8.4/§8.6/F-8.4.8 — when a retention pass rewrites the chain it
// appends an audit.retention_enforced marker recording the superseded
// chain head, so a verifier holding an older anchor can reconcile it.
func TestEnforce_AppendsRetentionMarkerRecordingSupersededHead(t *testing.T) {
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
	for _, ts := range []time.Time{old, recent} {
		_ = sink.Append(context.Background(), audit.Event{
			Type: audit.EventArtifactLoaded, Timestamp: ts, Caller: "u",
		})
	}
	// Capture the chain head before retention; the marker must name it.
	headBefore := chainHeadFromFile(t, path)

	dropped, err := audit.Enforce(context.Background(), sink, now,
		[]audit.Policy{{Type: audit.EventArtifactLoaded, MaxAge: 30 * 24 * time.Hour}}, nil)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Errorf("Verify after Enforce: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sink: %v", err)
	}
	if !containsBytes(data, []byte("audit.retention_enforced")) {
		t.Errorf("retention marker not appended")
	}
	if !containsBytes(data, []byte(headBefore)) {
		t.Errorf("marker does not record superseded head %q", headBefore)
	}
}

// Spec: §8.4/F-8.4.8 — a pass that drops nothing leaves the log
// untouched and appends no marker, so a stable chain is not perturbed.
func TestEnforce_NoMarkerWhenNothingChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, _ := audit.NewFileSink(path)
	now := time.Now().UTC()
	_ = sink.Append(context.Background(), audit.Event{
		Type: audit.EventArtifactLoaded, Timestamp: now.Add(-time.Hour), Caller: "u",
	})
	dropped, err := audit.Enforce(context.Background(), sink, now,
		[]audit.Policy{{Type: audit.EventArtifactLoaded, MaxAge: 365 * 24 * time.Hour}}, nil)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	data, _ := os.ReadFile(path)
	if containsBytes(data, []byte("audit.retention_enforced")) {
		t.Errorf("marker appended despite no change")
	}
}

// Spec: §8.4 — most-restrictive policy wins when two cover the
// same event type.
func TestEnforce_MostRestrictivePolicyWins(t *testing.T) {
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
	}, nil)
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
func TestEraseUser_ReplacesIdentifiersAndAppendsTombstone(t *testing.T) {
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
		"joan@example.com", "tenant-salt", "carol@acme.com")
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

// parsedEvent mirrors the §8.1 JSON-Lines wire form for the fields these
// tests inspect. The caller identity is nested under "caller" per §8.1
// (caller.identity), so Caller is the unpacked identity string.
type parsedEvent struct {
	Type    string
	Caller  string
	Target  string
	Context map[string]string
}

// parseEvents parses the file-backed sink and returns its events. Used to
// inspect the appended user.erased record.
func parseEvents(t *testing.T, path string) []parsedEvent {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []parsedEvent
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var e struct {
			Type   string `json:"type"`
			Caller struct {
				Identity string `json:"identity"`
			} `json:"caller"`
			Target  string            `json:"target"`
			Context map[string]string `json:"context"`
		}
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("parse event: %v", err)
		}
		out = append(out, parsedEvent{Type: e.Type, Caller: e.Caller.Identity, Target: e.Target, Context: e.Context})
	}
	return out
}

// Spec: §8.5 (F-8.5.2) — the redaction value is
// redacted-<sha256(user_id+salt)>: the redacted- prefix, the full 32-byte
// (64 hex char) SHA-256 digest, and no delimiter inserted between user_id
// and salt.
func TestEraseUser_TombstoneFormatMatchesSpec(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, _ := audit.NewFileSink(path)
	_ = sink.Append(context.Background(), audit.Event{
		Type: audit.EventArtifactLoaded, Caller: "alice@acme.com", Timestamp: time.Now().UTC(),
	})
	if _, err := audit.EraseUser(context.Background(), sink, "alice@acme.com", "tenant-salt", "carol@acme.com"); err != nil {
		t.Fatalf("EraseUser: %v", err)
	}
	want := func() string {
		h := sha256.Sum256([]byte("alice@acme.com" + "tenant-salt"))
		return "redacted-" + hex.EncodeToString(h[:])
	}()
	data, _ := readSinkBytes(path)
	if !containsBytes(data, []byte(want)) {
		t.Errorf("tombstone %q not found in log:\n%s", want, data)
	}
	// The pre-fix format must be gone: prefix, delimiter, and truncation.
	if containsBytes(data, []byte("erased:")) {
		t.Errorf("legacy erased: prefix still present")
	}
	wrongDelim := func() string {
		h := sha256.Sum256([]byte("alice@acme.com" + "|" + "tenant-salt"))
		return hex.EncodeToString(h[:])
	}()
	if containsBytes(data, []byte(wrongDelim)) {
		t.Errorf("delimiter-bearing digest present; salt must be appended without a separator")
	}
	// 64 hex chars after the prefix = full SHA-256, not the 16-char truncation.
	if len(want) != len("redacted-")+64 {
		t.Fatalf("test bug: want length %d", len(want))
	}
}

// Spec: §8.5 (F-8.5.5) — an empty salt is rejected so the tombstone cannot
// degrade to a guessable sha256(user_id).
func TestEraseUser_EmptySaltRejected(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, _ := audit.NewFileSink(path)
	_ = sink.Append(context.Background(), audit.Event{
		Type: audit.EventArtifactLoaded, Caller: "alice@acme.com", Timestamp: time.Now().UTC(),
	})
	if _, err := audit.EraseUser(context.Background(), sink, "alice@acme.com", "", "carol@acme.com"); err == nil {
		t.Fatalf("EraseUser with empty salt: want error, got nil")
	}
	// The log must be untouched: the original identity still present, no
	// user.erased event appended.
	data, _ := readSinkBytes(path)
	if !containsBytes(data, []byte("alice@acme.com")) {
		t.Errorf("rejected erase must not rewrite the log")
	}
	if containsBytes(data, []byte("user.erased")) {
		t.Errorf("rejected erase must not append user.erased")
	}
}

// Spec: §8.5 / §8.1 (F-8.5.4) — the appended user.erased event records the
// invoking admin as the Caller and in the admin context field.
func TestEraseUser_RecordsInvokingAdmin(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, _ := audit.NewFileSink(path)
	_ = sink.Append(context.Background(), audit.Event{
		Type: audit.EventArtifactLoaded, Caller: "alice@acme.com", Timestamp: time.Now().UTC(),
	})
	if _, err := audit.EraseUser(context.Background(), sink, "alice@acme.com", "salt", "carol@acme.com"); err != nil {
		t.Fatalf("EraseUser: %v", err)
	}
	events := parseEvents(t, path)
	var found bool
	for _, e := range events {
		if e.Type != string(audit.EventUserErased) {
			continue
		}
		found = true
		if e.Caller != "carol@acme.com" {
			t.Errorf("user.erased Caller = %q, want carol@acme.com", e.Caller)
		}
		if e.Context["admin"] != "carol@acme.com" {
			t.Errorf("user.erased context admin = %q, want carol@acme.com", e.Context["admin"])
		}
	}
	if !found {
		t.Fatalf("no user.erased event appended")
	}
}

// Helpers shared with the other audit tests.

func readSinkBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// chainHeadFromFile returns the Hash of the last JSON-Lines event in
// the file, the chain head a prior anchor would have pinned.
func chainHeadFromFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var head string
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var je struct {
			Hash string `json:"hash"`
		}
		if err := json.Unmarshal(line, &je); err != nil {
			t.Fatalf("parse event: %v", err)
		}
		head = je.Hash
	}
	if head == "" {
		t.Fatalf("no chain head found in %s", path)
	}
	return head
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
