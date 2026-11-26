package sync

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/lennylabs/podium/pkg/materialize"
)

// ErrStaleFilesKept signals a §7.8 render whose stale-file cleanup would
// leave, or left, a recorded file in place because the §7.5.3 file-hash check
// did not admit its removal or the removal failed. The render fails before the
// publish phase: the lock is not a compared path (§7.5.2), so a render whose
// only effect is a removal it could not make reports no change, the publish
// phase skips the commit, and the next render reads the same committed lock
// and keeps the file again, leaving an artifact that left the view published
// indefinitely.
var ErrStaleFilesKept = errors.New("the stale-file cleanup cannot remove recorded files")

// StaleFileKept is a stale recorded path the cleanup leaves on disk. Reason
// states why: the lock entry records no file_hash, the file's bytes do not
// match it, the file cannot be read, or the removal failed.
type StaleFileKept struct {
	Path   string
	Reason string
}

// keptReason returns the StaleFileKept reason for an error
// materialize.RemoveStale or materialize.CheckStale returned, or "" for nil
// and for an error that is not a refusal of the hash check or a failed
// removal, such as a path the §7.5.3 recorded-path rule refuses. Those paths
// are not files sync wrote, so they are reported on standard error only.
//
// Spec: §7.5.3
func keptReason(err error) string {
	switch {
	case errors.Is(err, materialize.ErrStaleHashMissing):
		return "the lock entry records no file_hash"
	case errors.Is(err, materialize.ErrStaleHashMismatch):
		return "the file does not match the file_hash its lock entry records"
	case errors.Is(err, materialize.ErrStaleUnreadable):
		return "the file cannot be read"
	case errors.Is(err, materialize.ErrStaleNotRemoved):
		return "the file cannot be removed"
	default:
		return ""
	}
}

// staleRefusals returns the stale standalone paths of prior that the cleanup
// would leave in place, judged before the render writes anything. It applies
// the selection removeStalePaths applies (a path the run writes, a path the
// recorded-path rule refuses, a config-merge path, and a path that names the
// same file as a path the run writes are not candidates) and then
// materialize.CheckStale, which changes nothing. The §7.8 render fails on a
// non-empty result, so the checkout keeps its tree and its lock: a render
// that failed after writing would leave a reused checkout with the new tree
// and the old lock, and the next render would find nothing to change.
//
// Spec: §7.5.3, §7.8
func staleRefusals(target string, prior map[string]LockArtifact, current map[string]bool) []StaleFileKept {
	var live []os.FileInfo
	var out []StaleFileKept
	for _, p := range slices.Sorted(maps.Keys(prior)) {
		entry := prior[p]
		if current[p] || entry.Merge != "" || materialize.CheckRecordedPath(p) != nil {
			continue
		}
		if live == nil {
			live = liveFiles(target, current, keptOutput{})
		}
		if aliasesLive(target, p, live) {
			continue
		}
		if reason := keptReason(materialize.CheckStale(target, p, entry.FileHash)); reason != "" {
			out = append(out, StaleFileKept{Path: p, Reason: reason})
		}
	}
	return out
}

// staleFilesKeptError builds the error a §7.8 render returns when its cleanup
// would leave, or left, recorded files in place. It names every kept path with
// its reason and states the remedies: remove the file from the repository,
// restore the bytes the render wrote, or, for an entry the render did not
// write, such as a forged one, remove the entry from the lock.
//
// Spec: §7.5.3, §7.8
func staleFilesKeptError(outputID string, kept []StaleFileKept) error {
	parts := make([]string, len(kept))
	for i, k := range kept {
		parts[i] = fmt.Sprintf("%s (%s)", k.Path, k.Reason)
	}
	return fmt.Errorf("publish %q: %w: %s; for each file, remove it from the repository by hand, restore the bytes the render wrote, "+
		"or remove its entry from .podium/sync.lock when the render did not write that file, then commit the change and render again",
		outputID, ErrStaleFilesKept, strings.Join(parts, ", "))
}
