package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// The credential fixtures are multi-character on purpose: a single-letter
// username or password matches unrelated response text.
const (
	credRepoFull     = "https://alice-user:s3cr3tpw@git.acme.com/acme/private.git"
	credRepoReported = "https://git.acme.com/acme/private.git"
)

// assertNoRepoCredential fails when a response body carries either part of
// the fixture credential.
func assertNoRepoCredential(t *testing.T, what string, body []byte) {
	t.Helper()
	for _, part := range []string{"alice-user", "s3cr3tpw"} {
		if strings.Contains(string(body), part) {
			t.Errorf("%s carries the credential part %q: %s", what, part, body)
		}
	}
}

// assertStoredRepo fails when the stored row of a live layer does not hold
// want, which pins that a response redacted a copy and wrote nothing back.
func assertStoredRepo(t *testing.T, st store.Store, id, want string) {
	t.Helper()
	cfg, err := st.GetLayerConfig(context.Background(), "t", id)
	if err != nil {
		t.Fatalf("GetLayerConfig %s: %v", id, err)
	}
	if cfg.Repo != want {
		t.Errorf("stored Repo of %s = %q, want %q", id, cfg.Repo, want)
	}
}

// decodeRegisterResponse decodes a register or update response body.
func decodeRegisterResponse(t *testing.T, body []byte) (repo, webhookURL, webhookSecret string) {
	t.Helper()
	var out struct {
		Layer         store.LayerConfig `json:"layer"`
		WebhookURL    string            `json:"webhook_url"`
		WebhookSecret string            `json:"webhook_secret"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out.Layer.Repo, out.WebhookURL, out.WebhookSecret
}

// listedRepos returns each layer's repo by ID from a list or reorder body.
func listedRepos(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var out struct {
		Layers []store.LayerConfig `json:"layers"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	repos := make(map[string]string, len(out.Layers))
	for _, l := range out.Layers {
		repos[l.ID] = l.Repo
	}
	return repos
}

// registerCredLayer registers a git layer with the given repo and returns the
// response body.
func registerCredLayer(t *testing.T, base, id, repo string) []byte {
	t.Helper()
	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": id, "source_type": "git", "repo": repo, "ref": "main",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register %s: status %d, body=%s", id, resp.StatusCode, body)
	}
	return body
}

// Spec: §7.3.1 — a registration stores the repo with its URL userinfo and
// reports it with the userinfo removed. The response still carries the
// webhook URL and the secret, which read the unredacted config.
func TestLayerRepoCredential_RegisterResponse(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	body := registerCredLayer(t, base, "private", credRepoFull)
	assertNoRepoCredential(t, "register response", body)
	repo, hookURL, hookSecret := decodeRegisterResponse(t, body)
	if repo != credRepoReported {
		t.Errorf("layer.repo = %q, want %q", repo, credRepoReported)
	}
	if hookURL != "/v1/ingest/webhook/private" {
		t.Errorf("webhook_url = %q, want /v1/ingest/webhook/private", hookURL)
	}
	stored, err := st.GetLayerConfig(context.Background(), "t", "private")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if hookSecret == "" || hookSecret != stored.WebhookSecret {
		t.Errorf("webhook_secret = %q, want the stored secret %q", hookSecret, stored.WebhookSecret)
	}
	if stored.Repo != credRepoFull {
		t.Errorf("stored Repo = %q, want %q", stored.Repo, credRepoFull)
	}
}

// Spec: §7.3.1 — an update, and an update that stores nothing, report the
// repo with the userinfo removed and leave the stored repo unchanged.
func TestLayerRepoCredential_UpdateResponse(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()
	registerCredLayer(t, base, "private", credRepoFull)

	for _, name := range []string{"update", "update no-op"} {
		resp, body := mustPut(t, base, "/v1/layers/update?id=private", map[string]any{"ref": "develop"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, body=%s", name, resp.StatusCode, body)
		}
		assertNoRepoCredential(t, name+" response", body)
		if repo, _, _ := decodeRegisterResponse(t, body); repo != credRepoReported {
			t.Errorf("%s: layer.repo = %q, want %q", name, repo, credRepoReported)
		}
		assertStoredRepo(t, st, "private", credRepoFull)
	}
	stored, _ := st.GetLayerConfig(context.Background(), "t", "private")
	if stored.Ref != "develop" {
		t.Errorf("stored Ref = %q, want develop (the first update must have applied)", stored.Ref)
	}
}

// Spec: §7.3.1 — the list reports the repo with the userinfo removed on the
// live arm and on ?deleted=true, to a tenant admin and to the layer's owner,
// and neither read changes the stored repo.
func TestLayerRepoCredential_ListResponse(t *testing.T) {
	t.Parallel()
	owner := layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true}
	cases := []struct {
		name    string
		harness func(*testing.T) (string, store.Store, func())
	}{
		{"admin arm", newLayerHarness},
		{"owner on the visibility arm", func(t *testing.T) (string, store.Store, func()) {
			return newClassHarness(t, owner)
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, st, cleanup := tc.harness(t)
			defer cleanup()
			registerCredLayer(t, base, "private", credRepoFull)

			live := mustGet(t, base, "/v1/layers")
			assertNoRepoCredential(t, "live list", live)
			if got := listedRepos(t, live)["private"]; got != credRepoReported {
				t.Errorf("live list repo = %q, want %q", got, credRepoReported)
			}
			assertStoredRepo(t, st, "private", credRepoFull)

			if resp, body := mustDelete(t, base, "/v1/layers?id=private"); resp.StatusCode != http.StatusOK {
				t.Fatalf("unregister: status %d, body=%s", resp.StatusCode, body)
			}
			deleted := mustGet(t, base, "/v1/layers?deleted=true")
			assertNoRepoCredential(t, "deleted list", deleted)
			if got := listedRepos(t, deleted)["private"]; got != credRepoReported {
				t.Errorf("deleted list repo = %q, want %q", got, credRepoReported)
			}
			tombstones, err := st.ListDeletedLayerConfigs(context.Background(), "t")
			if err != nil {
				t.Fatalf("ListDeletedLayerConfigs: %v", err)
			}
			if len(tombstones) != 1 || tombstones[0].Repo != credRepoFull {
				t.Errorf("stored tombstones = %+v, want one row with Repo %q", tombstones, credRepoFull)
			}
		})
	}
}

// Spec: §7.3.1 — the reorder response reports every element's repo with the
// userinfo removed, and the reorder write stores each repo unchanged.
func TestLayerRepoCredential_ReorderResponse(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()
	const otherFull = "https://alice-user:s3cr3tpw@git.acme.com/acme/other.git"
	registerCredLayer(t, base, "private", credRepoFull)
	registerCredLayer(t, base, "other", otherFull)

	resp, body := mustPost(t, base, "/v1/layers/reorder", map[string]any{
		"order": []string{"other", "private"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reorder: status %d, body=%s", resp.StatusCode, body)
	}
	assertNoRepoCredential(t, "reorder response", body)
	want := map[string]string{
		"private": credRepoReported,
		"other":   "https://git.acme.com/acme/other.git",
	}
	got := listedRepos(t, body)
	if len(got) != len(want) {
		t.Fatalf("reorder response holds %d layers, want %d: %s", len(got), len(want), body)
	}
	for id, repo := range want {
		if got[id] != repo {
			t.Errorf("reorder repo of %s = %q, want %q", id, got[id], repo)
		}
	}
	assertStoredRepo(t, st, "private", credRepoFull)
	assertStoredRepo(t, st, "other", otherFull)
}

// Spec: §7.3.1 — one userinfo rule covers every URL scheme, and a repo whose
// credential-bearing part cannot be identified is reported as [redacted] with
// no part of it echoed.
func TestLayerRepoCredential_SchemeAndFailClosed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, id, stored, reported string
	}{
		{"ssh userinfo is removed whole", "over-ssh", "ssh://git:s3cr3tpw@host/x.git", "ssh://host/x.git"},
		// url.Parse rejects the non-numeric port, so the userinfo cannot be
		// located and the whole value is withheld.
		{"unparseable URL is withheld", "unparseable",
			"https://alice-user:s3cr3tpw@git.acme.com:port/x.git", "[redacted]"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, st, cleanup := newLayerHarness(t)
			defer cleanup()

			body := registerCredLayer(t, base, tc.id, tc.stored)
			assertNoRepoCredential(t, "register response", body)
			if repo, _, _ := decodeRegisterResponse(t, body); repo != tc.reported {
				t.Errorf("register layer.repo = %q, want %q", repo, tc.reported)
			}
			list := mustGet(t, base, "/v1/layers")
			assertNoRepoCredential(t, "list response", list)
			if got := listedRepos(t, list)[tc.id]; got != tc.reported {
				t.Errorf("list repo = %q, want %q", got, tc.reported)
			}
			assertStoredRepo(t, st, tc.id, tc.stored)
		})
	}
}

// guardCaller is the answer the guard harness gives on the admin and identity
// seams for one request.
type guardCaller struct {
	id    layer.Identity
	admin bool
}

var (
	guardAdmin = guardCaller{id: layer.Identity{Sub: "carol@acme.com", IsAuthenticated: true}, admin: true}
	guardAlice = guardCaller{id: layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true}}
	guardBob   = guardCaller{id: layer.Identity{Sub: "bob@acme.com", IsAuthenticated: true}}
)

// guardHarness serves the layer endpoint over a memory store with a recording
// audit sink. The caller is switchable between requests, so one subtest can
// read the reported repo as a caller who sees the layer and then register as
// another.
type guardHarness struct {
	base string
	st   store.Store
	sink *audit.Memory

	// mu guards who, which the handler goroutines read on every request.
	mu  sync.Mutex
	who guardCaller
}

func newGuardHarness(t *testing.T, who guardCaller) *guardHarness {
	t.Helper()
	h := &guardHarness{st: store.NewMemory(), sink: audit.NewMemory(), who: who}
	if err := h.st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(h.st, "t", server.NewModeTracker()).
		WithAudit(h.sink).
		WithAdminAuth(func(*http.Request) error {
			if h.caller().admin {
				return nil
			}
			return server.ErrAdminRequired
		}).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return h.caller().id, nil })
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)
	h.base = ts.URL
	return h
}

func (h *guardHarness) caller() guardCaller {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.who
}

func (h *guardHarness) actAs(who guardCaller) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.who = who
}

// reported returns the repo the list response carries for id, which is the
// value a client that copies repo from a read would re-submit.
func (h *guardHarness) reported(t *testing.T, id string) string {
	t.Helper()
	body := mustGet(t, h.base, "/v1/layers")
	assertNoRepoCredential(t, "list response", body)
	repo, ok := listedRepos(t, body)[id]
	if !ok {
		t.Fatalf("list response does not carry layer %s: %s", id, body)
	}
	return repo
}

// register posts a git registration of id with the given repo and any extra
// members.
func (h *guardHarness) register(t *testing.T, id, repo string, extra map[string]any) (*http.Response, []byte) {
	t.Helper()
	body := map[string]any{"id": id, "source_type": "git", "repo": repo, "ref": "main"}
	for k, v := range extra {
		body[k] = v
	}
	resp, out := mustPost(t, h.base, "/v1/layers", body)
	assertNoRepoCredential(t, "register response", out)
	return resp, out
}

// stored returns the live row of id.
func (h *guardHarness) stored(t *testing.T, id string) store.LayerConfig {
	t.Helper()
	cfg, err := h.st.GetLayerConfig(context.Background(), "t", id)
	if err != nil {
		t.Fatalf("GetLayerConfig %s: %v", id, err)
	}
	return cfg
}

// assertRedactedRepoRefusal asserts the §7.3.1 re-registration refusal: 400
// registry.invalid_argument carrying details.constraint "redacted_repo", with
// a message that names force_repo_overwrite. assertNoRepoCredential on the
// body covers the rule that the message carries no part of the stored repo.
func assertRedactedRepoRefusal(t *testing.T, resp *http.Response, body []byte) {
	t.Helper()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	code, message, constraint := decodeErrorEnvelope(t, body)
	if code != "registry.invalid_argument" || constraint != "redacted_repo" {
		t.Errorf("code = %q, details.constraint = %q, want registry.invalid_argument and redacted_repo", code, constraint)
	}
	if !strings.Contains(message, "force_repo_overwrite") {
		t.Errorf("message does not name force_repo_overwrite: %q", message)
	}
	assertNoRepoCredential(t, "refusal", body)
}

// Spec: §7.3.1 — the re-registration rule. A registration under a stored
// layer's ID whose repo is the value a read reports for a credential-bearing
// stored repo is refused with 400 registry.invalid_argument carrying
// details.constraint "redacted_repo", unless it sets force_repo_overwrite.
// The refusal stores nothing, mints no webhook secret, and records no §8.1
// event. Each subtest's seeding registration is the case of an ID that names
// no stored layer.
func TestLayerRegister_RedactedRepoGuard(t *testing.T) {
	t.Parallel()

	// seed registers id as the harness's current caller and returns the
	// reported repo, asserting that the read differs from the stored value.
	seed := func(t *testing.T, h *guardHarness, id, repo string) string {
		t.Helper()
		if resp, body := h.register(t, id, repo, nil); resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed %s: status %d, body=%s", id, resp.StatusCode, body)
		}
		return h.reported(t, id)
	}

	t.Run("refuse", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAdmin)
		reported := seed(t, h, "private", credRepoFull)
		if reported != credRepoReported {
			t.Fatalf("reported repo = %q, want %q", reported, credRepoReported)
		}
		before := h.stored(t, "private")

		resp, body := h.register(t, "private", reported, nil)
		assertRedactedRepoRefusal(t, resp, body)
		// The whole row is compared, so a re-minted WebhookSecret or a reset
		// CreatedAt fails here as a changed Repo does.
		if after := h.stored(t, "private"); !reflect.DeepEqual(before, after) {
			t.Errorf("stored row changed on a refused registration:\nbefore %+v\nafter  %+v", before, after)
		}
		if before.Repo != credRepoFull || before.WebhookSecret == "" {
			t.Errorf("seeded row = %+v, want Repo %q and a minted webhook secret", before, credRepoFull)
		}
		if events := h.sink.Events(); len(events) != 1 {
			t.Errorf("audit sink holds %d events, want the seeding event alone: %+v", len(events), events)
		}
	})

	t.Run("force", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAdmin)
		reported := seed(t, h, "private", credRepoFull)

		resp, body := h.register(t, "private", reported, map[string]any{"force_repo_overwrite": true})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", resp.StatusCode, body)
		}
		assertStoredRepo(t, h.st, "private", reported)
	})

	t.Run("different remote", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAdmin)
		seed(t, h, "private", credRepoFull)
		const other = "https://git.acme.com/acme/other.git"

		resp, body := h.register(t, "private", other, nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", resp.StatusCode, body)
		}
		assertStoredRepo(t, h.st, "private", other)
	})

	t.Run("full URL", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAdmin)
		seed(t, h, "private", credRepoFull)

		resp, body := h.register(t, "private", credRepoFull, nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", resp.StatusCode, body)
		}
		assertStoredRepo(t, h.st, "private", credRepoFull)
	})

	t.Run("no stored credential", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAdmin)
		const plain = "https://git.acme.com/acme/x.git"
		reported := seed(t, h, "open", plain)
		if reported != plain {
			t.Fatalf("reported repo = %q, want the stored value %q", reported, plain)
		}

		resp, body := h.register(t, "open", reported, nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", resp.StatusCode, body)
		}
		assertStoredRepo(t, h.st, "open", plain)
	})

	t.Run("fail-closed class", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAdmin)
		// The unescaped slash moves the token out of the URL userinfo, so the
		// value is reported as [redacted] rather than with a part removed.
		const slashed = "https://ghp_tok/3n@host/x.git"
		reported := seed(t, h, "slashed", slashed)
		if reported != "[redacted]" {
			t.Fatalf("reported repo = %q, want [redacted]", reported)
		}

		resp, body := h.register(t, "slashed", reported, nil)
		assertRedactedRepoRefusal(t, resp, body)
		if strings.Contains(string(body), "ghp_tok") {
			t.Errorf("refusal carries part of the stored repo: %s", body)
		}
		assertStoredRepo(t, h.st, "slashed", slashed)
	})

	t.Run("soft-deleted ID", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAdmin)
		reported := seed(t, h, "private", credRepoFull)
		if resp, body := mustDelete(t, h.base, "/v1/layers?id=private"); resp.StatusCode != http.StatusOK {
			t.Fatalf("unregister: status %d, body=%s", resp.StatusCode, body)
		}
		recorded := len(h.sink.Events())

		resp, body := h.register(t, "private", reported, nil)
		assertRedactedRepoRefusal(t, resp, body)
		tombstones, err := h.st.ListDeletedLayerConfigs(context.Background(), "t")
		if err != nil {
			t.Fatalf("ListDeletedLayerConfigs: %v", err)
		}
		if len(tombstones) != 1 || tombstones[0].Repo != credRepoFull {
			t.Errorf("stored tombstones = %+v, want one row with Repo %q", tombstones, credRepoFull)
		}
		if _, err := h.st.GetLayerConfig(context.Background(), "t", "private"); err == nil {
			t.Errorf("a refused registration restored the soft-deleted layer")
		}
		if got := len(h.sink.Events()); got != recorded {
			t.Errorf("audit sink holds %d events after the refusal, want %d", got, recorded)
		}
	})

	t.Run("unauthorized caller", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAlice)
		// Seeded through the store, so the layer is alice's without any
		// request of hers reaching the handler under test.
		if err := h.st.PutLayerConfig(context.Background(), store.LayerConfig{
			TenantID: "t", ID: "alice-personal", SourceType: "git", Repo: credRepoFull, Ref: "main",
			UserDefined: true, Owner: "alice@acme.com", Users: []string{"alice@acme.com"},
		}); err != nil {
			t.Fatalf("PutLayerConfig: %v", err)
		}
		reported := h.reported(t, "alice-personal")

		h.actAs(guardBob)
		resp, body := h.register(t, "alice-personal", reported, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", resp.StatusCode, body)
		}
		// An empty constraint pins that the write rule answered: the guard's
		// envelope would tell bob that alice's stored repo carries a credential.
		if code, _, constraint := decodeErrorEnvelope(t, body); code != "auth.forbidden" || constraint != "" {
			t.Errorf("code = %q, details.constraint = %q, want auth.forbidden and no constraint", code, constraint)
		}
		assertStoredRepo(t, h.st, "alice-personal", credRepoFull)
	})

	t.Run("admin-only arm keeps its envelope", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAlice)
		reported := seed(t, h, "alice-personal", credRepoFull)

		resp, body := h.register(t, "alice-personal", reported, map[string]any{"public": true})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", resp.StatusCode, body)
		}
		if code, _, constraint := decodeErrorEnvelope(t, body); code != "auth.forbidden" || constraint != "admin_only_fields" {
			t.Errorf("code = %q, details.constraint = %q, want auth.forbidden and admin_only_fields", code, constraint)
		}
		assertStoredRepo(t, h.st, "alice-personal", credRepoFull)
	})

	t.Run("admin re-registration of a user-defined layer", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAlice)
		reported := seed(t, h, "alice-personal", credRepoFull)

		h.actAs(guardAdmin)
		resp, body := h.register(t, "alice-personal", reported, nil)
		assertRedactedRepoRefusal(t, resp, body)
		if cfg := h.stored(t, "alice-personal"); !cfg.UserDefined || cfg.Owner != "alice@acme.com" || cfg.Repo != credRepoFull {
			t.Errorf("stored row = %+v, want alice's user-defined layer with Repo %q", cfg, credRepoFull)
		}

		resp, body = h.register(t, "alice-personal", credRepoFull, nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("full-URL registration: status = %d, want 201: %s", resp.StatusCode, body)
		}
		if cfg := h.stored(t, "alice-personal"); cfg.UserDefined || cfg.Repo != credRepoFull {
			t.Errorf("stored row = %+v, want an admin-defined layer with Repo %q", cfg, credRepoFull)
		}
	})

	t.Run("update ignores the member", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAdmin)
		reported := seed(t, h, "private", credRepoFull)

		resp, body := mustPut(t, h.base, "/v1/layers/update?id=private", map[string]any{
			"repo": reported, "force_repo_overwrite": true,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
		}
		assertNoRepoCredential(t, "update response", body)
		assertStoredRepo(t, h.st, "private", credRepoFull)
	})
}
