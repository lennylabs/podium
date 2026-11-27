package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// markResetKey keys the §4.7.11 mark reset a manual reingest carries in the
// request context the reingest runner receives. The inbound webhook trigger
// carries none.
type markResetKey struct{}

// WithMarkReset returns ctx carrying the mark reset r.
func WithMarkReset(ctx context.Context, r *ingest.MarkReset) context.Context {
	return context.WithValue(ctx, markResetKey{}, r)
}

// MarkResetFromContext returns the mark reset of a manual reingest, nil when
// the request carries none.
func MarkResetFromContext(ctx context.Context) *ingest.MarkReset {
	r, _ := ctx.Value(markResetKey{}).(*ingest.MarkReset)
	return r
}

// markReset reads the mark reset of a reingest body for the stored layer
// cfg. to_commit names the commit the reset records, and it is refused for a
// layer whose source provider answers no lineage query, for a value longer
// than 128 bytes, and on a git layer for a value that is not a full
// hexadecimal commit name. A to_commit without reset_attestation_mark is
// refused, so a reset is never applied by accident.
//
// Spec: §4.7.11 (Mark reset), §7.3.1
func markReset(body reingestRequest, cfg store.LayerConfig) (*ingest.MarkReset, error) {
	if !body.ResetAttestationMark {
		if body.ToCommit != "" {
			return nil, fmt.Errorf("to_commit applies only with reset_attestation_mark")
		}
		return nil, nil
	}
	commit := body.ToCommit
	switch {
	case commit == "":
	case cfg.SourceType != "git":
		return nil, fmt.Errorf("to_commit applies to a layer whose source answers lineage queries; the %s source of layer %s answers none", cfg.SourceType, cfg.ID)
	case len(commit) > source.MaxLineageStringBytes:
		return nil, fmt.Errorf("to_commit holds %d bytes, more than %d", len(commit), source.MaxLineageStringBytes)
	case !fullCommitName(commit):
		return nil, fmt.Errorf("to_commit %q is not a full hexadecimal commit name", commit)
	}
	return &ingest.MarkReset{ToCommit: strings.ToLower(commit)}, nil
}

// fullCommitName reports whether s is a full hexadecimal Git object name: 40
// digits under SHA-1 or 64 under SHA-256.
func fullCommitName(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
