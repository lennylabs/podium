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

// themeOverrideTokens are token declarations the served stylesheet has to
// carry under each data-theme arm. The design brief fixes the resolution
// order as light by default, prefers-color-scheme switching the palette, and
// data-theme on the root overriding both, so an arm that re-declares no token
// loses every colour to the media query and the override wins in one
// direction alone.
var themeOverrideTokens = []string{"--page", "--ink", "--surf", "--bd"}

// Spec: §13.10 — the served stylesheet lets a data-theme attribute on the
// root element override the visitor's prefers-color-scheme in both
// directions. The stylesheet is the whole mechanism, and it is served from
// the binary, so the arms are read off what the running server returns.
func TestWebUI_ServedStylesheetOverridesEitherTheme(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_WEB_UI=true"},
		"serve", "--standalone", "--web-ui", "--layer-path", reg)

	st, index := getRaw(t, srv.BaseURL+"/ui/")
	if st != 200 {
		t.Fatalf("GET /ui/ status = %d, want 200\nlog:\n%s", st, srv.log())
	}
	styles := strings.Join(bundleStylesheets(t, srv, string(index)), "\n")
	if styles == "" {
		t.Fatalf("the served index references no stylesheet")
	}
	for _, theme := range []string{"light", "dark"} {
		arm := themeArm(styles, theme)
		if arm == "" {
			t.Errorf("the served stylesheet carries no data-theme=%s arm", theme)
			continue
		}
		for _, token := range themeOverrideTokens {
			if !strings.Contains(arm, token+":") {
				t.Errorf("the data-theme=%s arm declares no %s; the media query keeps supplying it", theme, token)
			}
		}
	}
}

// themeArm returns the declaration block of the rule the given data-theme
// value selects, which is where that arm's token declarations live.
func themeArm(styles, theme string) string {
	for _, quote := range []string{"'", `"`, ""} {
		marker := "[data-theme=" + quote + theme + quote + "]"
		at := strings.Index(styles, marker)
		if at < 0 {
			continue
		}
		open := strings.Index(styles[at:], "{")
		end := strings.Index(styles[at:], "}")
		if open < 0 || end < open {
			continue
		}
		return styles[at+open : at+end]
	}
	return ""
}

// fontFamilies the design fixes. Each one resolves from a font the bundle
// ships or it resolves from nothing, because the bundle loads no stylesheet
// from another origin and an air-gapped registry reaches no font host.
var fontFamilies = []string{"Space Grotesk", "JetBrains Mono", "Anton"}

// Spec: §13.10 — the served bundle names no font family it does not ship.
// The registry serves the UI from its own origin and a strict deployment
// reaches no font host, so a family named with no @font-face rule behind it
// renders as the fallback on every deployment while the type scale was sized
// against metrics that never load.
func TestWebUI_ServedBundleNamesNoFontItDoesNotShip(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_WEB_UI=true"},
		"serve", "--standalone", "--web-ui", "--layer-path", reg)

	st, index := getRaw(t, srv.BaseURL+"/ui/")
	if st != 200 {
		t.Fatalf("GET /ui/ status = %d, want 200\nlog:\n%s", st, srv.log())
	}
	styles := strings.Join(bundleStylesheets(t, srv, string(index)), "\n")
	served := strings.Contains(styles, "@font-face")
	for _, family := range fontFamilies {
		if strings.Contains(styles, family) && !served {
			t.Errorf("the served stylesheet names %q and carries no @font-face rule; nothing resolves that family", family)
		}
	}
}

// bundleStylesheets returns the bodies of the stylesheets the served index
// names.
func bundleStylesheets(t *testing.T, srv *serverProc, index string) []string {
	t.Helper()
	sheets := []string{}
	for _, ref := range bundleAssetRefPattern.FindAllStringSubmatch(index, -1) {
		if !strings.HasSuffix(ref[1], ".css") {
			continue
		}
		url, ok := bundleAssetURL(srv.BaseURL, ref[1])
		if !ok {
			t.Errorf("asset reference %q is rooted outside the /ui/ mount", ref[1])
			continue
		}
		st, body := getRaw(t, url)
		if st != 200 {
			t.Fatalf("GET %s status = %d, want 200\nlog:\n%s", url, st, srv.log())
		}
		sheets = append(sheets, string(body))
	}
	return sheets
}
