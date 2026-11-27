package integration

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/registry/ingest"
)

// gitEntry is one file of a commit tree: its blob bytes and its mode.
type gitEntry struct {
	data string
	mode filemode.FileMode
}

// writeGitTree stores the tree of files, keyed by slash path, and returns its
// hash. Entries are written in Git's order, where a directory sorts as its
// name followed by a slash.
func writeGitTree(t *testing.T, st storage.Storer, files map[string]gitEntry) plumbing.Hash {
	t.Helper()
	blobs := map[string]gitEntry{}
	dirs := map[string]map[string]gitEntry{}
	for p, e := range files {
		if i := strings.Index(p, "/"); i >= 0 {
			if dirs[p[:i]] == nil {
				dirs[p[:i]] = map[string]gitEntry{}
			}
			dirs[p[:i]][p[i+1:]] = e
			continue
		}
		blobs[p] = e
	}
	var entries []object.TreeEntry
	for name, e := range blobs {
		obj := st.NewEncodedObject()
		obj.SetType(plumbing.BlobObject)
		w, err := obj.Writer()
		if err != nil {
			t.Fatalf("blob writer: %v", err)
		}
		if _, err := w.Write([]byte(e.data)); err != nil {
			t.Fatalf("write blob: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close blob: %v", err)
		}
		h, err := st.SetEncodedObject(obj)
		if err != nil {
			t.Fatalf("store blob: %v", err)
		}
		entries = append(entries, object.TreeEntry{Name: name, Mode: e.mode, Hash: h})
	}
	for name, sub := range dirs {
		entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: writeGitTree(t, st, sub)})
	}
	key := func(e object.TreeEntry) string {
		if e.Mode == filemode.Dir {
			return e.Name + "/"
		}
		return e.Name
	}
	sort.Slice(entries, func(i, j int) bool { return key(entries[i]) < key(entries[j]) })
	obj := st.NewEncodedObject()
	if err := (&object.Tree{Entries: entries}).Encode(obj); err != nil {
		t.Fatalf("encode tree: %v", err)
	}
	h, err := st.SetEncodedObject(obj)
	if err != nil {
		t.Fatalf("store tree: %v", err)
	}
	return h
}

// Spec: §4.7.6, §11 (Package and layer digest tests) — the package digest of
// a git layer covers the blob bytes and tree paths of the snapshot commit,
// which are the bytes ingest reads, stores, and serves. A symbolic-link entry
// is a file whose bytes are its link text, a file under a .gitattributes
// line-ending rule keeps its committed line endings, a Git LFS pointer is
// digested as the pointer, and two paths that differ only in letter case are
// two entries. A checkout that applies those rules digests differently, so a
// signer reads the commit rather than a checkout.
func TestAttestDigest_GitSnapshotDigestsTheBlobBytes(t *testing.T) {
	t.Parallel()
	const (
		manifestMD = "---\ntype: rule\nversion: 1.0.0\ndescription: Apply the rule.\nrule_mode: always\n---\n\nBody.\n"
		linkText   = "../../../shared/common.md"
		lfsPointer = "version https://git-lfs.github.com/spec/v1\noid sha256:4d7a214614ab2935c943f9e0ff69d22eadbb8f32b1258daaa5e2ca24d17e2393\nsize 12345\n"
	)
	tree := map[string]gitEntry{
		".gitattributes":            {data: "*.md text eol=crlf\n*.bin filter=lfs diff=lfs merge=lfs -text\n", mode: filemode.Regular},
		"shared/common.md":          {data: "Shared text.\n", mode: filemode.Regular},
		"team/pkg/ARTIFACT.md":      {data: manifestMD, mode: filemode.Regular},
		"team/pkg/refs/common.md":   {data: linkText, mode: filemode.Symlink},
		"team/pkg/model.bin":        {data: lfsPointer, mode: filemode.Regular},
		"team/pkg/notes/Readme.md":  {data: "Upper.\n", mode: filemode.Regular},
		"team/pkg/notes/README.md":  {data: "Lower.\n", mode: filemode.Regular},
		"team/pkg/scripts/run.sh":   {data: "#!/bin/sh\necho run\n", mode: filemode.Executable},
		"team/DOMAIN.md":            {data: "---\ndescription: Team.\n---\n", mode: filemode.Regular},
		"team/pkg/.cache/blob.json": {data: "{}\n", mode: filemode.Regular},
	}

	dir := t.TempDir()
	repo, err := git.PlainInit(dir, true)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	treeHash := writeGitTree(t, repo.Storer, tree)
	sig := object.Signature{Name: "alice", Email: "alice@acme.com", When: time.Unix(0, 0).UTC()}
	commit := repo.Storer.NewEncodedObject()
	if err := (&object.Commit{Author: sig, Committer: sig, Message: "c", TreeHash: treeHash}).Encode(commit); err != nil {
		t.Fatalf("encode commit: %v", err)
	}
	head, err := repo.Storer.SetEncodedObject(commit)
	if err != nil {
		t.Fatalf("store commit: %v", err)
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference("refs/heads/main", head)); err != nil {
		t.Fatalf("set main: %v", err)
	}
	if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, "refs/heads/main")); err != nil {
		t.Fatalf("set HEAD: %v", err)
	}

	snap, err := (source.Git{}).Snapshot(context.Background(), source.LayerConfig{Repo: "file://" + dir, Ref: "main"})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	got, err := ingest.LayerContent(snap.Files)
	if err != nil {
		t.Fatalf("ingest.LayerContent: %v", err)
	}
	if len(got.Subjects) != 1 || got.Subjects[0].Name != "team/pkg@1.0.0" {
		t.Fatalf("subjects = %+v, want team/pkg@1.0.0 alone", got.Subjects)
	}
	entries := map[string]attest.Sum{}
	for p, e := range tree {
		if rel, ok := strings.CutPrefix(p, "team/pkg/"); ok {
			entries[rel] = sha256.Sum256([]byte(e.data))
		}
	}
	want := attest.PackageDigest(attest.Package{
		Resolved: true,
		Fields:   manifest.ResolvedFields{Type: manifest.TypeRule},
		Entries:  entries,
	})
	if got.Subjects[0].Digest != want {
		t.Errorf("package digest = %s, want the digest of the blob bytes %s", got.Subjects[0].Digest, want)
	}
	if _, ok := got.Files["team/DOMAIN.md"]; !ok || len(got.Files) != 1 {
		t.Errorf("layer files = %v, want team/DOMAIN.md alone", got.Files)
	}

	// A checkout that converts the manifest's line endings and writes the
	// link as a link gives another package digest.
	co := t.TempDir()
	for p, e := range tree {
		full := filepath.Join(co, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		switch {
		case e.mode == filemode.Symlink:
			err = os.Symlink(e.data, full)
		case strings.HasSuffix(p, ".md"):
			err = os.WriteFile(full, []byte(strings.ReplaceAll(e.data, "\n", "\r\n")), 0o644)
		default:
			err = os.WriteFile(full, []byte(e.data), 0o644)
		}
		if err != nil && !os.IsExist(err) {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	checkout, err := filesystem.LayerContent(filesystem.Layer{ID: "co", Path: co})
	if err != nil {
		t.Fatalf("filesystem.LayerContent: %v", err)
	}
	if len(checkout.Subjects) != 1 || checkout.Subjects[0].Digest == want {
		t.Errorf("checkout subjects = %+v; a checkout with converted bytes must not give the commit's digest %s", checkout.Subjects, want)
	}
}
