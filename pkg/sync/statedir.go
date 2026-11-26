package sync

import (
	"errors"
	"fmt"
	"os"
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
	return &stateDirErr{dir: filepath.Join(target, ".podium"), cause: err}
}

// stateDirErr reports an unusable .podium. It matches ErrStateDirUnusable and
// every error cause matches, and its message leaves cause out: cause restates
// that .podium is a symbolic link or not a directory and names the state file
// relative to the .podium path the message already names.
type stateDirErr struct {
	dir   string
	cause error
}

func (e *stateDirErr) Error() string {
	return fmt.Sprintf("%v: %s: sync keeps its lock and sync.yaml in a .podium directory inside the target and does not follow a link there; replace it with a directory that holds those files",
		ErrStateDirUnusable, e.dir)
}

func (e *stateDirErr) Unwrap() []error { return []error{ErrStateDirUnusable, e.cause} }

// readPriorLock reads the prior lock for a run. An unusable .podium returns
// ErrStateDirUnusable, which fails the run. Any other read error, such as a
// lock link that resolves outside the target or a lock that does not parse,
// is named on standard error and the run proceeds as if no prior lock
// existed, so a lock the run cannot use is never ignored silently.
//
// Spec: §7.5.3
func readPriorLock(target string) (*LockFile, error) {
	lock, err := ReadLock(target)
	if err == nil {
		return lock, nil
	}
	if errors.Is(err, ErrStateDirUnusable) {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "sync: cannot read %s: %v; the run proceeds as if no prior lock existed\n", LockFilePath(target), err)
	return nil, nil
}

// writeStateFile writes one of sync's own files below target through
// materialize.WriteStateFile, naming an unusable .podium with the remedy.
func writeStateFile(target, rel string, data []byte) error {
	return stateDirError(target, materialize.WriteStateFile(target, rel, data, 0o644))
}
