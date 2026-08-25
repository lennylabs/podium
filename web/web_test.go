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
// resolves under the /ui/ mount. A reference is either relative to the
// served index or rooted at /ui/, and either way the embedded set carries
// the file it names. A reference rooted anywhere else leaves the mount and
// the outer mux answers it with 404.
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
		path, ok := bundlePath(ref)
		if !ok {
			t.Errorf("asset reference %q is rooted outside the /ui/ mount", ref)
			continue
		}
		if _, err := fs.ReadFile(web.Assets(), path); err != nil {
			t.Errorf("referenced asset %s missing from the bundle: %v", ref, err)
		}
	}
}

// bundlePath maps an asset reference from the built index onto its path
// within the embedded bundle. It reports false when the reference is
// rooted outside the /ui/ mount, which is the one form the mount cannot
// serve.
func bundlePath(ref string) (string, bool) {
	if !strings.HasPrefix(ref, "/") {
		return strings.TrimPrefix(ref, "./"), true
	}
	if rest, ok := strings.CutPrefix(ref, "/ui/"); ok {
		return rest, true
	}
	return "", false
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
