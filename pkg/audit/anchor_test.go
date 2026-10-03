package audit_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/sign"
)

// Spec: §8.6 — Anchor signs the current chain head, appends an
// audit.anchored event carrying the envelope, and returns the
// log index from the Sigstore Rekor entry (or -1 when the signer
// produced no log entry).
func TestAnchor_RecordsChainHeadAndAppendsEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	for i := 0; i < 3; i++ {
		_ = sink.Append(context.Background(), audit.Event{
			Type:   audit.EventArtifactLoaded,
			Caller: "alice",
			Target: "skill/x",
		})
	}
	signer := sign.Noop{}
	idx, err := audit.Anchor(context.Background(), sink, signer)
	if err != nil {
		t.Fatalf("Anchor: %v", err)
	}
	if idx != -1 {
		t.Errorf("LogIndex = %d, want -1 (Noop signer has no Rekor entry)", idx)
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Errorf("Verify after Anchor: %v", err)
	}
}

// Spec: §8.6 — Anchor against an empty log is a no-op so callers
// can run it on a schedule without guarding for sink emptiness.
func TestAnchor_EmptyLogIsNoOp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, _ := audit.NewFileSink(path)
	idx, err := audit.Anchor(context.Background(), sink, sign.Noop{})
	if err != nil {
		t.Fatalf("Anchor empty: %v", err)
	}
	if idx != -1 {
		t.Errorf("got %d, want -1", idx)
	}
}

// Spec: §8.6 — when the configured signer produces a Sigstore-keyless
// envelope whose tlog object carries a Rekor log index, Anchor surfaces
// that index in the audit.anchored event's context for cross-correlation
// with the transparency log.
func TestAnchor_SurfacesRekorLogIndex(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, _ := audit.NewFileSink(path)
	_ = sink.Append(context.Background(), audit.Event{
		Type:   audit.EventArtifactLoaded,
		Caller: "alice",
		Target: "skill/x",
	})
	signer := fakeIndexedSigner{logIndex: 12345}
	idx, err := audit.Anchor(context.Background(), sink, signer)
	if err != nil {
		t.Fatalf("Anchor: %v", err)
	}
	if idx != 12345 {
		t.Errorf("returned LogIndex = %d, want 12345", idx)
	}
	// Inspect the appended event to confirm log_index is recorded.
	data, err := readSinkBytes(path)
	if err != nil {
		t.Fatalf("read sink: %v", err)
	}
	if !strings.Contains(string(data), `"log_index":"12345"`) {
		t.Errorf("audit log missing log_index entry: %s", data)
	}
}

// Spec: §8.6 — Rekor log indices are zero-based, so a
// genuine first entry has index 0. Anchor must preserve a present-and-zero
// tlog.log_index as 0 rather than conflating it with the absent case (-1), so an
// auditor can locate the index-0 entry in the transparency log.
func TestAnchor_PreservesRekorLogIndexZero(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, _ := audit.NewFileSink(path)
	_ = sink.Append(context.Background(), audit.Event{
		Type:   audit.EventArtifactLoaded,
		Caller: "alice",
		Target: "skill/x",
	})
	idx, err := audit.Anchor(context.Background(), sink, fakeIndexedSigner{logIndex: 0})
	if err != nil {
		t.Fatalf("Anchor: %v", err)
	}
	if idx != 0 {
		t.Errorf("returned LogIndex = %d, want 0 (zero-based first Rekor entry)", idx)
	}
	data, err := readSinkBytes(path)
	if err != nil {
		t.Fatalf("read sink: %v", err)
	}
	if !strings.Contains(string(data), `"log_index":"0"`) {
		t.Errorf("audit log must record log_index 0, got: %s", data)
	}
}

// Spec: §8.6 — an envelope with no tlog object (a signer that made no
// transparency-log entry) still maps to -1.
func TestAnchor_AbsentRekorLogIndexIsMinusOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, _ := audit.NewFileSink(path)
	_ = sink.Append(context.Background(), audit.Event{
		Type:   audit.EventArtifactLoaded,
		Caller: "alice",
	})
	idx, err := audit.Anchor(context.Background(), sink, envelopeSigner{envelope: `{"cert":"-","signature":"-"}`})
	if err != nil {
		t.Fatalf("Anchor: %v", err)
	}
	if idx != -1 {
		t.Errorf("returned LogIndex = %d, want -1 (no tlog object)", idx)
	}
}

// Spec: §8.6 — the log index is read from tlog.log_index only, so an
// envelope in the format that predates the tlog object, with a top-level
// log_index, records -1.
func TestAnchor_TopLevelLogIndexIsMinusOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, _ := audit.NewFileSink(path)
	_ = sink.Append(context.Background(), audit.Event{
		Type:   audit.EventArtifactLoaded,
		Caller: "alice",
	})
	signer := envelopeSigner{envelope: `{"cert":"-","signature":"-","log_index":12345}`}
	idx, err := audit.Anchor(context.Background(), sink, signer)
	if err != nil {
		t.Fatalf("Anchor: %v", err)
	}
	if idx != -1 {
		t.Errorf("returned LogIndex = %d, want -1 (top-level log_index is not read)", idx)
	}
	data, err := readSinkBytes(path)
	if err != nil {
		t.Fatalf("read sink: %v", err)
	}
	if !strings.Contains(string(data), `"log_index":"-1"`) {
		t.Errorf("audit log must record log_index -1, got: %s", data)
	}
}

// envelopeSigner returns a fixed envelope from Sign.
type envelopeSigner struct{ envelope string }

func (envelopeSigner) ID() string                                         { return "fixed-envelope" }
func (s envelopeSigner) Sign(_ context.Context, _ string) (string, error) { return s.envelope, nil }
func (envelopeSigner) Verify(_ context.Context, _, _ string) error        { return nil }

// fakeIndexedSigner produces a Sigstore-keyless envelope in the tlog
// format so the extractRekorLogIndex code path is exercised without a
// live stack.
type fakeIndexedSigner struct{ logIndex int64 }

func (fakeIndexedSigner) ID() string { return "fake-indexed" }
func (s fakeIndexedSigner) Sign(_ context.Context, _ string) (string, error) {
	return fmt.Sprintf(`{"cert":"-","signature":"-","tlog":{"log_index":%d,"body":"-","hashes":[],"checkpoint":"-"},"timestamp":"-"}`, s.logIndex), nil
}
func (s fakeIndexedSigner) Verify(_ context.Context, _, _ string) error { return nil }
