package e2e

// End-to-end coverage of the §13.10 surfaces the served bundle drives. The
// UI is a client of the HTTP API and reads the catalog and the layer list
// through the same endpoints an SDK would, so the endpoints its own client
// code calls have to be endpoints the running binary serves. Only the served
// bundle carries what the client calls, and only the running binary answers
// it, so this is the level where the two are compared.

import (
	"encoding/json"
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

// panelLayerFields are the layer-record members the layer panel reads: the
// identifier it keys a row on, the class and the order that place the row in
// the composition, the owner the ownership marker compares against, and the
// staleness stamp the row renders, plus the force-push policy the update
// form edits. The registry marshals store.LayerConfig directly, so the casing
// is not uniform: a tagged member carries its tag and every other member
// carries its Go field name.
var panelLayerFields = []string{"ID", "Order", "UserDefined", "Owner", "last_ingested_at", "force_push_policy"}

// panelLayerOmitted are the panel's members whose json tag carries omitempty
// and which a layer served from a local path leaves unset, so the wire object
// carries neither. Their names are checked against the struct tag through the
// bundle alone.
var panelLayerOmitted = map[string]bool{"last_ingested_at": true, "force_push_policy": true}

// Spec: §13.10, §7.3.1 — the served bundle reads a layer record under the
// member names the registry marshals it with. The registry marshals the store
// struct directly, so a member's name on the wire is either its json tag or
// its Go field name, and the two are mixed in one object. Reading a tagged
// member under its Go field name yields undefined on every response, which
// renders as a permanently absent value rather than as a failure, so the
// correspondence is pinned against a layer the running binary returns.
func TestWebUI_ServedBundleReadsTheLayerRecordFields(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_WEB_UI=true"},
		"serve", "--standalone", "--web-ui", "--layer-path", reg)

	st, body := getRaw(t, srv.BaseURL+"/v1/layers")
	if st != 200 {
		t.Fatalf("GET /v1/layers = %d, want 200\nbody: %s\nlog:\n%s", st, body, srv.log())
	}
	var listed struct {
		Layers []map[string]json.RawMessage `json:"layers"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode the layer list: %v (%s)", err, body)
	}
	if len(listed.Layers) == 0 {
		t.Fatalf("the registry listed no layer to read the member names off\nbody: %s", body)
	}
	// The wire object is checked for the members that are always present.
	for _, field := range panelLayerFields {
		if panelLayerOmitted[field] {
			continue
		}
		if _, ok := listed.Layers[0][field]; !ok {
			t.Errorf("the layer record carries no %q member; the panel reads one\nbody: %s", field, body)
		}
	}

	st, index := getRaw(t, srv.BaseURL+"/ui/")
	if st != 200 {
		t.Fatalf("GET /ui/ status = %d, want 200\nlog:\n%s", st, srv.log())
	}
	scripts := strings.Join(bundleScripts(t, srv, string(index)), "\n")
	for _, field := range panelLayerFields {
		if !strings.Contains(scripts, `"`+field+`"`) && !strings.Contains(scripts, "."+field) {
			t.Errorf("the served bundle references no %q layer member; the panel reads it under some other name", field)
		}
	}
}

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
