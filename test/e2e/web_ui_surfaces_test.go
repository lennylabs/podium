package e2e

// End-to-end coverage of the §13.10 surfaces the served bundle drives. The
// UI is a client of the HTTP API and reads the catalog and the layer list
// through the same endpoints an SDK would, so the endpoints its own client
// code calls have to be endpoints the running binary serves. Only the served
// bundle carries what the client calls, and only the running binary answers
// it, so this is the level where the two are compared.

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// bundleAPIPathPattern matches a registry path the built bundle references.
// The bundler emits every path as a string literal, so a path the client
// calls appears verbatim in the served asset.
var bundleAPIPathPattern = regexp.MustCompile(`"(/v1/[a-z0-9_/-]*)"`)

// Spec: §13.10, §7.3.4 — every registry path the served bundle references is
// a path the running binary serves, and the bundle spells no authentication
// route path of its own: it takes the sign-in and sign-out paths from the
// posture read, which reports them only where the browser flow is enabled.
func TestWebUI_ServedBundleCallsResolve(t *testing.T) {
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
	called := bundleAPIPaths(t, srv, string(index))
	if len(called) == 0 {
		t.Fatalf("the served bundle references no registry path")
	}
	if !contains(called, "/v1/ui/session") {
		t.Errorf("the bundle references %v, want the posture read among them", called)
	}
	for _, path := range called {
		if strings.HasPrefix(path, "/v1/ui/auth/") {
			t.Errorf("the bundle spells the authentication route path %s; it reads the path from the posture read", path)
			continue
		}
		// A read the bundle drives with no arguments is refused as an
		// invalid argument rather than as an unregistered path, so the
		// assertion is that the registry serves the path at all.
		if st := getStatus(t, srv.BaseURL+path); st == 404 {
			t.Errorf("GET %s = 404; the bundle calls a path this binary does not serve\nlog:\n%s", path, srv.log())
		}
	}
}

// Spec: §13.10, §13.2.1 — the served bundle reads the read-only marker off
// the registry's responses under the name the registry sets, so the layer
// panel presents the state before a write is attempted. The header name is
// the whole contract between the two sides, and a rename on either side
// silently returns the panel to one refusal per button press.
func TestWebUI_ServedBundleReadsTheReadOnlyMarker(t *testing.T) {
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
	found := false
	for _, script := range bundleScripts(t, srv, string(index)) {
		if strings.Contains(script, readOnlyHeaderName) {
			found = true
		}
	}
	if !found {
		t.Errorf("the served bundle reads no %s header; the panel would learn the read-only state one refused write at a time", readOnlyHeaderName)
	}
}

// readOnlyHeaderName is the §13.2.1 marker the read-only middleware sets on
// every response (pkg/registry/server/server.go).
const readOnlyHeaderName = "X-Podium-Read-Only"

// bundleAPIPaths returns the registry paths the bundle's own client code
// references, read off the scripts the served index names.
func bundleAPIPaths(t *testing.T, srv *serverProc, index string) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, script := range bundleScripts(t, srv, index) {
		for _, m := range bundleAPIPathPattern.FindAllStringSubmatch(script, -1) {
			seen[m[1]] = true
		}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// bundleScripts returns the bodies of the scripts the served index names,
// which is where the bundler puts the client code the browser runs.
func bundleScripts(t *testing.T, srv *serverProc, index string) []string {
	t.Helper()
	scripts := []string{}
	for _, ref := range bundleAssetRefPattern.FindAllStringSubmatch(index, -1) {
		if !strings.HasSuffix(ref[1], ".js") {
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
		scripts = append(scripts, string(body))
	}
	return scripts
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
