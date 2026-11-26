package sync

import (
	"errors"
	"io/fs"
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

// EnsureGitignoreEntries appends every entry missing from the workspace's
// .gitignore, creating the file when it is absent and keeping existing lines
// verbatim (§7.7). A cloned workspace can commit .gitignore as a symbolic
// link: one that resolves to a file inside the workspace is updated through
// the link, and one that resolves outside it, or into .git, is refused, so
// init never appends to a file outside the workspace.
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
	if errors.Is(err, fs.ErrNotExist) {
		return materialize.WriteStateFile(workspace, ".gitignore", add(nil), 0o644)
	}
	if err != nil {
		return err
	}
	return materialize.RewriteStale(workspace, ".gitignore", add)
}
