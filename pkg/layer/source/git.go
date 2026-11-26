package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"
)

// DefaultFetchTimeout is the §13.12 default of PODIUM_GIT_FETCH_TIMEOUT, the
// deadline of one Git snapshot.
//
// Spec: §7.3.1, §13.12
const DefaultFetchTimeout = 10 * time.Minute

// FetchTimeoutSetting names the operator setting that sets Git.FetchTimeout.
// A snapshot that exceeds the deadline names it in its error, so an operator
// who reads ingest.source_unreachable knows which value to raise.
const FetchTimeoutSetting = "PODIUM_GIT_FETCH_TIMEOUT"

// Git is the built-in git LayerSourceProvider per spec §4.6 source
// types and §7.3.1 ingestion triggers. It clones the configured repo
// in-memory and exposes the tree at the configured ref as an fs.FS.
type Git struct {
	// FetchTimeout bounds one Snapshot: the clone and the resolution of the
	// ref's tree. The returned Files read the in-memory clone and make no
	// network request. Zero selects DefaultFetchTimeout, so a Git value built
	// without the setting still never waits on a remote without bound.
	FetchTimeout time.Duration
}

// ID returns "git".
func (Git) ID() string { return "git" }

// Trigger returns TriggerWebhook; the registry ingests on a configured
// webhook delivery (§7.3.1) and falls back to manual reingest /
// `podium layer watch` polling for refs without a webhook.
func (Git) Trigger() TriggerModel { return TriggerWebhook }

// Snapshot fetches the repo and returns the tree at cfg.Ref. The clone
// runs against an in-memory storer so callers do not pay disk overhead
// for source-of-truth ingest snapshots.
//
// When cfg.PriorRef is set, the clone keeps full history so the
// provider can answer the §7.3.1 force-push question: does the new
// ref include PriorRef in its ancestry? When PriorRef is empty, the
// clone is shallow.
//
// The snapshot runs under the FetchTimeout deadline on every transport, so a
// remote that hangs or trickles data fails the snapshot as
// ErrSourceUnreachable instead of holding the layer's ingest cycle, and its
// claim, without bound.
//
// Spec: §7.3.1 (Source unreachable), §13.12
func (g Git) Snapshot(ctx context.Context, cfg LayerConfig) (*Snapshot, error) {
	if cfg.Repo == "" {
		return nil, fmt.Errorf("%w: git source requires repo", ErrInvalidConfig)
	}
	if cfg.Ref == "" {
		return nil, fmt.Errorf("%w: git source requires ref", ErrInvalidConfig)
	}
	timeout := g.FetchTimeout
	if timeout <= 0 {
		timeout = DefaultFetchTimeout
	}
	deadline := time.Now().Add(timeout)
	fetchCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	proxyOpts, release := snapshotProxyOptions(fetchCtx, cfg.Repo)
	defer release()
	snap, err := boundedSnapshot(fetchCtx, cfg, proxyOpts)
	// Only the expiry of this deadline names the setting. The clone error is
	// dropped in that case, because its cause is the deadline.
	if err != nil && fetchDeadlinePassed(ctx, fetchCtx, deadline) {
		return nil, fmt.Errorf("%w: git snapshot did not finish within %s (%s)",
			ErrSourceUnreachable, FetchTimeoutSetting, timeout)
	}
	return snap, err
}

// fetchDeadlinePassed reports whether a failed snapshot ended because the
// FetchTimeout deadline passed. A caller context that ended, or whose own
// deadline comes first, owns the failure, which keeps its own error.
//
// The clock is read as well as fetchCtx.Err: a context records its expiry
// only when its timer runs, and a transport's own timer or a peer's close at
// the same instant can fail the clone first, while Err is still nil.
func fetchDeadlinePassed(ctx, fetchCtx context.Context, deadline time.Time) bool {
	if ctx.Err() != nil {
		return false
	}
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
		return false
	}
	return errors.Is(fetchCtx.Err(), context.DeadlineExceeded) || !time.Now().Before(deadline)
}

// boundedSnapshot runs snapshotGit and returns no later than ctx's end.
//
// The clone runs in its own goroutine, and this function returns at ctx's end
// without waiting for it, because not every step of a clone can be
// interrupted:
//
//   - go-git's git:// transport dials with net.Dial, with no deadline and no
//     hook, before it reads ctx, so a git:// remote whose address drops the
//     connection attempt keeps the goroutine in that dial until the operating
//     system abandons it, which takes a minute or two. It holds one connection
//     attempt and an empty in-memory storer, and once the dial ends it reads
//     the expired ctx, fails, and closes the connection.
//   - go-git resolves the deltas of a received pack, which it has already read
//     in full, without reading ctx, so a deadline that passes then leaves the
//     goroutine resolving them, for a time and with memory that grow with the
//     pack.
//
// Every other step ends at ctx's end: the http transport honours ctx on every
// request, the ssh transport dials through deadlineDialer, which closes the
// connection when ctx ends, and readSnapshot checks ctx between its steps and
// during the history walk.
func boundedSnapshot(ctx context.Context, cfg LayerConfig, proxyOpts transport.ProxyOptions) (*Snapshot, error) {
	type result struct {
		snap *Snapshot
		err  error
	}
	done := make(chan result, 1)
	go func() {
		snap, err := snapshotGit(ctx, cfg, proxyOpts)
		done <- result{snap, err}
	}()
	select {
	case r := <-done:
		return r.snap, r.err
	case <-ctx.Done():
		// A clone that finished as the deadline passed keeps its result.
		select {
		case r := <-done:
			return r.snap, r.err
		default:
			return nil, fmt.Errorf("%w: %v", ErrSourceUnreachable, ctx.Err())
		}
	}
}

// snapshotGit clones cfg.Repo under ctx and reads the tree at cfg.Ref.
func snapshotGit(ctx context.Context, cfg LayerConfig, proxyOpts transport.ProxyOptions) (*Snapshot, error) {
	storer := memory.NewStorage()
	cloneOpts := &git.CloneOptions{
		URL:           cfg.Repo,
		ReferenceName: refNameFor(cfg.Ref),
		SingleBranch:  true,
		ProxyOptions:  proxyOpts,
	}
	if cfg.PriorRef == "" {
		cloneOpts.Depth = 1
	}
	repo, err := git.CloneContext(ctx, storer, nil, cloneOpts)
	if err != nil {
		// Spec: §7.3.1 (Repository credentials). go-git and net/http print the
		// request URL, userinfo included, in a failed clone's error.
		return nil, fmt.Errorf("%w: %s", ErrSourceUnreachable, RedactCloneError(cfg.Repo, err))
	}
	return readSnapshot(ctx, repo, cfg)
}

// readSnapshot resolves cfg.Ref in a cloned repo, reads its tree, and runs the
// §7.3.1 force-push check. It reads the in-memory clone and makes no network
// request. It checks ctx before each step and during the history walk, so a
// snapshot whose deadline passed stops working rather than finish a result no
// caller reads.
func readSnapshot(ctx context.Context, repo *git.Repository, cfg LayerConfig) (*Snapshot, error) {
	if err := snapshotCtxErr(ctx); err != nil {
		return nil, err
	}
	hash, err := repo.ResolveRevision(plumbing.Revision(cfg.Ref))
	if err != nil {
		return nil, fmt.Errorf("%w: resolve %q: %v", ErrSourceUnreachable, cfg.Ref, err)
	}
	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, fmt.Errorf("%w: commit %q: %v", ErrSourceUnreachable, hash, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("%w: tree %q: %v", ErrSourceUnreachable, hash, err)
	}
	if cfg.Root != "" {
		tree, err = tree.Tree(strings.TrimPrefix(cfg.Root, "/"))
		if err != nil {
			return nil, fmt.Errorf("%w: subtree %q: %v", ErrSourceUnreachable, cfg.Root, err)
		}
	}
	if err := snapshotCtxErr(ctx); err != nil {
		return nil, err
	}

	rewritten := false
	if cfg.PriorRef != "" {
		priorHash := plumbing.NewHash(cfg.PriorRef)
		// A zero hash on PriorRef means "first ingest" — never a
		// rewrite. Otherwise, walk the new ref's ancestry; if we hit
		// the prior commit, it's still reachable (no rewrite).
		if priorHash != plumbing.ZeroHash {
			reachable, err := isAncestor(ctx, repo, priorHash, *hash)
			if err != nil {
				return nil, err
			}
			rewritten = !reachable
		}
	}

	return &Snapshot{
		Reference:        hash.String(),
		Files:            &gitTreeFS{tree: tree},
		CreatedAt:        time.Now().UTC(),
		HistoryRewritten: rewritten,
	}, nil
}

// snapshotCtxErr returns the source-unreachable failure of a snapshot whose
// context ended, or nil.
func snapshotCtxErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrSourceUnreachable, err)
	}
	return nil
}

// ancestorWalkCheckEvery is how many commits isAncestor visits between two
// reads of its context. Visiting a commit decodes it from the in-memory
// clone, which takes microseconds, so the walk stops within about a
// millisecond of its context's end and the check costs nothing measurable.
const ancestorWalkCheckEvery = 256

// isAncestor reports whether ancestor is reachable from descendant
// via parent edges. Used by Git.Snapshot for §7.3.1 force-push
// detection. Walks BFS over the local clone; a missing object yields
// false (treated as "not reachable, therefore rewrite"). An unreachable
// ancestor makes the walk cover the whole history, so it stops with the
// snapshot's source-unreachable failure when ctx ends.
func isAncestor(ctx context.Context, repo *git.Repository, ancestor, descendant plumbing.Hash) (bool, error) {
	descCommit, err := repo.CommitObject(descendant)
	if err != nil {
		return false, nil
	}
	seen := map[plumbing.Hash]bool{}
	queue := []*object.Commit{descCommit}
	for visited := 0; len(queue) > 0; visited++ {
		if visited%ancestorWalkCheckEvery == 0 {
			if err := snapshotCtxErr(ctx); err != nil {
				return false, err
			}
		}
		c := queue[0]
		queue = queue[1:]
		if c.Hash == ancestor {
			return true, nil
		}
		if seen[c.Hash] {
			continue
		}
		seen[c.Hash] = true
		for _, ph := range c.ParentHashes {
			if seen[ph] {
				continue
			}
			pc, err := repo.CommitObject(ph)
			if err != nil {
				continue
			}
			queue = append(queue, pc)
		}
	}
	return false, nil
}

// refNameFor maps a user-supplied ref into the qualified ref name
// go-git's CloneOptions wants. The user can pass:
//
//	refs/heads/main      -> kept as-is
//	main                 -> refs/heads/main
//	v1.2.3               -> refs/tags/v1.2.3 (when the heads form fails)
//	<full sha>           -> kept; CloneOptions.SingleBranch=false in that case
//
// We default to refs/heads/<ref>; the resolver falls back to tags via
// ResolveRevision if the head form is not present.
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
