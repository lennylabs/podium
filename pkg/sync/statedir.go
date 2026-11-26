package sync

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/lennylabs/podium/pkg/materialize"
)

// ErrStateDirUnusable signals that a target's .podium is a symbolic link or is
// not a directory. Sync keeps its lock and sync.yaml there and never reads or
// writes them through a link, because a repository checkout can commit
// .podium as one (§7.8). The run fails with this error before it writes
// anything, rather than skipping the lock and orphaning later stale output.
var ErrStateDirUnusable = errors.New("sync: .podium is a symbolic link or not a directory")

// stateDirError returns err with the target's .podium named and the remedy
// stated when err reports an unusable state directory, and err unchanged
// otherwise.
//
// Spec: §7.5.3
func stateDirError(target string, err error) error {
	if !errors.Is(err, materialize.ErrPodiumNotDirectory) {
		return err
	}
	return fmt.Errorf("%w: %s: sync keeps its lock and sync.yaml in a .podium directory inside the target and does not follow a link there; replace it with a directory that holds those files: %w",
		ErrStateDirUnusable, filepath.Join(target, ".podium"), err)
}

// writeStateFile writes one of sync's own files below target through
// materialize.WriteStateFile, naming an unusable .podium with the remedy.
func writeStateFile(target, rel string, data []byte) error {
	return stateDirError(target, materialize.WriteStateFile(target, rel, data, 0o644))
}
