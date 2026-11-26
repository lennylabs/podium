package sync

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lennylabs/podium/pkg/materialize"
)

// WriteWorkspaceConfig writes a workspace-scope config file, sync.yaml or
// sync.local.yaml, to <workspace>/.podium/ for `podium init` (§7.7). A cloned
// workspace can commit .podium or the file's temporary name as a symbolic
// link, so the write follows the rules of sync's own state (§7.5.3): a .podium
// that is a link fails with ErrStateDirUnusable, and the temporary is created
// exclusively.
func WriteWorkspaceConfig(workspace, filename string, data []byte) error {
	return writeStateFile(workspace, ".podium/"+filename, data)
}

// CheckWorkspaceConfig returns the refusal WriteWorkspaceConfig would return
// for filename, without writing anything. `podium init` calls it before it
// looks for an existing file, so a .podium that is a symbolic link is
// reported with its remedy rather than as a file that --force would
// overwrite.
//
// Spec: §7.5.3, §7.7
func CheckWorkspaceConfig(workspace, filename string) error {
	return stateDirError(workspace, materialize.CheckStateFile(workspace, ".podium/"+filename))
}

// ErrGitignoreLinkSkipped signals that `podium init` left the workspace's
// .gitignore alone because it is a symbolic link that does not resolve to a
// regular file inside the workspace. The caller reports it as a warning.
var ErrGitignoreLinkSkipped = errors.New("sync: .gitignore is a symbolic link that does not resolve to a regular file inside the workspace; skipping the .gitignore update")

// EnsureGitignoreEntries appends every entry missing from the workspace's
// .gitignore, creating the file when it is absent and keeping existing lines
// verbatim (§7.7). A cloned workspace can commit .gitignore as a symbolic
// link: one that resolves to a regular file inside the workspace is updated
// through the link, and any other link, one that resolves outside the
// workspace, into .git, or to no file, is left in place and reported with
// ErrGitignoreLinkSkipped, so init never appends to a file outside the
// workspace and never replaces the operator's link.
func EnsureGitignoreEntries(workspace string, entries []string) error {
	add := func(existing []byte) []byte {
		out := string(existing)
		for _, e := range entries {
			if strings.Contains(out, e) {
				continue
			}
			if len(out) > 0 && !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			out += e + "\n"
		}
		return []byte(out)
	}
	_, err := materialize.ReadRecorded(workspace, ".gitignore")
	if info, lerr := os.Lstat(filepath.Join(workspace, ".gitignore")); lerr == nil && info.Mode()&fs.ModeSymlink != 0 && err != nil {
		return fmt.Errorf("%w: %s: %v", ErrGitignoreLinkSkipped, filepath.Join(workspace, ".gitignore"), err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return materialize.WriteStateFile(workspace, ".gitignore", add(nil), 0o644)
	}
	if err != nil {
		return err
	}
	return materialize.RewriteStale(workspace, ".gitignore", add)
}
