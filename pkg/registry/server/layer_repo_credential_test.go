package server_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
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
// want as the remote a clone uses, which pins that a response redacted a copy
// and wrote nothing back.
func assertStoredRepo(t *testing.T, st store.Store, id, want string) {
	t.Helper()
	cfg, err := st.GetLayerConfig(context.Background(), "t", id)
	if err != nil {
		t.Fatalf("GetLayerConfig %s: %v", id, err)
	}
	if got := cfg.CloneRepo(); got != want {
		t.Errorf("stored CloneRepo of %s = %q, want %q", id, got, want)
	}
}

// assertSplitRepo fails when the stored row of a live layer does not hold
// registered as the remote a clone uses and reported as its Repo.
func assertSplitRepo(t *testing.T, st store.Store, id, registered, reported string) {
	t.Helper()
	cfg, err := st.GetLayerConfig(context.Background(), "t", id)
	if err != nil {
		t.Fatalf("GetLayerConfig %s: %v", id, err)
	}
	assertSplitConfig(t, cfg, registered, reported)
}

// assertSplitConfig fails when a config read from the store was not split
// into the registered URL, which the clone uses, and the reported Repo.
//
// Spec: §7.3.1 (Repository credentials)
func assertSplitConfig(t *testing.T, cfg store.LayerConfig, registered, reported string) {
	t.Helper()
	if got := cfg.CloneRepo(); got != registered {
		t.Errorf("stored CloneRepo of %s = %q, want %q", cfg.ID, got, registered)
	}
	if cfg.Repo != reported {
		t.Errorf("stored Repo of %s = %q, want %q", cfg.ID, cfg.Repo, reported)
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

// credMembers returns the fixture credential as the repo_username and
// repo_token members, merged with any extra members.
func credMembers(extra map[string]any) map[string]any {
	out := map[string]any{"repo_username": "alice-user", "repo_token": "s3cr3tpw"}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// registerCredLayer registers a git layer on the bare remote repo with the
// fixture credential in the repo_username and repo_token members, and returns
// the response body. It asserts the body carries no part of the fixture
// credential, so every seeding registration in this file is checked.
func registerCredLayer(t *testing.T, base, id, repo string) []byte {
	t.Helper()
	resp, body := mustPost(t, base, "/v1/layers", credMembers(map[string]any{
		"id": id, "source_type": "git", "repo": repo, "ref": "main",
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register %s: status %d, body=%s", id, resp.StatusCode, body)
	}
	assertNoRepoCredential(t, "register response of "+id, body)
	return body
}

// Spec: §7.3.1 (Repository credentials) — a registration that carries the
// credential members stores the repo with the credential as its URL userinfo
// and reports it with the userinfo removed. The response still carries the
// webhook URL and the secret, which read the unredacted config.
func TestLayerRepoCredential_RegisterResponse(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	body := registerCredLayer(t, base, "private", credRepoReported)
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
	assertSplitConfig(t, stored, credRepoFull, credRepoReported)
}

// Spec: §7.3.1 — an update, and an update that stores nothing, report the
// repo with the userinfo removed and leave the stored repo unchanged.
func TestLayerRepoCredential_UpdateResponse(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()
	registerCredLayer(t, base, "private", credRepoReported)

	for _, name := range []string{"update", "update no-op"} {
		resp, body := mustPut(t, base, "/v1/layers/update?id=private", map[string]any{"ref": "develop"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, body=%s", name, resp.StatusCode, body)
		}
		assertNoRepoCredential(t, name+" response", body)
		if repo, _, _ := decodeRegisterResponse(t, body); repo != credRepoReported {
			t.Errorf("%s: layer.repo = %q, want %q", name, repo, credRepoReported)
		}
		assertSplitRepo(t, st, "private", credRepoFull, credRepoReported)
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
			registerCredLayer(t, base, "private", credRepoReported)

			live := mustGet(t, base, "/v1/layers")
			assertNoRepoCredential(t, "live list", live)
			if got := listedRepos(t, live)["private"]; got != credRepoReported {
				t.Errorf("live list repo = %q, want %q", got, credRepoReported)
			}
			assertSplitRepo(t, st, "private", credRepoFull, credRepoReported)

			resp, body := mustDelete(t, base, "/v1/layers?id=private")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("unregister: status %d, body=%s", resp.StatusCode, body)
			}
			assertNoRepoCredential(t, "unregister response", body)
			deleted := mustGet(t, base, "/v1/layers?deleted=true")
			assertNoRepoCredential(t, "deleted list", deleted)
			if got := listedRepos(t, deleted)["private"]; got != credRepoReported {
				t.Errorf("deleted list repo = %q, want %q", got, credRepoReported)
			}
			tombstones, err := st.ListDeletedLayerConfigs(context.Background(), "t")
			if err != nil {
				t.Fatalf("ListDeletedLayerConfigs: %v", err)
			}
			if len(tombstones) != 1 {
				t.Fatalf("stored tombstones = %+v, want one row", tombstones)
			}
			assertSplitConfig(t, tombstones[0], credRepoFull, credRepoReported)
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
	registerCredLayer(t, base, "private", credRepoReported)
	registerCredLayer(t, base, "other", "https://git.acme.com/acme/other.git")

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
	assertSplitRepo(t, st, "private", credRepoFull, credRepoReported)
	assertSplitRepo(t, st, "other", otherFull, "https://git.acme.com/acme/other.git")
}

// Spec: §7.3.1 (Repository credentials) — one userinfo rule covers every URL
// scheme, and a repo whose credential-bearing part cannot be identified is
// reported as [redacted] with no part of it echoed.
//
// The ssh registration carries its credential in the URL userinfo, because the
// members apply to an http or https remote alone and the url_credential
// refusal reads only the http and https schemes and the [redacted] class. The
// unparseable https value is refused at registration with url_credential, so
// it is seeded as the row an earlier release stored.
func TestLayerRepoCredential_SchemeAndFailClosed(t *testing.T) {
	t.Parallel()

	t.Run("ssh userinfo is removed whole", func(t *testing.T) {
		t.Parallel()
		base, st, cleanup := newLayerHarness(t)
		defer cleanup()
		const stored = "ssh://git:s3cr3tpw@host/x.git"
		resp, body := mustPost(t, base, "/v1/layers", map[string]any{
			"id": "over-ssh", "source_type": "git", "repo": stored, "ref": "main",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("register: status %d, body=%s", resp.StatusCode, body)
		}
		assertNoRepoCredential(t, "register response", body)
		assertSchemeReported(t, base, body, "over-ssh", "ssh://host/x.git")
		assertSplitRepo(t, st, "over-ssh", stored, "ssh://host/x.git")
	})

	t.Run("unparseable URL is withheld", func(t *testing.T) {
		t.Parallel()
		base, st, cleanup := newLayerHarness(t)
		defer cleanup()
		// url.Parse rejects the non-numeric port, so the userinfo cannot be
		// located and the whole value is withheld. The store does not split a
		// fail-closed value, so Repo keeps the registered bytes.
		const stored = "https://alice-user:s3cr3tpw@git.acme.com:port/x.git"
		resp, body := mustPost(t, base, "/v1/layers", map[string]any{
			"id": "unparseable", "source_type": "git", "repo": stored, "ref": "main",
		})
		assertURLCredentialRefusal(t, resp, body)

		seedLegacyLayer(t, st, legacyGitLayer("unparseable"), stored)
		resp, body = mustPut(t, base, "/v1/layers/update?id=unparseable", map[string]any{"ref": "develop"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("update: status %d, body=%s", resp.StatusCode, body)
		}
		assertNoRepoCredential(t, "update response", body)
		assertSchemeReported(t, base, body, "unparseable", "[redacted]")
		assertSplitRepo(t, st, "unparseable", stored, stored)
	})
}

// assertSchemeReported fails unless the register or update response body and
// the list both report reported for id.
func assertSchemeReported(t *testing.T, base string, body []byte, id, reported string) {
	t.Helper()
	if repo, _, _ := decodeRegisterResponse(t, body); repo != reported {
		t.Errorf("response layer.repo = %q, want %q", repo, reported)
	}
	list := mustGet(t, base, "/v1/layers")
	assertNoRepoCredential(t, "list response", list)
	if got := listedRepos(t, list)[id]; got != reported {
		t.Errorf("list repo = %q, want %q", got, reported)
	}
}

// assertURLCredentialRefusal asserts the §7.3.1 url_credential refusal: 400
// registry.invalid_argument carrying details.constraint "url_credential", with
// no part of the fixture credential in the body.
func assertURLCredentialRefusal(t *testing.T, resp *http.Response, body []byte) {
	t.Helper()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	if code, _, constraint := decodeErrorEnvelope(t, body); code != "registry.invalid_argument" || constraint != "url_credential" {
		t.Errorf("code = %q, details.constraint = %q, want registry.invalid_argument and url_credential", code, constraint)
	}
	assertNoRepoCredential(t, "refusal", body)
}

// legacyGitLayer returns an admin-defined git layer of tenant "t" for
// seedLegacyLayer, with a webhook secret a later write can be compared with.
func legacyGitLayer(id string) store.LayerConfig {
	return store.LayerConfig{
		TenantID: "t", ID: id, SourceType: "git", Ref: "main", Order: 10,
		WebhookSecret: "seeded-secret",
	}
}

// aliceGitLayer is legacyGitLayer as alice's user-defined layer.
func aliceGitLayer(id string) store.LayerConfig {
	cfg := legacyGitLayer(id)
	cfg.UserDefined = true
	cfg.Owner = "alice@acme.com"
	cfg.Users = []string{"alice@acme.com"}
	return cfg
}

// localLegacyLayer is an admin-defined local layer on /srv/x for
// seedLegacyLayer. An earlier release stored the repo a local registration
// carried, so the row can hold a credential URL that nothing reads.
func localLegacyLayer(id string) store.LayerConfig {
	return store.LayerConfig{TenantID: "t", ID: id, SourceType: "local", LocalPath: "/srv/x", Order: 10}
}

// seedLegacyLayer writes cfg with registered as its stored repo straight to
// the store, which is how a row an earlier release accepted reaches a test:
// the handler now refuses a credential URL, an empty userinfo, and a
// [redacted]-class value at registration.
//
// Spec: §7.3.1 (Repository credentials)
func seedLegacyLayer(t *testing.T, st store.Store, cfg store.LayerConfig, registered string) {
	t.Helper()
	cfg.Repo, cfg.RegisteredRepo = store.SplitLayerRepo(registered, sql.NullString{})
	if err := st.PutLayerConfig(context.Background(), cfg); err != nil {
		t.Fatalf("PutLayerConfig %s: %v", cfg.ID, err)
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
// audit sink and a recording event publisher. The caller is switchable between
// requests, so one subtest can read the reported repo as a caller who sees the
// layer and then register as another.
type guardHarness struct {
	base   string
	st     store.Store
	sink   *audit.Memory
	events *eventRecorder

	// mu guards who, which the handler goroutines read on every request.
	mu  sync.Mutex
	who guardCaller
}

func newGuardHarness(t *testing.T, who guardCaller) *guardHarness {
	t.Helper()
	return newGuardHarnessOn(t, who, store.NewMemory())
}

// newGuardHarnessOn is newGuardHarness over a store the caller built, for a
// case whose subject is how the store returns a layer.
func newGuardHarnessOn(t *testing.T, who guardCaller, st store.Store) *guardHarness {
	t.Helper()
	h := &guardHarness{st: st, sink: audit.NewMemory(), events: &eventRecorder{}, who: who}
	if err := h.st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(h.st, "t", server.NewModeTracker()).
		WithAudit(h.sink).
		WithEventPublisher(h.events.publish).
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

// update sends an update of id with the given body.
func (h *guardHarness) update(t *testing.T, id string, body map[string]any) (*http.Response, []byte) {
	t.Helper()
	resp, out := mustPut(t, h.base, "/v1/layers/update?id="+url.QueryEscape(id), body)
	assertNoRepoCredential(t, "update response", out)
	return resp, out
}

// seed registers id on repo with extra members as the harness's current
// caller, fails the test unless it is created, and returns the repo the list
// response reports for it.
func (h *guardHarness) seed(t *testing.T, id, repo string, extra map[string]any) string {
	t.Helper()
	if resp, body := h.register(t, id, repo, extra); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed %s: status %d, body=%s", id, resp.StatusCode, body)
	}
	return h.reported(t, id)
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

// unsplitStore wraps a store and returns every live layer the way a store
// that does not apply the §7.3.1 read rule returns it: the registered URL in
// Repo, userinfo included, and an empty RegisteredRepo. A custom RegistryStore
// that skips store.SplitLayerRepo reads this way.
type unsplitStore struct {
	store.Store
}

// unsplit undoes the read split on one config.
func unsplit(cfg store.LayerConfig) store.LayerConfig {
	cfg.Repo = cfg.CloneRepo()
	cfg.RegisteredRepo = ""
	return cfg
}

// GetLayerConfig delegates and returns the layer unsplit.
func (u unsplitStore) GetLayerConfig(ctx context.Context, tenantID, id string) (store.LayerConfig, error) {
	cfg, err := u.Store.GetLayerConfig(ctx, tenantID, id)
	return unsplit(cfg), err
}

// ListLayerConfigs delegates and returns each layer unsplit.
func (u unsplitStore) ListLayerConfigs(ctx context.Context, tenantID string) ([]store.LayerConfig, error) {
	cfgs, err := u.Store.ListLayerConfigs(ctx, tenantID)
	for i := range cfgs {
		cfgs[i] = unsplit(cfgs[i])
	}
	return cfgs, err
}

// Spec: §7.3.1 (Repository credentials) — an update, a reorder, and a webhook
// secret rotation each read the stored layer and write it back, and the
// stored layer still clones with the registered URL after each of them.
func TestLayerRepoCredential_ReadModifyWriteKeepsCloneRepo(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()
	registerCredLayer(t, base, "private", credRepoReported)
	minted, err := st.GetLayerConfig(context.Background(), "t", "private")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}

	steps := []struct {
		name string
		do   func() (*http.Response, []byte)
	}{
		{"update", func() (*http.Response, []byte) {
			return mustPut(t, base, "/v1/layers/update?id=private", map[string]any{"ref": "develop"})
		}},
		{"reorder", func() (*http.Response, []byte) {
			return mustPost(t, base, "/v1/layers/reorder", map[string]any{"order": []string{"private"}})
		}},
		{"rotate_webhook_secret", func() (*http.Response, []byte) {
			return mustPut(t, base, "/v1/layers/update?id=private", map[string]any{"rotate_webhook_secret": true})
		}},
	}
	for _, step := range steps {
		resp, body := step.do()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, body=%s", step.name, resp.StatusCode, body)
		}
		assertNoRepoCredential(t, step.name+" response", body)
		assertSplitRepo(t, st, "private", credRepoFull, credRepoReported)
	}

	// The writes above reached the store, so the assertions did not pass on
	// three requests that stored nothing.
	stored, err := st.GetLayerConfig(context.Background(), "t", "private")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if stored.Ref != "develop" {
		t.Errorf("stored Ref = %q, want develop", stored.Ref)
	}
	if stored.WebhookSecret == "" || stored.WebhookSecret == minted.WebhookSecret {
		t.Errorf("stored webhook secret was not rotated")
	}
}

// Spec: §7.3.1 (Re-registration with a reported repo) — a stored layer holds
// a credential when its registered value carries userinfo, whether or not the
// store split a RegisteredRepo off. A store that returns the registered URL
// unsplit therefore gets the same list response as one that splits, and a
// re-registration with the reported value keeps the registered URL.
func TestLayerRegister_KeepsStoredRemote_StoreThatDoesNotSplit(t *testing.T) {
	t.Parallel()
	mem := store.NewMemory()
	h := newGuardHarnessOn(t, guardAdmin, unsplitStore{Store: mem})

	reported := h.seed(t, "private", credRepoReported, credMembers(nil))
	if reported != credRepoReported {
		t.Fatalf("list repo = %q, want %q", reported, credRepoReported)
	}
	// The handler reads the layer unsplit, so the keep below is decided by
	// the registered value read through CloneRepo and by nothing else.
	seen := h.stored(t, "private")
	if seen.RegisteredRepo.Present() || seen.Repo != credRepoFull {
		t.Fatalf("wrapped read = Repo %q with a present RegisteredRepo %t, want the unsplit URL and none",
			seen.Repo, seen.RegisteredRepo.Present())
	}

	resp, body := h.register(t, "private", reported, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("re-registration: status = %d, want 201: %s", resp.StatusCode, body)
	}
	if repo, _, _ := decodeRegisterResponse(t, body); repo != credRepoReported {
		t.Errorf("re-registration layer.repo = %q, want %q", repo, credRepoReported)
	}
	if got := h.stored(t, "private").CloneRepo(); got != credRepoFull {
		t.Errorf("stored CloneRepo after the kept registration = %q, want %q", got, credRepoFull)
	}
	assertStoredRepo(t, mem, "private", credRepoFull)
}

// Spec: §7.3.1 (Repository credentials) / §7.3.1 (Re-registration with a
// reported repo) — a registered URL whose reported form carries "@" after its
// host is reported as that form by every response. Redacting the reported form
// a second time yields [redacted], so each assertion fails on a handler that
// redacts a Repo the store already redacted. Sending the reported form back is
// refused with url_credential before the keep rule runs, and the stored remote
// is unchanged.
func TestLayerRepoCredential_ReportedValueIsRedactedOnce(t *testing.T) {
	t.Parallel()
	const (
		bare         = "https://git.acme.com/acme/a%40b c.git"
		registered   = "https://:ghp_tok3n@git.acme.com/acme/a%40b c.git"
		reportedWant = "https://git.acme.com/acme/a@b%20c.git"
	)
	h := newGuardHarness(t, guardAdmin)
	assertReported := func(what, got string, body []byte) {
		t.Helper()
		if got != reportedWant {
			t.Errorf("%s repo = %q, want %q", what, got, reportedWant)
		}
		if strings.Contains(string(body), "ghp_tok3n") {
			t.Errorf("%s carries the credential: %s", what, body)
		}
	}

	resp, body := h.register(t, "odd", bare, map[string]any{"repo_token": "ghp_tok3n"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: status %d, body=%s", resp.StatusCode, body)
	}
	repo, _, _ := decodeRegisterResponse(t, body)
	assertReported("register response", repo, body)
	assertSplitConfig(t, h.stored(t, "odd"), registered, reportedWant)

	list := mustGet(t, h.base, "/v1/layers")
	assertReported("list response", listedRepos(t, list)["odd"], list)

	resp, body = h.update(t, "odd", map[string]any{"ref": "develop"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: status %d, body=%s", resp.StatusCode, body)
	}
	repo, _, _ = decodeRegisterResponse(t, body)
	assertReported("update response", repo, body)

	resp, body = mustPost(t, h.base, "/v1/layers/reorder", map[string]any{"order": []string{"odd"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reorder: status %d, body=%s", resp.StatusCode, body)
	}
	assertReported("reorder response", listedRepos(t, body)["odd"], body)
	assertSplitConfig(t, h.stored(t, "odd"), registered, reportedWant)

	before := h.stored(t, "odd")
	resp, body = h.register(t, "odd", reportedWant, nil)
	assertURLCredentialRefusal(t, resp, body)
	if strings.Contains(string(body), "ghp_tok3n") || strings.Contains(string(body), "a@b") {
		t.Errorf("refusal carries part of the repo: %s", body)
	}
	if after := h.stored(t, "odd"); !reflect.DeepEqual(before, after) {
		t.Errorf("stored row changed on a refused registration:\nbefore %+v\nafter  %+v", before, after)
	}
}

// rawRepoRow is the repo and repo_userinfo columns of one layer_configs row,
// read with no store read rule in between.
type rawRepoRow struct {
	Repo     string
	Userinfo sql.NullString
}

// readRawRepoRow reads the repo columns of layer id in tenant "t" from the
// SQLite file at path over a connection of its own.
func readRawRepoRow(t *testing.T, path, id string) rawRepoRow {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	var row rawRepoRow
	if err := db.QueryRow(`SELECT repo, repo_userinfo FROM layer_configs WHERE tenant_id = ? AND id = ?`,
		"t", id).Scan(&row.Repo, &row.Userinfo); err != nil {
		t.Fatalf("select repo, repo_userinfo for %s: %v", id, err)
	}
	return row
}

// writeRawRepoRow sets the repo columns of layer id in tenant "t" in the
// SQLite file at path over a connection of its own, which is how a row a
// release before 0.7.0 left reaches a test.
func writeRawRepoRow(t *testing.T, path, id string, row rawRepoRow) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	res, err := db.Exec(`UPDATE layer_configs SET repo = ?, repo_userinfo = ? WHERE tenant_id = ? AND id = ?`,
		row.Repo, row.Userinfo, "t", id)
	if err != nil {
		t.Fatalf("update repo, repo_userinfo for %s: %v", id, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("update repo, repo_userinfo for %s: %d rows, %v", id, n, err)
	}
}

// openSQLiteForCredential opens a SQLite store on a temp file and returns it
// with the file's path, for a case that reads or writes the raw row.
func openSQLiteForCredential(t *testing.T) (store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "podium.db")
	st, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, path
}

// Spec: §7.3.1 (Repository credentials) — over the SQLite store, a
// registration through the credential members writes the userinfo apart from
// the rest of the value, a re-registration with the reported value keeps the
// row, an update with repo_token rotates the userinfo column alone, and
// clear_repo_credential writes NULL. Step 5 is the row a 0.5.2
// re-registration leaves on Postgres, a bare repo beside a stale userinfo
// column (X12): a re-registration with the userinfo-free URL keeps it, and
// clear_repo_credential removes it.
//
// Spec: §7.3.1 (Setting, replacing, and removing a repository credential)
// Spec: §7.3.1 (Re-registration with a reported repo)
func TestLayerRepoCredential_SQLiteRowHoldsUserinfoApart(t *testing.T) {
	t.Parallel()
	st, path := openSQLiteForCredential(t)
	h := newGuardHarnessOn(t, guardAdmin, st)
	split := func(userinfo string) rawRepoRow {
		return rawRepoRow{Repo: credRepoReported, Userinfo: sql.NullString{String: userinfo, Valid: true}}
	}
	step := func(name string, resp *http.Response, body []byte, wantStatus int, id string, want rawRepoRow) {
		t.Helper()
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s: status = %d, want %d: %s", name, resp.StatusCode, wantStatus, body)
		}
		if got := readRawRepoRow(t, path, id); got != want {
			t.Errorf("%s: row = %+v, want %+v", name, got, want)
		}
	}

	resp, body := h.register(t, "private", credRepoReported, credMembers(nil))
	step("1 register through the members", resp, body, http.StatusCreated, "private", split("alice-user:s3cr3tpw"))
	if repo, _, _ := decodeRegisterResponse(t, body); repo != credRepoReported {
		t.Errorf("register layer.repo = %q, want %q", repo, credRepoReported)
	}
	if got := h.reported(t, "private"); got != credRepoReported {
		t.Errorf("list repo = %q, want %q", got, credRepoReported)
	}

	resp, body = h.register(t, "private", credRepoReported, nil)
	step("2 re-register the reported value", resp, body, http.StatusCreated, "private", split("alice-user:s3cr3tpw"))

	resp, body = h.update(t, "private", map[string]any{"repo_token": "n3wpw"})
	step("3 rotate the token", resp, body, http.StatusOK, "private", split("alice-user:n3wpw"))

	resp, body = h.register(t, "private", credRepoReported, map[string]any{"clear_repo_credential": true})
	step("4 register with clear_repo_credential", resp, body, http.StatusCreated, "private", rawRepoRow{Repo: credRepoReported})

	const other = "https://git.acme.com/acme/other.git"
	resp, body = h.register(t, "other", other, nil)
	step("5 register other", resp, body, http.StatusCreated, "other", rawRepoRow{Repo: other})
	stale := rawRepoRow{Repo: other, Userinfo: sql.NullString{String: "alice-user:s3cr3tpw", Valid: true}}
	writeRawRepoRow(t, path, "other", stale)
	resp, body = h.register(t, "other", other, nil)
	step("5 re-register other over the X12 row", resp, body, http.StatusCreated, "other", stale)
	resp, body = h.update(t, "other", map[string]any{"clear_repo_credential": true})
	step("5 clear the X12 row", resp, body, http.StatusOK, "other", rawRepoRow{Repo: other})
}
