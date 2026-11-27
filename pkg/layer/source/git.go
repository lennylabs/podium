package source

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// DefaultFetchTimeout is the §13.12 default of PODIUM_GIT_FETCH_TIMEOUT, the
// deadline of one call of the Git source.
//
// Spec: §7.3.1, §13.12
const DefaultFetchTimeout = 10 * time.Minute

// FetchTimeoutSetting names the operator setting that sets Git.FetchTimeout.
// A call that exceeds the deadline names it in its error, so an operator who
// reads ingest.source_unreachable knows which value to raise.
const FetchTimeoutSetting = "PODIUM_GIT_FETCH_TIMEOUT"

// Git is the built-in git LayerSourceProvider per spec §4.6 source
// types and §7.3.1 ingestion triggers. It clones the configured repo
// in-memory at a bounded depth, deepens the clone while an answer it needs is
// undecided, and exposes the tree at the configured ref as an fs.FS.
type Git struct {
	// FetchTimeout bounds one call: a Snapshot, which is the clone, its
	// deepening rounds, and the resolution of the ref's tree, or a Lineage
	// query with all its rounds. The returned Files read the in-memory clone
	// and make no network request. Zero selects DefaultFetchTimeout, so a Git
	// value built without the setting still never waits on a remote without
	// bound.
	FetchTimeout time.Duration
	// FetchDepth is the starting depth of each call's clone, in commits. Zero
	// selects DefaultFetchDepth, and a value above MaxFetchDepth is read as
	// MaxFetchDepth, because the next value requests the complete history.
	FetchDepth int
}

// ID returns "git".
func (Git) ID() string { return "git" }

// Trigger returns TriggerWebhook; the registry ingests on a configured
// webhook delivery (§7.3.1) and falls back to manual reingest /
// `podium layer watch` polling for refs without a webhook.
func (Git) Trigger() TriggerModel { return TriggerWebhook }

// AnswersLineage returns true: the Git source answers lineage queries.
//
// Spec: §4.6 (Source types)
func (Git) AnswersLineage() bool { return true }

func (g Git) fetchTimeout() time.Duration {
	if g.FetchTimeout <= 0 {
		return DefaultFetchTimeout
	}
	return g.FetchTimeout
}

func (g Git) fetchDepth() int {
	switch {
	case g.FetchDepth <= 0:
		return DefaultFetchDepth
	case g.FetchDepth > MaxFetchDepth:
		return MaxFetchDepth
	default:
		return g.FetchDepth
	}
}

// requireRepoAndRef refuses a configuration that names no repository or ref.
func requireRepoAndRef(cfg LayerConfig) error {
	if cfg.Repo == "" {
		return fmt.Errorf("%w: git source requires repo", ErrInvalidConfig)
	}
	if cfg.Ref == "" {
		return fmt.Errorf("%w: git source requires ref", ErrInvalidConfig)
	}
	return nil
}

// Snapshot clones the repo at the starting depth and returns the tree at
// cfg.Ref. The clone runs against an in-memory storer so callers do not pay
// disk overhead for source-of-truth ingest snapshots.
//
// When cfg.PriorRef is set, the snapshot answers the §7.3.1 force-push
// question, whether the snapshot commit is cfg.PriorRef or a descendant of it,
// and deepens the clone until the answer is decided. A history the remote
// cannot deepen far enough fails the snapshot as ErrSourceUnreachable rather
// than report a rewrite.
//
// Spec: §7.3.1 (Source unreachable, Force-push detected, Git fetch), §13.12
func (g Git) Snapshot(ctx context.Context, cfg LayerConfig) (*Snapshot, error) {
	if err := requireRepoAndRef(cfg); err != nil {
		return nil, err
	}
	return runCall(ctx, g, cfg, OperationSnapshot, func(ctx context.Context, c *gitCall) (*Snapshot, error) {
		return snapshotGit(ctx, c, g.fetchDepth())
	})
}

// snapshotGit clones c's repository at depth, fixes the snapshot commit, reads
// its tree, and runs the force-push check.
func snapshotGit(ctx context.Context, c *gitCall, depth int) (*Snapshot, error) {
	if err := c.clone(ctx, depth); err != nil {
		return nil, err
	}
	head, tree, err := readSnapshotTree(ctx, c.repo, c.cfg)
	if err != nil {
		return nil, err
	}
	rewritten, err := priorRewritten(ctx, c, head)
	if err != nil {
		return nil, err
	}
	return &Snapshot{
		Reference:        head.String(),
		Files:            &gitTreeFS{tree: tree},
		CreatedAt:        time.Now().UTC(),
		HistoryRewritten: rewritten,
	}, nil
}

// readSnapshotTree resolves cfg.Ref in a cloned repo and reads its tree. The
// commit it resolves is the snapshot commit, the starting commit of every walk
// of the snapshot. It reads the in-memory clone and makes no network request,
// and it checks ctx before and after its steps, so a snapshot whose deadline
// passed stops working rather than finish a result no caller reads.
func readSnapshotTree(ctx context.Context, repo *git.Repository, cfg LayerConfig) (plumbing.Hash, *object.Tree, error) {
	if err := snapshotCtxErr(ctx); err != nil {
		return plumbing.ZeroHash, nil, err
	}
	hash, err := resolveRef(repo, cfg.Ref)
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	commit, err := repo.CommitObject(hash)
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("%w: commit %q: %v", ErrSourceUnreachable, hash, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("%w: tree %q: %v", ErrSourceUnreachable, hash, err)
	}
	if cfg.Root != "" {
		tree, err = tree.Tree(strings.TrimPrefix(cfg.Root, "/"))
		if err != nil {
			return plumbing.ZeroHash, nil, fmt.Errorf("%w: subtree %q: %v", ErrSourceUnreachable, cfg.Root, err)
		}
	}
	if err := snapshotCtxErr(ctx); err != nil {
		return plumbing.ZeroHash, nil, err
	}
	return hash, tree, nil
}

// resolveRef resolves the tracked ref in the call's repository. After the
// clone, the local branch points at the commit the clone fetched, and a
// deepening fetch updates only the remote-tracking ref, so the commit this
// returns stays the one the first fetch fixed.
func resolveRef(repo *git.Repository, ref string) (plumbing.Hash, error) {
	hash, err := repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("%w: resolve %q: %v", ErrSourceUnreachable, ref, err)
	}
	return *hash, nil
}

// priorRewritten answers the §7.3.1 force-push question for a snapshot whose
// commit is head: whether the layer's last ingested reference is head or an
// ancestor of it. It deepens the clone while the answer is undecided, so a
// truncated history never reads as a rewrite. A zero hash means a first
// ingest, which is never a rewrite. A reference that is not a full
// hexadecimal commit name names no commit of the history, so it is a rewrite
// with no deepening.
//
// Spec: §7.3.1 (Force-push detected, Git fetch: Snapshot)
func priorRewritten(ctx context.Context, c *gitCall, head plumbing.Hash) (bool, error) {
	if c.cfg.PriorRef == "" {
		return false, nil
	}
	prior, ok := fullHexCommit(c.cfg.PriorRef)
	if !ok {
		return true, nil
	}
	if prior == plumbing.ZeroHash {
		return false, nil
	}
	answer := ancestryUndecided
	err := c.settle(ctx, func(ctx context.Context, walks *walkCache) (bool, progress, error) {
		w, err := walks.from(ctx, head)
		if err != nil {
			return false, progress{}, err
		}
		answer = w.ancestorOrSelf(prior)
		return answer != ancestryUndecided, progressOf(head, w), nil
	})
	if err != nil {
		return false, err
	}
	return answer == ancestryNo, nil
}

// snapshotCtxErr returns the source-unreachable failure of a call whose
// context ended, or nil.
func snapshotCtxErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrSourceUnreachable, err)
	}
	return nil
}

// refNameFor maps a user-supplied ref into the qualified ref name
// go-git's CloneOptions wants:
//
//	refs/heads/main      -> kept as-is
//	refs/tags/v1.2.3     -> kept as-is
//	main                 -> refs/heads/main
//
// A short name always names a branch. A tag is tracked by its full name, and a
// short name with no branch of that name fails the clone, which reports the
// missing reference.
func refNameFor(ref string) plumbing.ReferenceName {
	if strings.HasPrefix(ref, "refs/") {
		return plumbing.ReferenceName(ref)
	}
	return plumbing.NewBranchReferenceName(ref)
}

// gitTreeFS adapts a *object.Tree to fs.FS so the ingest pipeline can
// walk it like a directory.
type gitTreeFS struct {
	tree *object.Tree
}

// Open implements fs.FS.
func (g *gitTreeFS) Open(name string) (fs.File, error) {
	if name == "." {
		return &gitDir{tree: g.tree, name: "."}, nil
	}
	clean := strings.TrimSuffix(strings.TrimPrefix(name, "./"), "/")

	// Try a file first.
	entry, err := g.tree.File(clean)
	if err == nil {
		reader, err := entry.Reader()
		if err != nil {
			return nil, err
		}
		return &gitFile{
			name:   path.Base(clean),
			size:   entry.Size,
			reader: reader,
		}, nil
	}

	// Directory: navigate down each segment.
	segments := strings.Split(clean, "/")
	cur := g.tree
	for _, seg := range segments {
		next, err := cur.Tree(seg)
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		cur = next
	}
	return &gitDir{tree: cur, name: path.Base(clean)}, nil
}

// ReadDir implements fs.ReadDirFS.
func (g *gitTreeFS) ReadDir(name string) ([]fs.DirEntry, error) {
	target := g.tree
	if name != "." && name != "" {
		clean := strings.TrimSuffix(strings.TrimPrefix(name, "./"), "/")
		segments := strings.Split(clean, "/")
		cur := g.tree
		for _, seg := range segments {
			next, err := cur.Tree(seg)
			if err != nil {
				return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
			}
			cur = next
		}
		target = cur
	}
	return treeEntries(target), nil
}

func treeEntries(tree *object.Tree) []fs.DirEntry {
	out := make([]fs.DirEntry, 0, len(tree.Entries))
	for _, e := range tree.Entries {
		out = append(out, gitEntry{
			name:  e.Name,
			isDir: e.Mode == 0o040000,
		})
	}
	return out
}

// gitEntry implements fs.DirEntry over a tree entry.
type gitEntry struct {
	name  string
	isDir bool
}

func (e gitEntry) Name() string { return e.name }
func (e gitEntry) IsDir() bool  { return e.isDir }
func (e gitEntry) Type() fs.FileMode {
	if e.isDir {
		return fs.ModeDir
	}
	return 0
}
func (e gitEntry) Info() (fs.FileInfo, error) { return gitFileInfo{name: e.name, isDir: e.isDir}, nil }

// gitDir implements fs.File (ReadDirFile) for a tree.
type gitDir struct {
	tree *object.Tree
	name string
}

func (d *gitDir) Stat() (fs.FileInfo, error) { return gitFileInfo{name: d.name, isDir: true}, nil }
func (d *gitDir) Read([]byte) (int, error)   { return 0, fmt.Errorf("read on directory") }
func (d *gitDir) Close() error               { return nil }
func (d *gitDir) ReadDir(n int) ([]fs.DirEntry, error) {
	entries := treeEntries(d.tree)
	if n <= 0 || n >= len(entries) {
		return entries, nil
	}
	return entries[:n], nil
}

// gitFile implements fs.File for a blob.
type gitFile struct {
	name   string
	size   int64
	reader io.ReadCloser
}

func (f *gitFile) Stat() (fs.FileInfo, error) {
	return gitFileInfo{name: f.name, size: f.size}, nil
}
func (f *gitFile) Read(p []byte) (int, error) { return f.reader.Read(p) }
func (f *gitFile) Close() error               { return f.reader.Close() }

// gitFileInfo implements fs.FileInfo.
type gitFileInfo struct {
	name  string
	size  int64
	isDir bool
}

func (i gitFileInfo) Name() string { return i.name }
func (i gitFileInfo) Size() int64  { return i.size }
func (i gitFileInfo) Mode() fs.FileMode {
	if i.isDir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (i gitFileInfo) ModTime() time.Time { return time.Time{} }
func (i gitFileInfo) IsDir() bool        { return i.isDir }
func (i gitFileInfo) Sys() any           { return nil }
