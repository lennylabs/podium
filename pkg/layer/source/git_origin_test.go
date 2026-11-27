package source

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

// originRepo creates a working tree at dir with an origin remote of url,
// and with refs/remotes/origin/HEAD naming target when target is not empty.
func originRepo(t *testing.T, dir, url, target string) {
	t.Helper()
	commitWorkTree(t, dir, map[string]string{"skills/greet/ARTIFACT.md": "x\n"})
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if url != "" {
		if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{url}}); err != nil {
			t.Fatal(err)
		}
	}
	if target != "" {
		ref := plumbing.NewSymbolicReference(plumbing.NewRemoteHEADReferenceName("origin"), plumbing.ReferenceName(target))
		if err := repo.Storer.SetReference(ref); err != nil {
			t.Fatal(err)
		}
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — `podium attest init` reads
// the origin remote's URL and the default branch that
// refs/remotes/origin/HEAD names, from the top of a working tree.
func TestReadOrigin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	originRepo(t, dir, "git@github.com:acme/finance.git", "refs/remotes/origin/trunk")
	o, err := ReadOrigin(dir)
	if err != nil || o.URL != "git@github.com:acme/finance.git" || o.DefaultBranch != "trunk" {
		t.Errorf("ReadOrigin = %+v, %v", o, err)
	}

	noHead := t.TempDir()
	originRepo(t, noHead, "https://github.com/acme/finance", "")
	if o, err := ReadOrigin(noHead); err != nil || o.DefaultBranch != "" {
		t.Errorf("no origin HEAD: %+v, %v", o, err)
	}

	otherTarget := t.TempDir()
	originRepo(t, otherTarget, "https://github.com/acme/finance", "refs/heads/main")
	if o, err := ReadOrigin(otherTarget); err != nil || o.DefaultBranch != "" {
		t.Errorf("origin HEAD outside refs/remotes/origin: %+v, %v", o, err)
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — a repository with no
// origin, a directory below the top of the working tree, a bare
// repository, and a directory outside any repository are refused.
func TestReadOrigin_Refusals(t *testing.T) {
	t.Parallel()
	noOrigin := t.TempDir()
	originRepo(t, noOrigin, "", "")
	if _, err := ReadOrigin(noOrigin); !errors.Is(err, ErrNoOrigin) {
		t.Errorf("no origin: %v", err)
	}

	top := t.TempDir()
	originRepo(t, top, "https://github.com/acme/finance", "")
	if _, err := ReadOrigin(filepath.Join(top, "skills")); !errors.Is(err, ErrNotWorkTreeTop) {
		t.Errorf("below the top: %v", err)
	}

	bare := t.TempDir()
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOrigin(bare); err == nil {
		t.Error("a bare repository was read")
	}

	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "f"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOrigin(plain); err == nil {
		t.Error("a directory outside a repository was read")
	}
}
