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
	"github.com/go-git/go-git/v5/storage/memory"
)

// Git is the built-in git LayerSourceProvider per spec §4.6 source
// types and §7.3.1 ingestion triggers. It clones the configured repo
// in-memory and exposes the tree at the configured ref as an fs.FS.
type Git struct{}

// ID returns "git".
func (Git) ID() string { return "git" }

// Trigger returns TriggerWebhook; the registry ingests on a configured
// webhook delivery (§7.3.1) and falls back to manual reingest /
// `podium layer watch` polling for refs without a webhook.
func (Git) Trigger() TriggerModel { return TriggerWebhook }

// Snapshot fetches the repo and returns the tree at cfg.Ref. The clone
// runs against an in-memory storer so callers do not pay disk overhead
// for source-of-truth ingest snapshots.
func (Git) Snapshot(ctx context.Context, cfg LayerConfig) (*Snapshot, error) {
	if cfg.Repo == "" {
		return nil, fmt.Errorf("%w: git source requires repo", ErrInvalidConfig)
	}
	if cfg.Ref == "" {
		return nil, fmt.Errorf("%w: git source requires ref", ErrInvalidConfig)
	}

	storer := memory.NewStorage()
	repo, err := git.CloneContext(ctx, storer, nil, &git.CloneOptions{
		URL:           cfg.Repo,
		ReferenceName: refNameFor(cfg.Ref),
		SingleBranch:  true,
		Depth:         1,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSourceUnreachable, err)
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
	return &Snapshot{
		Reference: hash.String(),
		Files:     &gitTreeFS{tree: tree},
		CreatedAt: time.Now().UTC(),
	}, nil
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

func (e gitEntry) Name() string                       { return e.name }
func (e gitEntry) IsDir() bool                        { return e.isDir }
func (e gitEntry) Type() fs.FileMode                  { if e.isDir { return fs.ModeDir }; return 0 }
func (e gitEntry) Info() (fs.FileInfo, error)         { return gitFileInfo{name: e.name, isDir: e.isDir}, nil }

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

func (i gitFileInfo) Name() string       { return i.name }
func (i gitFileInfo) Size() int64        { return i.size }
func (i gitFileInfo) Mode() fs.FileMode  { if i.isDir { return fs.ModeDir | 0o755 }; return 0o644 }
func (i gitFileInfo) ModTime() time.Time { return time.Time{} }
func (i gitFileInfo) IsDir() bool        { return i.isDir }
func (i gitFileInfo) Sys() any           { return nil }
