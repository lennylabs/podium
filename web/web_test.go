package web_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/podium/web"
)

// Spec: §13.10 — the built UI bundle ships inside the binary so a
// single distribution covers the standalone deployment, and its entry
// document sits at the root the file server mounts at /ui/.
func TestAssets_HasIndex(t *testing.T) {
	got, err := fs.ReadFile(web.Assets(), "index.html")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(got), "<title>Podium</title>") {
		t.Errorf("index.html missing title; got first 200 bytes: %.200s", got)
	}
}

// Spec: §13.10 — every script and stylesheet the built index references
// resolves under the /ui/ mount, which means the reference is rooted at
// /ui/ and the embedded set carries the file it names.
func TestAssets_ReferencedEntryPointsResolve(t *testing.T) {
	index, err := fs.ReadFile(web.Assets(), "index.html")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	refs := assetRefs(string(index))
	if len(refs) == 0 {
		t.Fatalf("built index references no script or stylesheet: %s", index)
	}
	for _, ref := range refs {
		if !strings.HasPrefix(ref, "/ui/") {
			t.Errorf("asset reference %q does not resolve under the /ui/ mount", ref)
			continue
		}
		path := strings.TrimPrefix(ref, "/ui/")
		if _, err := fs.ReadFile(web.Assets(), path); err != nil {
			t.Errorf("referenced asset %s missing from the bundle: %v", ref, err)
		}
	}
}

// assetRefPattern matches the src of a <script> and the href of a
// stylesheet <link> in the built index document.
var assetRefPattern = regexp.MustCompile(`(?:src|href)="([^"]+\.(?:js|css))"`)

// assetRefs returns the script and stylesheet URLs the index references.
func assetRefs(index string) []string {
	var refs []string
	for _, m := range assetRefPattern.FindAllStringSubmatch(index, -1) {
		refs = append(refs, m[1])
	}
	return refs
}
