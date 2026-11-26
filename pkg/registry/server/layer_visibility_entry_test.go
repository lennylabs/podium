package server_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// entryLayerServer serves a layer endpoint over a SQLite store, the backend
// that joins a layer's entries on a line feed, and seeds the tenant t.
// customize installs the deployment condition a case varies.
func entryLayerServer(t *testing.T, customize func(*server.LayerEndpoint) *server.LayerEndpoint) (string, store.Store) {
	t.Helper()
	st, _ := openSQLiteForCredential(t)
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	if customize != nil {
		endpoint = customize(endpoint)
	}
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)
	return ts.URL, st
}

// assertEntryRefusal asserts the §7.3.1 visibility entries refusal: 400
// registry.invalid_argument with a message naming the member and position.
func assertEntryRefusal(t *testing.T, resp *http.Response, body []byte, name string) {
	t.Helper()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	env := refusalEnvelope(t, body)
	if env.Code != "registry.invalid_argument" || !strings.HasPrefix(env.Message, name+" ") {
		t.Fatalf("envelope = %+v, want registry.invalid_argument naming %s", env, name)
	}
}

// putRaw sends body verbatim, so a case can carry duplicate keys that a
// marshalled map cannot.
func putRaw(t *testing.T, base, path, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, out
}

// visibilityView renders the visibility fields of a stored layer for
// comparison.
func visibilityView(c store.LayerConfig) string {
	return fmt.Sprintf("owner=%q public=%t organization=%t groups=%q users=%q secret=%t",
		c.Owner, c.Public, c.Organization, c.Groups, c.Users, c.WebhookSecret != "")
}

// Spec: §4.6 — a groups or users entry is a non-empty string with no control
// character. Spec: §7.3.1 (Visibility entries) — a registration carrying
// another entry, or an owner holding a control character, is refused with
// 400 registry.invalid_argument naming the member and position, and stores
// nothing, so an entry "alice@acme.com\nbob@acme.com" cannot reach the SQLite
// store and read back as two entries that widen the layer to bob.
// Matrix: §6.10 (registry.invalid_argument)
func TestLayerEndpoint_RegisterRefusesVisibilityEntries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"line feed inside a users entry", map[string]any{"users": []string{"alice@acme.com\nbob@acme.com"}}, "users[0]"},
		{"trailing line feed", map[string]any{"users": []string{"carol@acme.com", "alice@acme.com\n"}}, "users[1]"},
		{"empty users entry", map[string]any{"users": []string{"alice@acme.com", ""}}, "users[1]"},
		{"carriage return in a group", map[string]any{"groups": []string{"acme-finance\r"}}, "groups[0]"},
		{"empty group", map[string]any{"groups": []string{""}}, "groups[0]"},
		{"owner of a user-defined layer", map[string]any{"user_defined": true, "owner": "alice\nbob"}, "owner"},
		{"owner of an admin-defined layer", map[string]any{"owner": "alice\x00", "public": true}, "owner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, st := entryLayerServer(t, nil)
			body := map[string]any{"id": "team", "source_type": "git", "repo": "git@github.com:acme/team.git"}
			for k, v := range tc.body {
				body[k] = v
			}
			resp, out := mustPost(t, base, "/v1/layers", body)
			assertEntryRefusal(t, resp, out, tc.want)
			if _, err := st.GetLayerConfig(context.Background(), "t", "team"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("GetLayerConfig after the refusal: err = %v, want ErrNotFound", err)
			}
		})
	}

	// The refusal runs with the checks on the request's own fields, ahead of
	// the layer write authorization rule: a caller the admin arm refuses
	// gets the entry refusal and no store read.
	t.Run("evaluated before the admin-only fields rule", func(t *testing.T) {
		t.Parallel()
		base, _ := entryLayerServer(t, func(e *server.LayerEndpoint) *server.LayerEndpoint {
			return e.WithAdminAuth(func(*http.Request) error { return errors.New("not an admin") }).
				WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
					return layer.Identity{Sub: "mallory", IsAuthenticated: true}, nil
				})
		})
		resp, out := mustPost(t, base, "/v1/layers", map[string]any{
			"id": "team", "source_type": "git", "repo": "git@github.com:acme/team.git",
			"users": []string{"mallory\nalice"},
		})
		assertEntryRefusal(t, resp, out, "users[0]")
	})

	// Entries the rule admits are stored and read back as written, white
	// space included, through the SQLite join.
	t.Run("admitted entries round-trip exactly", func(t *testing.T) {
		t.Parallel()
		base, st := entryLayerServer(t, nil)
		resp, out := mustPost(t, base, "/v1/layers", map[string]any{
			"id": "team", "source_type": "git", "repo": "git@github.com:acme/team.git",
			"groups": []string{"Finance Team", " acme-eng "},
			"users":  []string{"alice@acme.com", "zoë@acme.com"},
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", resp.StatusCode, out)
		}
		cfg, err := st.GetLayerConfig(context.Background(), "t", "team")
		if err != nil {
			t.Fatalf("GetLayerConfig: %v", err)
		}
		want := `groups=["Finance Team" " acme-eng "] users=["alice@acme.com" "zoë@acme.com"]`
		if got := fmt.Sprintf("groups=%q users=%q", cfg.Groups, cfg.Users); got != want {
			t.Fatalf("stored %s, want %s", got, want)
		}
	})
}

// Spec: §7.3.1 (Visibility entries) — an update carrying an entry §4.6 does
// not admit is refused with 400 registry.invalid_argument, and the refused
// request applies no other field it carries and rotates no webhook secret.
// Against a user-defined layer, the immutable visibility rule is evaluated
// first and keeps its own envelope. A null or empty list carries no entry.
// Matrix: §6.10 (registry.invalid_argument)
func TestLayerEndpoint_UpdateRefusesVisibilityEntries(t *testing.T) {
	t.Parallel()

	// adminLayer registers an admin-defined git layer and returns its stored
	// record.
	adminLayer := func(t *testing.T) (string, store.Store, store.LayerConfig) {
		t.Helper()
		base, st := entryLayerServer(t, nil)
		resp, out := mustPost(t, base, "/v1/layers", map[string]any{
			"id": "team", "source_type": "git", "repo": "git@github.com:acme/team.git",
			"owner": "platform", "groups": []string{"acme-eng"}, "users": []string{"alice@acme.com"},
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("register status = %d: %s", resp.StatusCode, out)
		}
		cfg, err := st.GetLayerConfig(context.Background(), "t", "team")
		if err != nil {
			t.Fatalf("GetLayerConfig: %v", err)
		}
		return base, st, cfg
	}

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"line feed widening users", map[string]any{"users": []string{"alice@acme.com\nbob@acme.com"}}, "users[0]"},
		{"empty users entry", map[string]any{"users": []string{""}}, "users[0]"},
		{"tab in a group", map[string]any{"groups": []string{"acme-eng", "acme\tops"}}, "groups[1]"},
		{"owner with a line feed", map[string]any{"owner": "platform\n"}, "owner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, st, before := adminLayer(t)
			body := map[string]any{"public": true, "rotate_webhook_secret": true}
			for k, v := range tc.body {
				body[k] = v
			}
			resp, out := putJSON(t, base, "/v1/layers/update?id=team", body)
			assertEntryRefusal(t, resp, out, tc.want)
			after, err := st.GetLayerConfig(context.Background(), "t", "team")
			if err != nil {
				t.Fatalf("GetLayerConfig: %v", err)
			}
			if visibilityView(after) != visibilityView(before) || after.WebhookSecret != before.WebhookSecret {
				t.Fatalf("a refused update changed the layer:\nafter:  %s\nbefore: %s", visibilityView(after), visibilityView(before))
			}
		})
	}

	t.Run("null and empty lists carry no entry", func(t *testing.T) {
		t.Parallel()
		base, st, _ := adminLayer(t)
		resp, out := putJSON(t, base, "/v1/layers/update?id=team", map[string]any{
			"groups": nil, "users": []string{}, "organization": true,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", resp.StatusCode, out)
		}
		after, err := st.GetLayerConfig(context.Background(), "t", "team")
		if err != nil {
			t.Fatalf("GetLayerConfig: %v", err)
		}
		if after.Groups != nil || after.Users != nil || !after.Organization {
			t.Fatalf("stored %s, want both lists withdrawn and organization set", visibilityView(after))
		}
	})

	// The JSON decode into the flat request matches keys case-insensitively
	// and keeps the last duplicate, while the update applies the list read by
	// exact key. The refusal reads the applied list, so a case-variant
	// duplicate key carrying a clean list cannot carry a refused one past it.
	caseVariants := []struct {
		name, body, want string
	}{
		{"users tab behind a case-variant key", `{"users":["alice\tbob",""],"Users":["ok"]}`, "users[0]"},
		{"users line feed behind a case-variant key", `{"users":["alice@acme.com\nbob@acme.com"],"USERS":["ok"]}`, "users[0]"},
		{"groups carriage return behind a case-variant key", `{"groups":["acme\r",""],"Groups":["ok"]}`, "groups[0]"},
		{"groups line feed behind a case-variant key", `{"groups":["acme\neng"],"Groups":["ok"]}`, "groups[0]"},
	}
	for _, tc := range caseVariants {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, st, before := adminLayer(t)
			resp, out := putRaw(t, base, "/v1/layers/update?id=team", tc.body)
			assertEntryRefusal(t, resp, out, tc.want)
			after, err := st.GetLayerConfig(context.Background(), "t", "team")
			if err != nil {
				t.Fatalf("GetLayerConfig: %v", err)
			}
			if visibilityView(after) != visibilityView(before) {
				t.Fatalf("a refused update changed the layer:\nafter:  %s\nbefore: %s", visibilityView(after), visibilityView(before))
			}
		})
	}

	t.Run("immutable visibility keeps its envelope on a user-defined layer", func(t *testing.T) {
		t.Parallel()
		base, st := entryLayerServer(t, nil)
		seedUserDefinedLayer(t, st, "personal")
		resp, out := putJSON(t, base, "/v1/layers/update?id=personal", map[string]any{
			"users": []string{"alice\nbob"},
		})
		assertImmutableVisibilityRefusal(t, resp, out, "users")
	})
}

// Spec: §7.3.1 (Visibility entries) — an owner or users entry the registry
// resolves from the caller's identity is checked on the same terms as the
// request's own: a user-defined registration stores the caller's subject as
// owner and as its users entry, and an admin-defined one under
// PODIUM_DEFAULT_LAYER_VISIBILITY=users stores it as its users entry. A
// trusted-headers subject can carry a tab or a byte at or above 0x80 and an
// OIDC sub is any JSON string, so each such subject is refused with 400
// registry.invalid_argument and nothing is stored; a line feed is refused
// with 400 rather than reaching the store's backstop as a 500.
// Matrix: §6.10 (registry.invalid_argument)
func TestLayerEndpoint_RegisterRefusesIdentityDerivedEntries(t *testing.T) {
	t.Parallel()
	subjects := []struct{ name, sub string }{
		{"tab", "alice\tbob"},
		{"C1 next line", "alice\u0085bob"},
		{"line feed", "alice\nbob"},
	}
	asCaller := func(sub string) func(*http.Request) (layer.Identity, error) {
		return func(*http.Request) (layer.Identity, error) {
			return layer.Identity{Sub: sub, IsAuthenticated: true}, nil
		}
	}
	arms := []struct {
		name      string
		customize func(sub string) func(*server.LayerEndpoint) *server.LayerEndpoint
		want      string
	}{
		{"user-defined layer", func(sub string) func(*server.LayerEndpoint) *server.LayerEndpoint {
			return func(e *server.LayerEndpoint) *server.LayerEndpoint {
				return e.WithAdminAuth(func(*http.Request) error { return errors.New("not an admin") }).
					WithIdentityResolver(asCaller(sub))
			}
		}, "owner "},
		{"default visibility users", func(sub string) func(*server.LayerEndpoint) *server.LayerEndpoint {
			return func(e *server.LayerEndpoint) *server.LayerEndpoint {
				return e.WithDefaultVisibility("users").WithIdentityResolver(asCaller(sub))
			}
		}, "users[0] "},
	}
	for _, arm := range arms {
		for _, s := range subjects {
			t.Run(arm.name+"/"+s.name, func(t *testing.T) {
				t.Parallel()
				base, st := entryLayerServer(t, arm.customize(s.sub))
				resp, out := mustPost(t, base, "/v1/layers", map[string]any{
					"id": "team", "source_type": "git", "repo": "git@github.com:acme/team.git",
				})
				if resp.StatusCode != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400: %s", resp.StatusCode, out)
				}
				env := refusalEnvelope(t, out)
				wantMsg := "the registrant's identity cannot be stored as a visibility entry: " + arm.want
				if env.Code != "registry.invalid_argument" || !strings.HasPrefix(env.Message, wantMsg) {
					t.Fatalf("envelope = %+v, want registry.invalid_argument starting %q", env, wantMsg)
				}
				if _, err := st.GetLayerConfig(context.Background(), "t", "team"); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("GetLayerConfig after the refusal: err = %v, want ErrNotFound", err)
				}
			})
		}
	}
}
