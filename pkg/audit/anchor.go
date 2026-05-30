package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lennylabs/podium/pkg/sign"
)

// Anchor records the §8.6 transparency-anchor for the current
// audit chain head. The chain head is signed via the supplied
// Sigstore-keyless provider; the resulting envelope is stored in an
// audit.anchored event so independent auditors can walk back to
// any prior chain head and confirm via Rekor that the anchor was
// recorded at that time.
//
// The chain head is the Hash of the most recent event. An empty
// log produces an anchor with chainHead="" (recorded as a no-op so
// callers can run Anchor unconditionally on a schedule).
//
// Anchor returns the log index Sigstore assigned (or -1 if the
// provider had no Rekor configured). The index is also recorded in
// the audit.anchored event's Context so the chain itself can be
// queried for past anchors.
func Anchor(ctx context.Context, sink *FileSink, signer sign.Provider) (int64, error) {
	chainHead, err := chainHeadOf(sink)
	if err != nil {
		return 0, fmt.Errorf("audit.anchor: read chain head: %w", err)
	}
	if chainHead == "" {
		// Nothing to anchor; callers running on a schedule should
		// see this as a no-op rather than an error.
		return -1, nil
	}
	contentHash := "sha256:" + chainHead
	envelope, err := signer.Sign(contentHash)
	if err != nil {
		return 0, fmt.Errorf("audit.anchor: sign chain head: %w", err)
	}
	logIndex := extractRekorLogIndex(envelope)
	now := time.Now().UTC()
	if err := sink.Append(ctx, Event{
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

// chainHeadOf returns the Hash of the most recent event in sink, or
// "" when the log is empty. Read-only; does not mutate the chain.
func chainHeadOf(sink *FileSink) (string, error) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	events, err := readAllEvents(sink.path)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "", nil
	}
	return events[len(events)-1].Hash, nil
}

// extractRekorLogIndex pulls the Rekor log index out of the
// Sigstore-keyless envelope shape. Returns -1 when the envelope
// does not carry a log index (e.g., RegistryManagedKey or a
// Sigstore-keyless flow with no Rekor configured).
func extractRekorLogIndex(envelope string) int64 {
	type partial struct {
		LogIndex int64 `json:"log_index"`
	}
	var p partial
	// Best-effort decode; any failure returns -1.
	_ = json.Unmarshal([]byte(envelope), &p)
	if p.LogIndex == 0 {
		return -1
	}
	return p.LogIndex
}
