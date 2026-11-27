package ingest_test

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
)

// openFailFS fails every Open of the name fail and exposes Open alone, so
// every fs helper reaches the failure.
type openFailFS struct {
	fs.FS
	fail string
}

func (f openFailFS) Open(name string) (fs.File, error) {
	if name == f.fail {
		return nil, errors.New("boom")
	}
	return f.FS.Open(name)
}

// Spec: §4.7.6, §6.10 — the ingest walk's layer digest input fails when the
// snapshot cannot be read, with the source-unreachable sentinel the walk
// reports for an unreadable directory, and when a package has no subject
// name. A file of an unparseable package that cannot be read fails the walk
// too, so its package digest never omits a file.
func TestLayerContent_Failures(t *testing.T) {
	t.Parallel()
	tree := fstest.MapFS{
		"ops/a/ARTIFACT.md":  {Data: []byte("---\ntype: [rule\n---\n")},
		"ops/a/data.txt":     {Data: []byte("x")},
		"ops/b/DOMAIN.md":    {Data: []byte("domain")},
		"ops/b/ARTIFACT.md":  {Data: []byte("---\ntype: rule\nversion: 1.0.0\n---\nBody.\n")},
		"ops/b/schemas/x.js": {Data: []byte("{}")},
	}
	if _, err := ingest.LayerContent(openFailFS{FS: tree, fail: "ops"}); !errors.Is(err, source.ErrSourceUnreachable) {
		t.Errorf("an unreadable directory = %v, want ErrSourceUnreachable", err)
	}
	if _, err := ingest.LayerContent(openFailFS{FS: tree, fail: "ops/a/data.txt"}); err == nil {
		t.Errorf("an unreadable file of an unparseable package = nil, want the read error")
	}
	if _, err := ingest.LayerContent(openFailFS{FS: tree, fail: "ops/b/schemas"}); !errors.Is(err, source.ErrSourceUnreachable) {
		t.Errorf("an unreadable package directory = %v, want ErrSourceUnreachable", err)
	}
	bad := fstest.MapFS{"ops/a\tb/SKILL.md": {Data: []byte("---\nname: a\ndescription: d\n---\n")}}
	if _, err := ingest.LayerContent(bad); !errors.Is(err, attest.ErrNoSubjectName) {
		t.Errorf("a package with no subject name = %v, want ErrNoSubjectName", err)
	}
	l, err := ingest.LayerContent(tree)
	if err != nil {
		t.Fatalf("LayerContent: %v", err)
	}
	if len(l.Subjects) != 2 || len(l.Files) != 1 {
		t.Errorf("LayerContent = %+v, want two subjects and one layer file", l)
	}
}
