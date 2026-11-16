package core

import (
	"context"

	"github.com/lennylabs/podium/pkg/store"
)

// EventAudienceWaiters reports how many callers of a.Visible are waiting on
// the shared layer read. TEST-1 case 14 uses it to confirm a caller is in the
// single-flight wait before it cancels the evaluator.
func EventAudienceWaiters(a *EventAudience) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.waiters
}

// AdmittedTags admits rec and returns the tags parsedArtifact reads from the
// admitted row. A load result carries no tags member, so the admission tests
// read through this export the tags a reader of the admitted row sees.
func AdmittedTags(ctx context.Context, r *Registry, rec store.ManifestRecord) ([]string, error) {
	served, err := r.admit(ctx, rec)
	if err != nil {
		return nil, err
	}
	return parsedArtifact(served).Tags, nil
}
