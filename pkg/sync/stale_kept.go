package sync

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lennylabs/podium/pkg/materialize"
)

// ErrStaleFilesKept signals a §7.8 render whose stale-file cleanup left a
// recorded file in place because the §7.5.3 file-hash check did not admit its
// removal. The render fails before the publish phase: the lock is not a
// compared path (§7.5.2), so a render whose only effect is a removal it could
// not make reports no change, the publish phase skips the commit, and the next
// render reads the same committed lock and keeps the file again, leaving an
// artifact that left the view published indefinitely.
var ErrStaleFilesKept = errors.New("the stale-file cleanup left recorded files in place")

// StaleFileKept is a stale recorded path the cleanup left on disk because the
// §7.5.3 file-hash check did not admit its removal. Reason states why: the
// lock entry records no file_hash, the file's bytes do not match it, or the
// file cannot be read.
type StaleFileKept struct {
	Path   string
	Reason string
}

// keptReason returns the StaleFileKept reason for an error
// materialize.RemoveStale returned, or "" for an error that is not a refusal
// of the hash check, such as a path the §7.5.3 recorded-path rule refuses.
// Those paths are not files sync wrote, so they are reported on standard error
// only.
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
	default:
		return ""
	}
}

// staleFilesKeptError builds the error a §7.8 render returns when its cleanup
// left recorded files in place. It names every kept path with its reason and
// states the remedy: remove or restore each file in the repository by hand,
// after which the next render reconciles cleanly.
//
// Spec: §7.5.3, §7.8
func staleFilesKeptError(outputID string, kept []StaleFileKept) error {
	parts := make([]string, len(kept))
	for i, k := range kept {
		parts[i] = fmt.Sprintf("%s (%s)", k.Path, k.Reason)
	}
	return fmt.Errorf("publish %q: %w: %s; remove each file from the repository by hand, or restore the bytes the render wrote, then render again",
		outputID, ErrStaleFilesKept, strings.Join(parts, ", "))
}
