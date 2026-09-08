package web

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// The browser icons the UI references are copied into web/ui/public from
// docs/assets/logo, which is where the documentation site reads the same marks
// from. Two copies of an asset drift, and a drifted icon is invisible until
// someone compares two tabs, so the copies are asserted equal here rather than
// left to review.
//
// Spec: §13.10
func TestBundleIconsMatchTheDocumentationMarks(t *testing.T) {
	for _, name := range []string{
		"podium-mark-light.svg",
		"podium-mark-dark.svg",
		"podium-tile.svg",
	} {
		served, err := fs.ReadFile(Assets(), name)
		if err != nil {
			t.Fatalf("the bundle serves no %s: %v; rebuild web/ui and commit web/bundle", name, err)
		}
		origin, err := os.ReadFile(filepath.Join("..", "docs", "assets", "logo", name))
		if err != nil {
			t.Fatalf("reading the documentation mark %s: %v", name, err)
		}
		if !bytes.Equal(served, origin) {
			t.Errorf("%s differs between web/bundle and docs/assets/logo; copy the mark into web/ui/public and rebuild", name)
		}
	}
}

// The entry document must reference the icons, because a bundle that carries
// them and a page that names none leaves every tab on the browser default and
// puts a 404 in the access log for each load, which is the state this replaced.
//
// Spec: §13.10
func TestIndexReferencesTheIcons(t *testing.T) {
	index, err := fs.ReadFile(Assets(), "index.html")
	if err != nil {
		t.Fatalf("reading the bundle's index.html: %v", err)
	}
	for _, want := range []string{
		`href="/app/podium-mark-light.svg"`,
		`href="/app/podium-mark-dark.svg"`,
		`rel="apple-touch-icon" href="/app/podium-tile.svg"`,
	} {
		if !bytes.Contains(index, []byte(want)) {
			t.Errorf("the served index.html does not carry %s", want)
		}
	}
}
