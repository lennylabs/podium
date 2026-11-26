package serverboot

// Integration coverage for the §4.6 evaluator's treatment of an empty `users`
// or `groups` member: a real core.Registry behind a real meta-tool server, with
// the production oidcJWTVerifier and trustedHeadersVerifier wired via
// server.WithIdentityVerifier, driven over HTTP. An anonymous caller has an
// empty subject, so an evaluator that compared list entries without checking
// authentication served such a layer to every request carrying no credential.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// emptyEntryServer stands up a registry whose layers carry an empty `users`
// entry and a `groups` filter the directory resolves to an empty member name,
// next to a public layer and a layer granted to alice as a positive control.
// resolveGroup is the §6.3.1 directory, or nil where the provider expands no
// `groups:` filter against it.
func emptyEntryServer(t *testing.T, verify func(*http.Request) (layer.Identity, error), resolveGroup layer.GroupResolver) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	const tenant = "empty-entry"
	if err := st.CreateTenant(t.Context(), store.Tenant{ID: tenant, Name: tenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	layers := []layer.Layer{
		{ID: "pub", Precedence: 1, Visibility: layer.Visibility{Public: true}},
		{ID: "blank", Precedence: 2, Visibility: layer.Visibility{Users: []string{""}}},
		{ID: "ghosts", Precedence: 3, Visibility: layer.Visibility{Groups: []string{"ghosts"}}},
		{ID: "alice", Precedence: 4, Visibility: layer.Visibility{Users: []string{"alice@acme.com"}}},
	}
	for artifactID, layerID := range map[string]string{
		"pub/welcome":  "pub",
		"blank/secret": "blank",
		"ghost/secret": "ghosts",
		"alice/notes":  "alice",
	} {
		if err := st.PutManifest(t.Context(), storetest.Seal(t, store.ManifestRecord{
			TenantID: tenant, ArtifactID: artifactID, Version: "1.0.0",
			ContentHash: "sha256:" + artifactID, Type: "agent",
			Description: "secret " + artifactID, Layer: layerID,
			IngestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}, nil, nil)); err != nil {
			t.Fatalf("PutManifest(%s): %v", artifactID, err)
		}
	}
	reg := core.New(st, tenant, layers)
	if resolveGroup != nil {
		reg = reg.WithGroupResolver(resolveGroup)
	}
	ts := httptest.NewServer(server.New(reg, server.WithIdentityVerifier(verify)).Handler())
	t.Cleanup(ts.Close)
	return ts
}

// searchIDs runs search_artifacts and returns the artifact IDs it lists.
func searchIDs(t *testing.T, base string, headers map[string]string) []string {
	t.Helper()
	st, body := gwGet(t, base+"/v1/search_artifacts?query=secret&top_k=50", headers)
	if st != http.StatusOK {
		t.Fatalf("search_artifacts = %d, want 200\nbody: %s", st, body)
	}
	var out struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode search_artifacts: %v\nbody: %s", err, body)
	}
	ids := make([]string, 0, len(out.Results))
	for _, r := range out.Results {
		ids = append(ids, r.ID)
	}
	slices.Sort(ids)
	return ids
}

// assertLoads checks the load_artifact status for each artifact ID.
func assertLoads(t *testing.T, caller, base string, headers map[string]string, want map[string]int) {
	t.Helper()
	for id, status := range want {
		if st, body := loadArtifact(t, base, id, headers); st != status {
			t.Errorf("%s load %s = %d, want %d\nbody: %s", caller, id, st, status, body)
		}
	}
}

// Spec: §4.6 — `users` entries match the caller's subject or email, and only
// `public: true` reaches an unauthenticated caller.
// Spec: §6.3.3 — under oidc-jwt a request whose token header carries no bearer
// credential is anonymous and sees public visibility only, and an
// authenticated caller whose token carries no email is matched on its subject
// alone. A layer with `users: [""]`, or a `groups` filter the §6.3.1 directory
// resolves to an empty member name, reaches neither caller.
func TestEmptyVisibilityEntry_OIDCJWTAnonymousSeesPublicOnly(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verifier := identity.NewOIDCVerifier(idp.issuer(), gwAudiences, 0)
	directory := func(group string) []string {
		if group == "ghosts" {
			return []string{""}
		}
		return nil
	}
	ts := emptyEntryServer(t, oidcJWTVerifier(verifier, "", nil, false), directory)

	assertLoads(t, "anonymous", ts.URL, nil, map[string]int{
		"pub/welcome":  http.StatusOK,
		"blank/secret": http.StatusNotFound,
		"ghost/secret": http.StatusNotFound,
		"alice/notes":  http.StatusNotFound,
	})
	if got := searchIDs(t, ts.URL, nil); !slices.Equal(got, []string{"pub/welcome"}) {
		t.Errorf("anonymous search_artifacts = %v, want [pub/welcome]", got)
	}

	// gwClaims carries no email claim, so bob's email is empty.
	bob := bearer(idp.sign(t, gwClaims(idp.issuer(), "bob", nil)))
	assertLoads(t, "bob (no email)", ts.URL, bob, map[string]int{
		"blank/secret": http.StatusNotFound,
		"ghost/secret": http.StatusNotFound,
		"alice/notes":  http.StatusNotFound,
	})

	alice := bearer(idp.sign(t, gwClaims(idp.issuer(), "alice@acme.com", nil)))
	assertLoads(t, "alice", ts.URL, alice, map[string]int{
		"alice/notes":  http.StatusOK,
		"blank/secret": http.StatusNotFound,
	})
	if got := searchIDs(t, ts.URL, alice); !slices.Equal(got, []string{"alice/notes", "pub/welcome"}) {
		t.Errorf("alice search_artifacts = %v, want [alice/notes pub/welcome]", got)
	}
}

// Spec: §4.6, §6.3.3 — under trusted-headers a request without identity
// headers is anonymous and sees public visibility only, and a caller the
// gateway names without an email is matched on its subject alone, so neither
// reaches a layer with `users: [""]`. The provider expands no `groups:` filter
// against the directory (§6.3.1), so no resolver is wired.
func TestEmptyVisibilityEntry_TrustedHeadersAnonymousSeesPublicOnly(t *testing.T) {
	t.Parallel()
	ts := emptyEntryServer(t, trustedHeadersVerifier(""), nil)

	// An email header without a subject header is still anonymous.
	emailOnly := map[string]string{identity.HeaderUserEmail: "alice@acme.com"}
	for name, headers := range map[string]map[string]string{
		"no headers":         nil,
		"email header alone": emailOnly,
	} {
		assertLoads(t, name, ts.URL, headers, map[string]int{
			"pub/welcome":  http.StatusOK,
			"blank/secret": http.StatusNotFound,
			"alice/notes":  http.StatusNotFound,
		})
		if got := searchIDs(t, ts.URL, headers); !slices.Equal(got, []string{"pub/welcome"}) {
			t.Errorf("%s search_artifacts = %v, want [pub/welcome]", name, got)
		}
	}

	bob := map[string]string{identity.HeaderUserSub: "bob"}
	assertLoads(t, "bob (no email)", ts.URL, bob, map[string]int{
		"blank/secret": http.StatusNotFound,
	})
	alice := map[string]string{identity.HeaderUserSub: "alice", identity.HeaderUserEmail: "alice@acme.com"}
	assertLoads(t, "alice", ts.URL, alice, map[string]int{
		"alice/notes":  http.StatusOK,
		"blank/secret": http.StatusNotFound,
	})
}
