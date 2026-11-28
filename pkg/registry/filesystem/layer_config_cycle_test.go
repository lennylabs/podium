package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"
)

// failingFS fails every open with err, and every stat through Open.
type failingFS struct{ err error }

func (f failingFS) Open(string) (fs.File, error) { return nil, f.err }

// readFailFS stats .layer-config as a regular file and fails its read.
type readFailFS struct{ fstest.MapFS }

func (f readFailFS) ReadFile(name string) ([]byte, error) {
	return nil, errors.New("read refused")
}

// TestReadDirectoryVisibility covers how a cycle reads .layer-config: an
// absent file and an empty visibility block declare none, a file that is not
// a regular file is absent, a declared visibility is returned as declared,
// and a file that does not parse or that holds an entry §4.6 does not admit
// declares a visibility that admits no caller. A stat or a read that fails is
// an error.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer), §4.6
func TestReadDirectoryVisibility(t *testing.T) {
	t.Parallel()
	file := func(s string) fstest.MapFS {
		return fstest.MapFS{LayerConfigFile: &fstest.MapFile{Data: []byte(s)}}
	}
	cases := []struct {
		name string
		fsys fs.FS
		want string
	}{
		{"absent", fstest.MapFS{}, "{false false [] []} declared=false malformed=false"},
		{"directory", fstest.MapFS{LayerConfigFile + "/x": &fstest.MapFile{}}, "{false false [] []} declared=false malformed=false"},
		{"empty block", file("visibility: {}\n"), "{false false [] []} declared=false malformed=false"},
		{"empty lists", file("visibility:\n  groups: []\n"), "{false false [] []} declared=false malformed=false"},
		{"no block", file("other: 1\n"), "{false false [] []} declared=false malformed=false"},
		{"groups", file("visibility:\n  groups: [acme-finance]\n  users: [alice@acme.com]\n"), "{false false [acme-finance] [alice@acme.com]} declared=true malformed=false"},
		{"public", file("visibility:\n  public: true\n"), "{true false [] []} declared=true malformed=false"},
		{"does not parse", file("visibility: [\n"), "{false false [] []} declared=true malformed=true"},
		{"empty users entry", file("visibility:\n  organization: true\n  users: [\"\"]\n"), "{false false [] []} declared=true malformed=true"},
		{"control character", file("visibility:\n  groups: [\"a\\nb\"]\n"), "{false false [] []} declared=true malformed=true"},
	}
	for _, tc := range cases {
		got, err := ReadDirectoryVisibility(tc.fsys)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		s := fmt.Sprintf("{%v %v %v %v} declared=%v malformed=%v", got.Visibility.Public, got.Visibility.Organization,
			got.Visibility.Groups, got.Visibility.Users, got.Declared, got.Malformed)
		if s != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, s, tc.want)
		}
	}
	if _, err := ReadDirectoryVisibility(failingFS{err: fs.ErrPermission}); err == nil {
		t.Error("a stat that fails is no error")
	}
	if _, err := ReadDirectoryVisibility(readFailFS{file("visibility:\n  public: true\n")}); err == nil {
		t.Error("a read that fails is no error")
	}
}
