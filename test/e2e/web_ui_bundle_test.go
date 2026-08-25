package e2e

// End-to-end coverage of the committed §13.10 UI bundle: the binary
// embeds the built bundle at compile time, so what the running server
// returns from /ui/ is the only place the bundle's served root and its
// asset references can be checked together.

import (
	"regexp"
	"strings"
	"testing"
)

// bundleAssetRefPattern matches the src of a <script> and the href of a
// stylesheet <link> in the built index document.
var bundleAssetRefPattern = regexp.MustCompile(`(?:src|href)="([^"]+\.(?:js|css))"`)

// Spec: §13.10 — a binary started with PODIUM_WEB_UI=true serves the
// built bundle's index.html at /ui/, and every script and stylesheet
// that index references resolves under the same mount on the same
// running binary. A bundle built with the default public base of / emits
// references the outer mux answers with 404, which leaves /ui/ blank
// while the index itself still returns 200.
func TestWebUI_ServedBundleAssetsResolve(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_WEB_UI=true"},
		"serve", "--standalone", "--layer-path", reg)

	st, body := getRaw(t, srv.BaseURL+"/ui/")
	if st != 200 {
		t.Fatalf("GET /ui/ status = %d, want 200\nlog:\n%s", st, srv.log())
	}
	index := string(body)
	if !strings.Contains(index, "<title>Podium</title>") {
		t.Fatalf("GET /ui/ did not return the bundle index: %.200s", index)
	}

	refs := bundleAssetRefPattern.FindAllStringSubmatch(index, -1)
	if len(refs) == 0 {
		t.Fatalf("served index references no script or stylesheet: %s", index)
	}
	for _, ref := range refs {
		url, ok := bundleAssetURL(srv.BaseURL, ref[1])
		if !ok {
			t.Errorf("asset reference %q is rooted outside the /ui/ mount", ref[1])
			continue
		}
		if st, _ := getRaw(t, url); st != 200 {
			t.Errorf("GET %s status = %d, want 200\nlog:\n%s", url, st, srv.log())
		}
	}
}

// bundleAssetURL resolves an asset reference from the served index against
// the /ui/ mount. A relative reference resolves against the served index's
// directory, and a reference rooted at /ui/ resolves against the origin.
// It reports false when the reference is rooted outside the mount, which
// is the one form the outer mux answers with 404.
func bundleAssetURL(base, ref string) (string, bool) {
	if !strings.HasPrefix(ref, "/") {
		return base + "/ui/" + strings.TrimPrefix(ref, "./"), true
	}
	if strings.HasPrefix(ref, "/ui/") {
		return base + ref, true
	}
	return "", false
}
