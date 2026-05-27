package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
)

func writeLayersYAML(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .podium: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layers.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write layers.yaml: %v", err)
	}
}

func TestLoadLayerVisibility_MissingFile_ReturnsNil(t *testing.T) {
	root := t.TempDir()
	got, err := loadLayerVisibility(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil map for missing file, got %#v", got)
	}
}

func TestLoadLayerVisibility_ParsesAllVisibilityFields(t *testing.T) {
	root := t.TempDir()
	writeLayersYAML(t, root, `
layers:
  - name: common
    visibility:
      organization: true
  - name: jira
    visibility:
      groups: [engineering, sre]
  - name: alice-personal
    visibility:
      users: [alice@acme.com]
  - name: public-stuff
    visibility:
      public: true
  - name: no-vis
    description: layer without a visibility block, should be omitted from the map
`)
	got, err := loadLayerVisibility(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]layer.Visibility{
		"common":         {Organization: true},
		"jira":           {Groups: []string{"engineering", "sre"}},
		"alice-personal": {Users: []string{"alice@acme.com"}},
		"public-stuff":   {Public: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestLoadLayerVisibility_MalformedYAML_Errors(t *testing.T) {
	root := t.TempDir()
	writeLayersYAML(t, root, ":: not valid yaml ::\n  garbage:\n - -\n")
	if _, err := loadLayerVisibility(root); err == nil {
		t.Fatal("expected parse error on malformed YAML, got nil")
	}
}

func TestLoadLayerVisibility_EmptyLayersBlock_ReturnsEmptyMap(t *testing.T) {
	root := t.TempDir()
	writeLayersYAML(t, root, "layers: []\n")
	got, err := loadLayerVisibility(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %#v", got)
	}
}
