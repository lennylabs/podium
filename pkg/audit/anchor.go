package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lennylabs/podium/pkg/sign"
)

// Anchor records a §8.6 local chain-head anchor. It signs
// "sha256:<chain head>" with signer and appends the envelope in an
// audit.anchored event, so an auditor holding the signer's public key
// can check that the key attested that head. The registry passes its
// dedicated Ed25519 anchor key, and Anchor submits nothing to a
// transparency log. The signature covers the digest alone; the event's
// timestamp and anchored_at are hash-chained with the event but are not
// covered by the anchor signature.
//
// The chain head is the Hash of the most recent event. An empty
// log produces an anchor with chainHead="" (recorded as a no-op so
// callers can run Anchor unconditionally on a schedule).
//
// Anchor returns the log index the envelope's tlog.log_index records
// (or -1 when the signer produced no transparency-log entry). The index is also recorded in
// the audit.anchored event's Context so the chain itself can be
// queried for past anchors.
//
// Anchor reads the head from the stream's file destination under the
// file's lock, releases it, signs with no lock held, and appends the
// audit.anchored event through the stream, so a slow signer never blocks
// Append and the event reaches every destination of the stream.
//
// Spec: §8.6
func Anchor(ctx context.Context, s *Stream, signer sign.Provider) (int64, error) {
	file := s.File()
	if file == nil {
		return 0, fmt.Errorf("audit.anchor: %w", errNoFileDestination)
	}
	chainHead, err := chainHeadOf(file)
	if err != nil {
		return 0, fmt.Errorf("audit.anchor: read chain head: %w", err)
	}
	if chainHead == "" {
		// Nothing to anchor; callers running on a schedule should
		// see this as a no-op rather than an error.
		return -1, nil
	}
	contentHash := "sha256:" + chainHead
	envelope, err := signer.Sign(ctx, contentHash)
	if err != nil {
		return 0, fmt.Errorf("audit.anchor: sign chain head: %w", err)
	}
	logIndex := extractRekorLogIndex(envelope)
	now := time.Now().UTC()
	// Spec: §8.1: audit.anchored describes the registry as a whole, so it records no tenant.
	if err := s.Append(ctx, Event{
		Type:      EventAuditAnchored,
		Timestamp: now,
		Caller:    "system:anchor",
		Target:    chainHead,
		Context: map[string]string{
			"signer":      signer.ID(),
			"envelope":    envelope,
			"log_index":   fmt.Sprintf("%d", logIndex),
			"anchored_at": now.Format(time.RFC3339Nano),
		},
	}); err != nil {
		return logIndex, fmt.Errorf("audit.anchor: append: %w", err)
	}
	return logIndex, nil
}

// chainHeadOf returns the Hash of the most recent event in file, or
// "" when the log is empty. It reads under file.mu and does not mutate
// the chain.
func chainHeadOf(file *FileSink) (string, error) {
	file.mu.Lock()
	defer file.mu.Unlock()
	events, err := readAllEvents(file.path)
	if err != nil {
		return "", err
	}
	return headOf(events), nil
}

// extractRekorLogIndex pulls the Rekor log index out of a
// Sigstore-keyless envelope's tlog.log_index. Returns -1 when the
// envelope carries no tlog object or no log_index in it (e.g.,
// RegistryManagedKey or the Noop signer), or does not decode.
// The registry's anchor key is a RegistryManagedKey, so registry anchors
// record -1.
//
// Rekor indices are zero-based, so the first entry recorded against
// an instance has index 0. The decode uses a nested pointer so a
// field that is present and zero is preserved as 0 and only true
// absence (or a decode failure) maps to -1.
//
// Spec: §8.6.
func extractRekorLogIndex(envelope string) int64 {
	var p struct {
		TLog *struct {
			LogIndex *int64 `json:"log_index"`
		} `json:"tlog"`
	}
	if err := json.Unmarshal([]byte(envelope), &p); err != nil || p.TLog == nil || p.TLog.LogIndex == nil {
		return -1
	}
	return *p.TLog.LogIndex
}
