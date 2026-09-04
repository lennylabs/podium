package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

func putJSON(t *testing.T, base, path string, body any) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer resp.Body.Close()
	out := new(bytes.Buffer)
	_, _ = out.ReadFrom(resp.Body)
	return resp, out.Bytes()
}

// refusalEnvelope decodes the §6.10 envelope a refusal carries, so a test can
// read its code, its message, and its details.constraint discriminator.
func refusalEnvelope(t *testing.T, body []byte) server.ErrorResponse {
	t.Helper()
	var env server.ErrorResponse
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope %q: %v", body, err)
	}
	return env
}

// assertImmutableVisibilityRefusal asserts the §7.3.1 immutable visibility
// refusal: 400 registry.invalid_argument carrying
// details.constraint: "immutable_visibility" and naming every asserted field.
func assertImmutableVisibilityRefusal(t *testing.T, resp *http.Response, body []byte, fields ...string) {
	t.Helper()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	env := refusalEnvelope(t, body)
	if env.Code != "registry.invalid_argument" {
		t.Errorf("code = %q, want registry.invalid_argument", env.Code)
	}
	if got := env.Details["constraint"]; got != "immutable_visibility" {
		t.Errorf("details.constraint = %v, want immutable_visibility", got)
	}
	for _, f := range fields {
		if !strings.Contains(env.Message, f) {
			t.Errorf("message %q does not name the asserted field %q", env.Message, f)
		}
	}
}

// seedUserDefinedLayer stores a user-defined layer owned by alice, the class
// the immutable visibility rule governs, and returns the stored record.
func seedUserDefinedLayer(t *testing.T, st store.Store, id string) store.LayerConfig {
	t.Helper()
	cfg := store.LayerConfig{
		TenantID: "t", ID: id, SourceType: "local", LocalPath: "/tmp/p",
		UserDefined: true, Owner: "alice", Users: []string{"alice"},
		Order: 10, CreatedAt: time.Now().UTC(),
	}
	if err := st.PutLayerConfig(context.Background(), cfg); err != nil {
		t.Fatalf("seed layer: %v", err)
	}
	return cfg
}

// userDefinedLayerServer serves a layer endpoint over a store holding one
// user-defined layer owned by alice. customize installs the deployment
// condition an arm varies (the caller's identity, the admin arm).
func userDefinedLayerServer(t *testing.T, customize func(*server.LayerEndpoint) *server.LayerEndpoint) (string, store.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	seedUserDefinedLayer(t, st, "personal")
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	if customize != nil {
		endpoint = customize(endpoint)
	}
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)
	return ts.URL, st
}

// Spec: §4.6 — a user-defined layer's owner and its implicit users:[owner]
// visibility are set automatically at registration and cannot be widened.
// Spec: §7.3.1 — the immutable visibility rule: an update asserting owner,
// public, organization, groups, or users against a stored user-defined layer
// is refused rather than discarded and answered 200, the refusal rejects the
// whole request, and it reads the stored layer's class rather than the caller.
// Matrix: §6.10 (registry.invalid_argument)
func TestLayerEndpoint_UpdateCannotWidenUserDefined(t *testing.T) {
	t.Parallel()

	// Each field alone is refused, named in the message.
	t.Run("each field refused", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			field string
			patch map[string]any
		}{
			{"groups", map[string]any{"groups": []string{"acme-eng"}}},
			{"organization", map[string]any{"organization": true}},
			{"owner", map[string]any{"owner": "bob"}},
			{"public", map[string]any{"public": true}},
			{"users", map[string]any{"users": []string{"bob"}}},
		}
		for _, tc := range cases {
			t.Run(tc.field, func(t *testing.T) {
				t.Parallel()
				base, st := userDefinedLayerServer(t, nil)
				before, _ := st.GetLayerConfig(context.Background(), "t", "personal")
				resp, body := putJSON(t, base, "/v1/layers/update?id=personal", tc.patch)
				assertImmutableVisibilityRefusal(t, resp, body, tc.field)
				after, _ := st.GetLayerConfig(context.Background(), "t", "personal")
				if !reflect.DeepEqual(before, after) {
					t.Errorf("stored record changed on a refused patch:\nbefore %+v\nafter  %+v", before, after)
				}
			})
		}
	})

	// All five are named in sorted order.
	t.Run("every field named in sorted order", func(t *testing.T) {
		t.Parallel()
		base, _ := userDefinedLayerServer(t, nil)
		resp, body := putJSON(t, base, "/v1/layers/update?id=personal", map[string]any{
			"public": true, "organization": true, "groups": []string{"acme-eng"},
			"users": []string{"bob"}, "owner": "bob",
		})
		assertImmutableVisibilityRefusal(t, resp, body)
		if msg := refusalEnvelope(t, body).Message; !strings.Contains(msg, "groups, organization, owner, public, users") {
			t.Errorf("message %q does not name the fields in sorted order", msg)
		}
	})

	// The refusal rejects the whole request, so a ref carried beside the
	// assertion is not applied either.
	t.Run("a refused patch carrying ref applies nothing", func(t *testing.T) {
		t.Parallel()
		base, st := userDefinedLayerServer(t, nil)
		before, _ := st.GetLayerConfig(context.Background(), "t", "personal")
		resp, body := putJSON(t, base, "/v1/layers/update?id=personal", map[string]any{
			"public": true, "ref": "release",
		})
		assertImmutableVisibilityRefusal(t, resp, body, "public")
		after, _ := st.GetLayerConfig(context.Background(), "t", "personal")
		if !reflect.DeepEqual(before, after) {
			t.Errorf("stored record is not byte-identical after a refused patch:\nbefore %+v\nafter  %+v", before, after)
		}
	})

	// The rule is evaluated above the rotation, so a refused patch mints no
	// webhook secret. Before this rule the same body minted one, discarded
	// the widening, and answered 200.
	t.Run("a refused rotation mints no secret", func(t *testing.T) {
		t.Parallel()
		st := store.NewMemory()
		if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
			t.Fatalf("CreateTenant: %v", err)
		}
		if err := st.PutLayerConfig(context.Background(), store.LayerConfig{
			TenantID: "t", ID: "personal", SourceType: "git", Repo: "git@example/p.git",
			Ref: "main", UserDefined: true, Owner: "alice", Users: []string{"alice"},
			WebhookSecret: "old-secret", CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		ts := httptest.NewServer(server.NewLayerEndpoint(st, "t", server.NewModeTracker()).Handler())
		t.Cleanup(ts.Close)

		resp, body := putJSON(t, ts.URL, "/v1/layers/update?id=personal", map[string]any{
			"rotate_webhook_secret": true, "groups": []string{"acme-eng"},
		})
		assertImmutableVisibilityRefusal(t, resp, body, "groups")
		if strings.Contains(string(body), "webhook_secret") {
			t.Errorf("refusal carried a webhook_secret: %s", body)
		}
		after, _ := st.GetLayerConfig(context.Background(), "t", "personal")
		if after.WebhookSecret != "old-secret" {
			t.Errorf("stored secret = %q, want old-secret (unrotated)", after.WebhookSecret)
		}
	})

	// The refusal is evaluated above the rotation, so the same body against a
	// local layer carries the immutable visibility envelope rather than the
	// rotation's own refusal. This is the arm that reads the ordering: on a
	// git layer the rotation writes nothing a refused request can expose.
	t.Run("the refusal precedes the rotation's own refusal", func(t *testing.T) {
		t.Parallel()
		base, _ := userDefinedLayerServer(t, nil)
		resp, body := putJSON(t, base, "/v1/layers/update?id=personal", map[string]any{
			"rotate_webhook_secret": true, "public": true,
		})
		assertImmutableVisibilityRefusal(t, resp, body, "public")
	})

	// A refused request writes no record and emits no §8.1 event.
	t.Run("a refused patch emits no audit event", func(t *testing.T) {
		t.Parallel()
		sink := newAuditSink(t)
		base := layerAuditHarness(t, sink, layer.Identity{Sub: "alice", IsAuthenticated: true})
		resp, body := mustPost(t, base, "/v1/layers", map[string]any{
			"id": "personal", "source_type": "local", "local_path": "/tmp/p", "user_defined": true,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("register status = %d: %s", resp.StatusCode, body)
		}
		beforeLog := readAuditLog(t, sink)
		upResp, upBody := putJSON(t, base, "/v1/layers/update?id=personal", map[string]any{"public": true})
		assertImmutableVisibilityRefusal(t, upResp, upBody, "public")
		if got := readAuditLog(t, sink); got != beforeLog {
			t.Errorf("a refused update emitted an audit event:\nbefore:\n%s\nafter:\n%s", beforeLog, got)
		}
	})

	// The rule reads the stored class rather than the caller, so a caller
	// the admin arm admits is refused on the same terms.
	t.Run("a tenant admin is refused identically", func(t *testing.T) {
		t.Parallel()
		base, _ := userDefinedLayerServer(t, func(e *server.LayerEndpoint) *server.LayerEndpoint {
			return e.WithAdminAuth(func(*http.Request) error { return nil }).
				WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
					return layer.Identity{Sub: "carol", IsAuthenticated: true}, nil
				})
		})
		resp, body := putJSON(t, base, "/v1/layers/update?id=personal", map[string]any{"public": true})
		assertImmutableVisibilityRefusal(t, resp, body, "public")
	})

	// A registry with no identity provider and one in public mode refuse on
	// the same terms, which is what separates this rule from the layer
	// write, local-source, and admin-only registration fields rules, each of
	// which admits every caller there.
	t.Run("no identity provider and public mode refuse identically", func(t *testing.T) {
		t.Parallel()
		noIdP, _ := userDefinedLayerServer(t, nil)
		resp, body := putJSON(t, noIdP, "/v1/layers/update?id=personal", map[string]any{"public": true})
		assertImmutableVisibilityRefusal(t, resp, body, "public")

		publicMode, _ := userDefinedLayerServer(t, func(e *server.LayerEndpoint) *server.LayerEndpoint {
			return e.WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
				return layer.Identity{IsPublic: true}, nil
			})
		})
		pResp, pBody := putJSON(t, publicMode, "/v1/layers/update?id=personal", map[string]any{"public": true})
		assertImmutableVisibilityRefusal(t, pResp, pBody, "public")
	})

	// A field is asserted by a value that differs from what the layer
	// stores, so a zero value and a value restating the stored one assert
	// nothing and the patch's other fields apply.
	t.Run("the zero-value and restating bodies are admitted", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name  string
			patch map[string]any
		}{
			{"public false", map[string]any{"public": false}},
			{"organization false", map[string]any{"organization": false}},
			{"empty groups", map[string]any{"groups": []string{}}},
			{"empty users", map[string]any{"users": []string{}}},
			{"empty owner", map[string]any{"owner": ""}},
			{"owner restating the stored owner", map[string]any{"owner": "alice"}},
			{"users restating the stored users", map[string]any{"users": []string{"alice"}}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				base, st := userDefinedLayerServer(t, nil)
				before, _ := st.GetLayerConfig(context.Background(), "t", "personal")
				patch := map[string]any{"ref": "release"}
				for k, v := range tc.patch {
					patch[k] = v
				}
				resp, body := putJSON(t, base, "/v1/layers/update?id=personal", patch)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
				}
				after, _ := st.GetLayerConfig(context.Background(), "t", "personal")
				if after.Ref != "release" {
					t.Errorf("Ref = %q, want release applied", after.Ref)
				}
				assertVisibilityUnchanged(t, before, after)
			})
		}
	})

	// The comparison against the stored owner and users is what admits a
	// client that reads a layer object and returns it unchanged.
	t.Run("a layer object read back is admitted verbatim", func(t *testing.T) {
		t.Parallel()
		base, st := userDefinedLayerServer(t, nil)
		before, _ := st.GetLayerConfig(context.Background(), "t", "personal")

		resp, err := http.Get(base + "/v1/layers")
		if err != nil {
			t.Fatalf("GET /v1/layers: %v", err)
		}
		defer resp.Body.Close()
		var list struct {
			Layers []json.RawMessage `json:"layers"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
			t.Fatalf("decode layer list: %v", err)
		}
		if len(list.Layers) != 1 {
			t.Fatalf("layers = %d, want 1", len(list.Layers))
		}
		upResp, upBody := putJSON(t, base, "/v1/layers/update?id=personal", list.Layers[0])
		if upResp.StatusCode != http.StatusOK {
			t.Fatalf("replaying the layer object: status = %d, want 200: %s", upResp.StatusCode, upBody)
		}
		after, _ := st.GetLayerConfig(context.Background(), "t", "personal")
		assertVisibilityUnchanged(t, before, after)
	})

	// The comparison is byte for byte. An owner differing from the stored
	// one only by surrounding whitespace asserts, because the application
	// block stores the value the patch carries and cfg.Owner bounds the
	// write authorization and the per-identity layer cap.
	t.Run("a whitespace-padded owner is refused", func(t *testing.T) {
		t.Parallel()
		base, st := userDefinedLayerServer(t, nil)
		resp, body := putJSON(t, base, "/v1/layers/update?id=personal", map[string]any{"owner": " alice "})
		assertImmutableVisibilityRefusal(t, resp, body, "owner")
		after, _ := st.GetLayerConfig(context.Background(), "t", "personal")
		if after.Owner != "alice" {
			t.Errorf("stored Owner = %q, want alice unchanged", after.Owner)
		}
	})

	// The rule runs after the layer write authorization rule, so a caller
	// neither arm admits keeps that envelope.
	t.Run("a non-owner non-admin keeps auth.forbidden", func(t *testing.T) {
		t.Parallel()
		base, _ := userDefinedLayerServer(t, func(e *server.LayerEndpoint) *server.LayerEndpoint {
			return e.WithAdminAuth(func(*http.Request) error { return server.ErrAdminRequired }).
				WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
					return layer.Identity{Sub: "bob", IsAuthenticated: true}, nil
				})
		})
		resp, body := putJSON(t, base, "/v1/layers/update?id=personal", map[string]any{"public": true})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", resp.StatusCode, body)
		}
		env := refusalEnvelope(t, body)
		if env.Code != "auth.forbidden" {
			t.Errorf("code = %q, want auth.forbidden", env.Code)
		}
		if _, ok := env.Details["constraint"]; ok {
			t.Errorf("details carried a constraint on the write-rule refusal: %v", env.Details)
		}
	})

	// The rule runs after the local-source authorization rule, so a patch on
	// both arms keeps that envelope.
	t.Run("a non-admin local_path patch keeps local_source", func(t *testing.T) {
		t.Parallel()
		base, _ := userDefinedLayerServer(t, func(e *server.LayerEndpoint) *server.LayerEndpoint {
			return e.WithAdminAuth(func(*http.Request) error { return server.ErrAdminRequired }).
				WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
					return layer.Identity{Sub: "alice", IsAuthenticated: true}, nil
				})
		})
		resp, body := putJSON(t, base, "/v1/layers/update?id=personal", map[string]any{
			"local_path": "/etc", "public": true,
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", resp.StatusCode, body)
		}
		env := refusalEnvelope(t, body)
		if env.Code != "auth.forbidden" {
			t.Errorf("code = %q, want auth.forbidden", env.Code)
		}
		if got := env.Details["constraint"]; got != "local_source" {
			t.Errorf("details.constraint = %v, want local_source", got)
		}
	})

	// On a stored admin-defined layer the rule does not reach, and every
	// field still applies.
	t.Run("every field applies on an admin-defined layer", func(t *testing.T) {
		t.Parallel()
		base, st, cleanup := newLayerHarness(t)
		defer cleanup()
		mustPost(t, base, "/v1/layers", map[string]any{
			"id": "team", "source_type": "local", "local_path": "/tmp/team",
		})
		resp, body := putJSON(t, base, "/v1/layers/update?id=team", map[string]any{
			"owner": "bob", "public": true, "organization": true,
			"groups": []string{"acme-eng"}, "users": []string{"bob"},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
		}
		cfg, err := st.GetLayerConfig(context.Background(), "t", "team")
		if err != nil {
			t.Fatalf("GetLayerConfig: %v", err)
		}
		if cfg.Owner != "bob" || !cfg.Public || !cfg.Organization ||
			!reflect.DeepEqual(cfg.Groups, []string{"acme-eng"}) ||
			!reflect.DeepEqual(cfg.Users, []string{"bob"}) {
			t.Errorf("admin-defined layer did not take the patch: %+v", cfg)
		}
	})
}

// assertVisibilityUnchanged pins that an admitted patch left the §4.6 owner
// and visibility axes byte-identical, which is what falsifies an application
// block storing a value that is not what the layer already held.
func assertVisibilityUnchanged(t *testing.T, before, after store.LayerConfig) {
	t.Helper()
	if after.Owner != before.Owner {
		t.Errorf("Owner = %q, want %q unchanged", after.Owner, before.Owner)
	}
	if after.Public != before.Public || after.Organization != before.Organization {
		t.Errorf("public/organization = %v/%v, want %v/%v unchanged",
			after.Public, after.Organization, before.Public, before.Organization)
	}
	if !reflect.DeepEqual(after.Groups, before.Groups) {
		t.Errorf("Groups = %v, want %v unchanged", after.Groups, before.Groups)
	}
	if !reflect.DeepEqual(after.Users, before.Users) {
		t.Errorf("Users = %v, want %v unchanged", after.Users, before.Users)
	}
}

// Spec: §4.6 — the owner of a user-defined layer is the authenticated
// registrant; a caller cannot register a layer owned by an arbitrary
// subject. The identity-derived owner overrides the request body.
func TestLayerEndpoint_UserDefinedOwnerFromIdentity(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
			return layer.Identity{Sub: "alice", IsAuthenticated: true}, nil
		})
	ts := httptest.NewServer(endpoint.Handler())
	defer ts.Close()

	resp, body := mustPost(t, ts.URL, "/v1/layers", map[string]any{
		"id": "personal", "source_type": "local", "local_path": "/tmp/p",
		"user_defined": true, "owner": "bob", // attempt to spoof another owner
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var got server.LayerRegisterResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Layer.Owner != "alice" {
		t.Errorf("Owner = %q, want alice (from identity, not the body's bob)", got.Layer.Owner)
	}
	if len(got.Layer.Users) != 1 || got.Layer.Users[0] != "alice" {
		t.Errorf("Users = %v, want [alice]", got.Layer.Users)
	}
}

// Spec: §7.3.1 — the layer-write authorization rule. Mutating an
// admin-defined layer is authorized to a tenant admin alone. A user-defined
// layer is authorized to its stored owner or to a tenant admin, so a caller
// whom the admin arm denies and who resolves no subject is refused as well.
func TestLayerEndpoint_UpdateAdminGating(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	// Seed both an admin-defined and a user-defined layer with a no-op
	// admin authorizer so setup is unobstructed.
	seed := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	seedTS := httptest.NewServer(seed.Handler())
	mustPost(t, seedTS.URL, "/v1/layers", map[string]any{"id": "team", "source_type": "local", "local_path": "/x"})
	mustPost(t, seedTS.URL, "/v1/layers", map[string]any{
		"id": "personal", "source_type": "local", "local_path": "/p", "user_defined": true, "owner": "alice",
	})
	seedTS.Close()

	// A second endpoint that denies admin authorization.
	denied := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAdminAuth(func(*http.Request) error { return server.ErrAdminRequired })
	ts := httptest.NewServer(denied.Handler())
	defer ts.Close()

	// Admin-defined layer update is rejected without admin auth.
	resp, _ := putJSON(t, ts.URL, "/v1/layers/update?id=team", map[string]any{"ref": "release"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("admin-defined update status = %d, want 403", resp.StatusCode)
	}
	// The user-defined layer is owned by alice; this endpoint's default
	// identity resolver resolves no subject, so the owner arm fails and the
	// denying admin arm refuses the update.
	resp2, body2 := putJSON(t, ts.URL, "/v1/layers/update?id=personal", map[string]any{"local_path": "/p2"})
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("user-defined update status = %d, want 403: %s", resp2.StatusCode, body2)
	}
	if code := errCode(t, body2); code != "auth.forbidden" {
		t.Errorf("user-defined update code = %q, want auth.forbidden", code)
	}
}
