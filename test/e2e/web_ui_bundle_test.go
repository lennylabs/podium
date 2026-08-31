package e2e

// End-to-end coverage of the committed §13.10 UI bundle: the binary
// embeds the built bundle at compile time, so what the running server
// returns from /app/ is the only place the bundle's served root and its
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
// built bundle's index.html at /app/, and every script and stylesheet
// that index references resolves under the same mount on the same
// running binary. A bundle built with the default public base of / emits
// references the outer mux answers with 404, which leaves /app/ blank
// while the index itself still returns 200.
func TestWebUI_ServedBundleAssetsResolve(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_WEB_UI=true"},
		"serve", "--standalone", "--layer-path", reg)

	st, body := getRaw(t, srv.BaseURL+"/app/")
	if st != 200 {
		t.Fatalf("GET /app/ status = %d, want 200\nlog:\n%s", st, srv.log())
	}
	index := string(body)
	if !strings.Contains(index, "<title>Podium</title>") {
		t.Fatalf("GET /app/ did not return the bundle index: %.200s", index)
	}

	refs := bundleAssetRefPattern.FindAllStringSubmatch(index, -1)
	if len(refs) == 0 {
		t.Fatalf("served index references no script or stylesheet: %s", index)
	}
	for _, ref := range refs {
		url, ok := bundleAssetURL(srv.BaseURL, ref[1])
		if !ok {
			t.Errorf("asset reference %q is rooted outside the /app/ mount", ref[1])
			continue
		}
		if st, _ := getRaw(t, url); st != 200 {
			t.Errorf("GET %s status = %d, want 200\nlog:\n%s", url, st, srv.log())
		}
	}
}

// bundleAssetURL resolves an asset reference from the served index against
// the /app/ mount. A relative reference resolves against the served index's
// directory, and a reference rooted at /app/ resolves against the origin.
// It reports false when the reference is rooted outside the mount, which
// is the one form the outer mux answers with 404.
func bundleAssetURL(base, ref string) (string, bool) {
	if !strings.HasPrefix(ref, "/") {
		return base + "/app/" + strings.TrimPrefix(ref, "./"), true
	}
	if strings.HasPrefix(ref, "/app/") {
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

	st, index := getRaw(t, srv.BaseURL+"/app/")
	if st != 200 {
		t.Fatalf("GET /app/ status = %d, want 200\nlog:\n%s", st, srv.log())
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

// fontFamilies the design fixes: Space Grotesk for prose and UI, JetBrains
// Mono for identifiers, and Anton for the wordmark.
var fontFamilies = []string{"Space Grotesk", "JetBrains Mono", "Anton"}

// fontFacePattern matches one @font-face rule's family name and source URL in
// the served stylesheet, which the bundler emits with the quotes stripped.
var fontFacePattern = regexp.MustCompile(`@font-face\{font-family:([^;]+);[^}]*src:url\(([^)]+)\)`)

// Spec: §13.10 — the served bundle ships every font family it names. The
// registry serves the UI from its own origin and a strict deployment reaches
// no font host, so a family named with no @font-face rule behind it renders
// as the fallback on every deployment while the type scale was sized against
// metrics that never load. Self-hosting is what the design pass names as the
// remedy, so each family resolves from an asset under the /app/ mount.
func TestWebUI_ServedBundleShipsTheFontsItNames(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_WEB_UI=true"},
		"serve", "--standalone", "--web-ui", "--layer-path", reg)

	st, index := getRaw(t, srv.BaseURL+"/app/")
	if st != 200 {
		t.Fatalf("GET /app/ status = %d, want 200\nlog:\n%s", st, srv.log())
	}
	styles := strings.Join(bundleStylesheets(t, srv, string(index)), "\n")
	faces := map[string]string{}
	for _, rule := range fontFacePattern.FindAllStringSubmatch(styles, -1) {
		faces[strings.Trim(rule[1], `"' `)] = rule[2]
	}
	for _, family := range fontFamilies {
		if !strings.Contains(styles, family) {
			t.Errorf("the served stylesheet names no %q; the design fixes it as one of the three families", family)
			continue
		}
		src, ok := faces[family]
		if !ok {
			t.Errorf("the served stylesheet names %q and carries no @font-face rule; nothing resolves that family", family)
			continue
		}
		url, ok := bundleAssetURL(srv.BaseURL, src)
		if !ok {
			t.Errorf("the %q face is sourced from %q, which is rooted outside the /app/ mount", family, src)
			continue
		}
		if st, _ := getRaw(t, url); st != 200 {
			t.Errorf("GET %s status = %d, want 200; the %q face resolves from nothing\nlog:\n%s",
				url, st, family, srv.log())
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
			t.Errorf("asset reference %q is rooted outside the /app/ mount", ref[1])
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

// securityHeaders are the hardening headers every response from the running
// binary carries, listed with the value the response has to send.
var securityHeaders = map[string]string{
	"X-Content-Type-Options": "nosniff",
	"X-Frame-Options":        "DENY",
	"Referrer-Policy":        "no-referrer",
}

// Spec: §13.10 — the origin that serves the UI document also holds the
// browser session cookie, so the running binary sends the hardening headers
// on the UI document and on the API responses the page makes alongside it.
// The client-side markdown sanitizer is otherwise the only barrier between an
// artifact body and that origin, and the panel's destructive controls are
// otherwise framable by any page.
func TestWebUI_ServedResponsesCarrySecurityHeaders(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_WEB_UI=true"},
		"serve", "--standalone", "--web-ui", "--layer-path", reg)

	for _, path := range []string{"/app/", "/v1/ui/session"} {
		resp, err := httpClient.Get(srv.BaseURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v\nlog:\n%s", path, err, srv.log())
		}
		_ = resp.Body.Close()
		for header, want := range securityHeaders {
			if got := resp.Header.Get(header); got != want {
				t.Errorf("GET %s: %s = %q, want %q", path, header, got, want)
			}
		}
		csp := resp.Header.Get("Content-Security-Policy")
		for _, directive := range []string{"default-src 'self'", "frame-ancestors 'none'", "form-action 'self'"} {
			if !strings.Contains(csp, directive) {
				t.Errorf("GET %s: Content-Security-Policy %q omits %q", path, csp, directive)
			}
		}
	}
}
