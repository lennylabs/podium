package sync

import (
	"fmt"
	"os"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/materialize"
)

// dropUnsafeLockPaths returns a copy of lock without the entries whose
// materialized_path fails the §7.5.3 recorded-path rule
// (materialize.CheckRecordedPath), and names each dropped entry on standard
// error. The stale-file cleanup, the §7.5.2 comparison, and the §13.11.3 keep
// decision all read the returned lock, so a refused path is neither removed,
// rewritten, read, nor carried into the lock the run writes. An entry that
// records no path is kept as it is: it names no file to clean up. A nil lock
// returns nil.
//
// Spec: §7.5.3
func dropUnsafeLockPaths(lock *LockFile) *LockFile {
	if lock == nil {
		return nil
	}
	out := *lock
	out.Artifacts = make([]LockArtifact, 0, len(lock.Artifacts))
	for _, a := range lock.Artifacts {
		if a.MaterializedPath != "" {
			if err := materialize.CheckRecordedPath(a.MaterializedPath); err != nil {
				fmt.Fprintf(os.Stderr, "sync: stale-file cleanup: skipping lock entry %q (id %q): %v\n", a.MaterializedPath, a.ID, err)
				continue
			}
		}
		out.Artifacts = append(out.Artifacts, a)
	}
	return &out
}

// dropUnrecordablePaths returns files without the files whose path fails the
// §7.5.3 recorded-path rule, and names each refused file on standard error.
// The rule that admits a prior-lock entry also governs what sync writes and
// records, so a path the run writes is a path the next run's cleanup accepts:
// a bundled resource whose name holds a backslash, for example, is neither
// written nor recorded rather than recorded and then refused on every later
// run. owner names the artifact, or the marketplace manifest, the files
// belong to.
//
// Spec: §7.5.3
func dropUnrecordablePaths(owner string, files []adapter.File) []adapter.File {
	out := files[:0:0]
	for _, f := range files {
		if recordable(owner, f.Path) {
			out = append(out, f)
		}
	}
	return out
}

// recordable reports whether path passes the §7.5.3 recorded-path rule, and
// names the refused path and its owner on standard error when it does not.
func recordable(owner, path string) bool {
	if err := materialize.CheckRecordedPath(path); err != nil {
		fmt.Fprintf(os.Stderr, "sync: refusing to write %q for %s: %v\n", path, owner, err)
		return false
	}
	return true
}
