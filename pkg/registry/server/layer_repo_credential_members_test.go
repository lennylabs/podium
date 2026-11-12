package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// memberRepo is the bare remote the member cases register.
const memberRepo = "https://git.acme.com/acme/private.git"

// assertAbsent fails when text carries any of parts.
func assertAbsent(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if part != "" && strings.Contains(text, part) {
			t.Errorf("%s carries %q: %s", what, part, text)
		}
	}
}

// assertUnchangedRow fails when the live row of id differs from before and
// when the audit sink holds more than events entries.
func (h *guardHarness) assertUnchangedRow(t *testing.T, id string, before store.LayerConfig, events int) {
	t.Helper()
	if after := h.stored(t, id); !reflect.DeepEqual(before, after) {
		t.Errorf("stored row of %s changed:\nbefore %+v\nafter  %+v", id, before, after)
	}
	if got := len(h.sink.Events()); got != events {
		t.Errorf("audit sink holds %d events, want %d", got, events)
	}
}

// mustStatus fails the test unless resp carries want.
func mustStatus(t *testing.T, what string, resp *http.Response, body []byte, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s: status = %d, want %d: %s", what, resp.StatusCode, want, body)
	}
}

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) —
// the member semantics over the memory store. A registration stores the
// credential it carries with the username as sent; an update with repo_token
// replaces it on the stored remote, keeping the stored username when the body
// omits repo_username and taking a present empty or null one as the empty
// username; clear_repo_credential removes it; and a request that changes no
// credential and no other field stores nothing and records no event.
func TestLayerRepoCredential_Members(t *testing.T) {
	t.Parallel()

	t.Run("register, rotate, and clear", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAdmin)
		withUser := func(token string) string {
			return "https://x-access-token:" + token + "@git.acme.com/acme/private.git"
		}
		noUser := func(token string) string { return "https://:" + token + "@git.acme.com/acme/private.git" }

		// Step 1 (X2-0.7.0).
		resp, body := h.register(t, "private", memberRepo,
			map[string]any{"repo_username": "x-access-token", "repo_token": "tok1"})
		mustStatus(t, "register", resp, body, http.StatusCreated)
		assertAbsent(t, "register response", string(body), "x-access-token", "tok1")
		assertAbsent(t, "list response", string(mustGet(t, h.base, "/v1/layers")), "x-access-token", "tok1")
		assertStoredRepo(t, h.st, "private", withUser("tok1"))

		// Step 2 (X4-0.7.0): an omitted repo_username keeps the stored one.
		resp, body = h.update(t, "private", map[string]any{"repo_token": "tok2"})
		mustStatus(t, "token-only update", resp, body, http.StatusOK)
		assertAbsent(t, "update response", string(body), "x-access-token", "tok2")
		assertStoredRepo(t, h.st, "private", withUser("tok2"))

		// Step 5: neither member over a stored credential.
		events := len(h.sink.Events())
		before := h.stored(t, "private")
		resp, body = h.update(t, "private", map[string]any{"ref": "main"})
		mustStatus(t, "member-free update", resp, body, http.StatusOK)
		h.assertUnchangedRow(t, "private", before, events)

		// Step 9: a present empty repo_username alone changes nothing.
		resp, body = h.update(t, "private", map[string]any{"repo_username": ""})
		mustStatus(t, "empty repo_username update", resp, body, http.StatusOK)
		h.assertUnchangedRow(t, "private", before, events)

		// Step 3: a present empty or null repo_username is the empty username.
		resp, body = h.update(t, "private", map[string]any{"repo_username": "", "repo_token": "tok3"})
		mustStatus(t, "empty-username update", resp, body, http.StatusOK)
		assertStoredRepo(t, h.st, "private", noUser("tok3"))
		resp, body = h.update(t, "private", map[string]any{"repo_username": nil, "repo_token": "tok4"})
		mustStatus(t, "null-username update", resp, body, http.StatusOK)
		assertStoredRepo(t, h.st, "private", noUser("tok4"))

		// Step 4.
		resp, body = h.update(t, "private", map[string]any{"clear_repo_credential": true})
		mustStatus(t, "clearing update", resp, body, http.StatusOK)
		cleared := h.stored(t, "private")
		if cleared.CloneRepo() != memberRepo || cleared.RegisteredRepo.Present() {
			t.Errorf("cleared row = CloneRepo %q, RegisteredRepo present %t; want %q and none",
				cleared.CloneRepo(), cleared.RegisteredRepo.Present(), memberRepo)
		}
	})

	t.Run("legacy token-only userinfo holds no username", func(t *testing.T) {
		t.Parallel()
		// Step 6 (D-5).
		h := newGuardHarness(t, guardAdmin)
		seedLegacyLayer(t, h.st, legacyGitLayer("private"), "https://tok@git.acme.com/acme/private.git")
		resp, body := h.update(t, "private", map[string]any{"repo_token": "tok5"})
		mustStatus(t, "token-only update", resp, body, http.StatusOK)
		assertStoredRepo(t, h.st, "private", "https://:tok5@git.acme.com/acme/private.git")
	})

	t.Run("registration without a username", func(t *testing.T) {
		t.Parallel()
		// Step 7.
		h := newGuardHarness(t, guardAdmin)
		for id, extra := range map[string]map[string]any{
			"empty":  {"repo_username": "", "repo_token": "tokE"},
			"absent": {"repo_token": "tokA"},
		} {
			resp, body := h.register(t, id, memberRepo, extra)
			mustStatus(t, "register "+id, resp, body, http.StatusCreated)
			token, _ := extra["repo_token"].(string)
			assertStoredRepo(t, h.st, id, "https://:"+token+"@git.acme.com/acme/private.git")
		}
	})

	t.Run("a formatted request carries neither member", func(t *testing.T) {
		t.Parallel()
		// Step 8 (D-14).
		var req server.LayerRegisterRequest
		if err := json.Unmarshal([]byte(`{"repo_username":"alice-user","repo_token":"s3cr3tpw"}`), &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.RepoUsername == "" || req.RepoToken == "" {
			t.Fatalf("decoded request = %+v, want both members set", req)
		}
		assertAbsent(t, "formatted request", fmt.Sprintf("%v %+v %#v", req, req, req), "alice-user", "s3cr3tpw")
		if got := fmt.Sprint(server.RepoSecret("")); got != "" {
			t.Errorf("an empty member formats as %q, want the empty string", got)
		}
	})

	t.Run("no-op members on a layer without a credential", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAdmin)
		seedLegacyLayer(t, h.st, legacyGitLayer("plain"), "https://git.acme.com/acme/plain.git")
		seedLegacyLayer(t, h.st, legacyGitLayer("over-ssh"), "ssh://git@git.acme.com/acme/x.git")
		seedLegacyLayer(t, h.st, localLegacyLayer("local"), "")
		cases := []struct {
			id   string
			body map[string]any
		}{
			// Step 10.
			{"plain", map[string]any{"clear_repo_credential": true}},
			// Step 11 (D-5, D-12): a present empty repo_username is no
			// credential member, so the applicability refusal does not fire.
			{"over-ssh", map[string]any{"repo_username": ""}},
			{"local", map[string]any{"repo_username": ""}},
		}
		for _, tc := range cases {
			before := h.stored(t, tc.id)
			resp, body := h.update(t, tc.id, tc.body)
			mustStatus(t, "update "+tc.id, resp, body, http.StatusOK)
			h.assertUnchangedRow(t, tc.id, before, 0)
		}
		// Step 11: a member-free ssh registration is admitted.
		const ssh = "ssh://git.acme.com/acme/x.git"
		resp, body := h.register(t, "fresh-ssh", ssh, nil)
		mustStatus(t, "register ssh", resp, body, http.StatusCreated)
		assertStoredRepo(t, h.st, "fresh-ssh", ssh)
	})

	t.Run("a first credential on a layer that holds none", func(t *testing.T) {
		t.Parallel()
		// Step 12 (X13-0.7.0): a row with no credential keeps no username, so
		// a token-only update stores the empty username.
		h := newGuardHarness(t, guardAdmin)
		seedLegacyLayer(t, h.st, legacyGitLayer("plain"), "https://git.acme.com/acme/plain.git")
		seedLegacyLayer(t, h.st, legacyGitLayer("plain2"), "https://git.acme.com/acme/plain2.git")
		resp, body := h.update(t, "plain", map[string]any{"repo_username": "alice-user", "repo_token": "tok6"})
		mustStatus(t, "update plain", resp, body, http.StatusOK)
		assertStoredRepo(t, h.st, "plain", "https://alice-user:tok6@git.acme.com/acme/plain.git")
		resp, body = h.update(t, "plain2", map[string]any{"repo_token": "tok7"})
		mustStatus(t, "update plain2", resp, body, http.StatusOK)
		assertStoredRepo(t, h.st, "plain2", "https://:tok7@git.acme.com/acme/plain2.git")
	})
}

// refusalCase is one request the credential rules refuse.
type refusalCase struct {
	name string
	// seed writes layer "x" before the request; nil leaves the ID unused.
	seed   func(t *testing.T, st store.Store)
	caller guardCaller
	// update sends body as an update of "x"; otherwise body is merged into
	// a git registration of "x" on repo.
	update bool
	repo   string
	body   map[string]any
	status int
	code   string
	// constraint is the details.constraint the envelope carries, or empty
	// for an envelope with none.
	constraint string
	// named are the substrings the message carries, and absent the ones it
	// must not: the fixture username, token, and repo bytes.
	named, absent []string
	// then runs after the refusal's assertions, for a case whose remedy is
	// part of the rule.
	then func(t *testing.T, h *guardHarness)
}

// credentialRefusalAbsent is the fixture text no refusal message carries.
var credentialRefusalAbsent = []string{"alice-user", "s3cr3tpw", "git.acme.com", "host:org"}

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) —
// each refusal answers its code and constraint, names the member, carries no
// part of the username, the token, or the repo, stores nothing, and records no
// §8.1 event. An update that carries rotate_webhook_secret with a refused
// member mints no secret.
func TestLayerRepoCredential_Refusals(t *testing.T) {
	t.Parallel()
	urlMessage := []string{"repo_username", "repo_token"}
	parseMessage := []string{"does not parse"}
	seedAt := func(cfg store.LayerConfig, registered string) func(*testing.T, store.Store) {
		return func(t *testing.T, st store.Store) { seedLegacyLayer(t, st, cfg, registered) }
	}
	redactedRow := seedAt(legacyGitLayer("x"), "https://git.acme.com/acme/x.git@s3cr3tpw")
	sshRow := seedAt(legacyGitLayer("x"), "ssh://git@git.acme.com/acme/x.git")
	localRow := seedAt(localLegacyLayer("x"), "https://alice-user:s3cr3tpw@git.acme.com/acme/x.git")
	aliceRow := seedAt(aliceGitLayer("x"), credRepoFull)
	local := func(extra map[string]any) map[string]any {
		out := map[string]any{"source_type": "local", "local_path": "/srv/x"}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	bad := http.StatusBadRequest
	const invalid = "registry.invalid_argument"

	cases := []refusalCase{
		// X1-0.7.0, X15-0.7.0.
		{name: "register userinfo", repo: "https://alice-user:s3cr3tpw@git.acme.com/x.git",
			status: bad, code: invalid, constraint: "url_credential", named: urlMessage},
		{name: "register empty userinfo", repo: "https://@git.acme.com/x.git",
			status: bad, code: invalid, constraint: "url_credential", named: urlMessage},
		{name: "register redacted class", repo: "https://git.acme.com/x.git@s3cr3tpw",
			status: bad, code: invalid, constraint: "url_credential", named: urlMessage},
		{name: "register unparseable ssh", repo: "ssh://git@host:org/x.git",
			status: bad, code: invalid, constraint: "url_credential", named: parseMessage,
			absent: []string{"repo_username", "repo_token"}},
		// X1-0.7.0, D-8: the refusal reads no source type.
		{name: "register local with a credential URL", repo: "https://alice-user:s3cr3tpw@git.acme.com/x.git",
			body: local(nil), status: bad, code: invalid, constraint: "url_credential", named: urlMessage},
		// X5-0.7.0, X15-0.7.0: url_credential precedes the member refusal.
		{name: "register redacted class with members", repo: "https://git.acme.com/x.git@s3cr3tpw",
			body: credMembers(nil), status: bad, code: invalid, constraint: "url_credential", named: urlMessage},
		{name: "register unparseable ssh with a token", repo: "ssh://git@host:org/x.git",
			body: map[string]any{"repo_token": "s3cr3tpw"}, status: bad, code: invalid,
			constraint: "url_credential", named: parseMessage, absent: []string{"repo_username", "repo_token"}},
		// D-6.
		{name: "username with a colon", repo: memberRepo,
			body:   map[string]any{"repo_username": "acme:bot", "repo_token": "s3cr3tpw"},
			status: bad, code: invalid, named: []string{"repo_username"}, absent: []string{"acme:bot"}},
		{name: "username with a newline", repo: memberRepo,
			body:   map[string]any{"repo_username": "acme\nbot", "repo_token": "s3cr3tpw"},
			status: bad, code: invalid, named: []string{"repo_username"}},
		{name: "username with a space and a slash", repo: memberRepo,
			body:   map[string]any{"repo_username": "acme ci/bot", "repo_token": "s3cr3tpw"},
			status: bad, code: invalid, named: []string{"repo_username"}, absent: []string{"acme", "ci/bot"}},
		{name: "token with a NUL", repo: memberRepo,
			body:   map[string]any{"repo_username": "alice-user", "repo_token": "s3cr\x00tpw"},
			status: bad, code: invalid, named: []string{"repo_token"}, absent: []string{"s3cr"}},
		{name: "username without a token", repo: memberRepo,
			body:   map[string]any{"repo_username": "alice-user"},
			status: bad, code: invalid, named: []string{"repo_username", "repo_token"}},
		{name: "token with clear_repo_credential", repo: memberRepo,
			body:   map[string]any{"repo_token": "s3cr3tpw", "clear_repo_credential": true},
			status: bad, code: invalid, named: []string{"repo_token", "clear_repo_credential"}},
		// D-12, Source type: a git source on this repo would be admitted.
		{name: "register local with a token", repo: "https://git.acme.com/x.git",
			body:   local(map[string]any{"repo_token": "s3cr3tpw"}),
			status: bad, code: invalid, named: []string{"repo_token"}},
		{name: "register ssh with a token", repo: "ssh://git.acme.com/x.git",
			body:   map[string]any{"repo_token": "s3cr3tpw"},
			status: bad, code: invalid, named: []string{"repo_token"}},
		{name: "register local with clear", repo: "https://git.acme.com/x.git",
			body:   local(map[string]any{"clear_repo_credential": true}),
			status: bad, code: invalid, named: []string{"clear_repo_credential"}},
		{name: "register ssh with clear", repo: "ssh://git.acme.com/x.git",
			body:   map[string]any{"clear_repo_credential": true},
			status: bad, code: invalid, named: []string{"clear_repo_credential"}},
		// D-4: a stored username the D-6 rule refuses cannot be kept.
		{name: "update keeps a refused stored username",
			seed:   seedAt(legacyGitLayer("x"), "https://a%3Ab:tok@git.acme.com/acme/x.git"),
			update: true, body: map[string]any{"repo_token": "n3wpw"},
			status: bad, code: invalid, named: []string{"repo_username"},
			absent: []string{"a:b", "a%3Ab", "tok@", "n3wpw"},
			then: func(t *testing.T, h *guardHarness) {
				resp, body := h.update(t, "x", map[string]any{"repo_username": "alice-user", "repo_token": "n3wpw"})
				mustStatus(t, "update with repo_username", resp, body, http.StatusOK)
				assertStoredRepo(t, h.st, "x", "https://alice-user:n3wpw@git.acme.com/acme/x.git")
			}},
		// X5-0.7.0; D-12, Source type.
		{name: "update redacted row with a token", seed: redactedRow, update: true,
			body: map[string]any{"repo_token": "n3wpw"}, status: bad, code: invalid, named: []string{"repo_token"}},
		{name: "update ssh row with a token", seed: sshRow, update: true,
			body: map[string]any{"repo_token": "n3wpw"}, status: bad, code: invalid, named: []string{"repo_token"}},
		{name: "update local row with a token", seed: localRow, update: true,
			body: map[string]any{"repo_token": "n3wpw"}, status: bad, code: invalid, named: []string{"repo_token"}},
		{name: "update redacted row with clear", seed: redactedRow, update: true,
			body:   map[string]any{"clear_repo_credential": true},
			status: bad, code: invalid, named: []string{"clear_repo_credential"}},
		{name: "update ssh row with clear", seed: sshRow, update: true,
			body:   map[string]any{"clear_repo_credential": true},
			status: bad, code: invalid, named: []string{"clear_repo_credential"}},
		{name: "update local row with clear", seed: localRow, update: true,
			body:   map[string]any{"clear_repo_credential": true},
			status: bad, code: invalid, named: []string{"clear_repo_credential"}},
		// D-13: the immutable visibility rule answers first on update.
		{name: "update visibility with a bad member", seed: aliceRow, update: true,
			body:   map[string]any{"public": true, "repo_username": "acme:bot", "repo_token": "n3wpw"},
			status: bad, code: invalid, constraint: "immutable_visibility"},
		{name: "update by a caller the write rule refuses", seed: aliceRow, update: true, caller: guardBob,
			body:   map[string]any{"repo_username": "acme:bot", "repo_token": "n3wpw"},
			status: http.StatusForbidden, code: "auth.forbidden"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			caller := tc.caller
			if caller.id.Sub == "" {
				caller = guardAdmin
			}
			h := newGuardHarness(t, caller)
			if tc.seed != nil {
				tc.seed(t, h.st)
			}
			before, seeded := h.lookup(t, "x")
			resp, body := h.sendRefusalCase(t, tc)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.status, body)
			}
			code, message, constraint := decodeErrorEnvelope(t, body)
			if code != tc.code || constraint != tc.constraint {
				t.Errorf("code = %q, details.constraint = %q, want %q and %q", code, constraint, tc.code, tc.constraint)
			}
			for _, part := range tc.named {
				if !strings.Contains(message, part) {
					t.Errorf("message does not name %q: %q", part, message)
				}
			}
			assertAbsent(t, "message", message, append(append([]string{}, credentialRefusalAbsent...), tc.absent...)...)
			if after, exists := h.lookup(t, "x"); exists != seeded || !reflect.DeepEqual(before, after) {
				t.Errorf("stored row changed on a refusal:\nbefore %+v\nafter  %+v", before, after)
			}
			if events := h.sink.Events(); len(events) != 0 {
				t.Errorf("a refusal recorded %d audit events: %+v", len(events), events)
			}
			if tc.then != nil {
				tc.then(t, h)
			}
		})
	}
}

// lookup returns the live row of id and whether one exists.
func (h *guardHarness) lookup(t *testing.T, id string) (store.LayerConfig, bool) {
	t.Helper()
	cfg, err := h.st.GetLayerConfig(context.Background(), "t", id)
	if errors.Is(err, store.ErrNotFound) {
		return store.LayerConfig{}, false
	}
	if err != nil {
		t.Fatalf("GetLayerConfig %s: %v", id, err)
	}
	return cfg, true
}

// sendRefusalCase sends tc's request. An update carries
// rotate_webhook_secret, so the unchanged-row assertion also pins that a
// refused member mints no secret.
func (h *guardHarness) sendRefusalCase(t *testing.T, tc refusalCase) (*http.Response, []byte) {
	t.Helper()
	if !tc.update {
		return h.register(t, "x", tc.repo, tc.body)
	}
	body := map[string]any{"rotate_webhook_secret": true}
	for k, v := range tc.body {
		body[k] = v
	}
	return h.update(t, "x", body)
}

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) —
// a credential change records its §8.1 event, and it wakes a §7.5.4 watcher
// only when the reported repo changes: a rotation on the same remote changes
// RegisteredRepo alone, which TestWakesWatchers_EveryField classifies as no
// wake, and a first credential on a remote whose reported form re-serializes
// changes the reported Repo.
func TestLayerRepoCredential_Events(t *testing.T) {
	t.Parallel()
	lastEvent := func(t *testing.T, h *guardHarness) audit.Event {
		t.Helper()
		events := h.sink.Events()
		if len(events) == 0 {
			t.Fatalf("the audit sink holds no event")
		}
		return events[len(events)-1]
	}

	t.Run("admin-defined layer", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAdmin)
		h.seed(t, "private", memberRepo, credMembers(nil))
		recorded, published := len(h.sink.Events()), len(h.events.typesFor())
		resp, body := h.update(t, "private", map[string]any{"repo_token": "n3wpw"})
		mustStatus(t, "rotation", resp, body, http.StatusOK)
		if got := len(h.sink.Events()); got != recorded+1 {
			t.Fatalf("audit sink holds %d events, want %d", got, recorded+1)
		}
		if ev := lastEvent(t, h); ev.Type != audit.EventLayerConfigChanged {
			t.Errorf("event type = %s, want %s", ev.Type, audit.EventLayerConfigChanged)
		}
		if got := len(h.events.typesFor()); got != published {
			t.Errorf("a credential rotation published %d change events, want none", got-published)
		}
	})

	t.Run("user-defined layer", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAlice)
		h.seed(t, "alice-personal", memberRepo, credMembers(nil))
		resp, body := h.update(t, "alice-personal", map[string]any{"repo_token": "n3wpw"})
		mustStatus(t, "rotation", resp, body, http.StatusOK)
		if ev := lastEvent(t, h); ev.Type != audit.EventLayerUserRegistered {
			t.Errorf("event type = %s, want %s", ev.Type, audit.EventLayerUserRegistered)
		}
	})

	t.Run("first credential re-serializes the reported repo", func(t *testing.T) {
		t.Parallel()
		h := newGuardHarness(t, guardAdmin)
		seedLegacyLayer(t, h.st, legacyGitLayer("upper"), "HTTPS://Git.Acme.com/x.git")
		resp, body := h.update(t, "upper", map[string]any{"repo_token": "n3wpw"})
		mustStatus(t, "first credential", resp, body, http.StatusOK)
		if repo, _, _ := decodeRegisterResponse(t, body); repo != "https://Git.Acme.com/x.git" {
			t.Errorf("layer.repo = %q, want the re-serialized form", repo)
		}
		if got := h.events.typesFor(); len(got) != 1 {
			t.Errorf("published events = %v, want one change event", got)
		}
	})
}

// lookupCountingStore records the reads lookupLayerForWrite makes.
type lookupCountingStore struct {
	store.Store
	gets, deleted atomic.Int64
}

// GetLayerConfig counts and delegates.
func (c *lookupCountingStore) GetLayerConfig(ctx context.Context, tenantID, id string) (store.LayerConfig, error) {
	c.gets.Add(1)
	return c.Store.GetLayerConfig(ctx, tenantID, id)
}

// ListDeletedLayerConfigs counts and delegates.
func (c *lookupCountingStore) ListDeletedLayerConfigs(ctx context.Context, tenantID string) ([]store.LayerConfig, error) {
	c.deleted.Add(1)
	return c.Store.ListDeletedLayerConfigs(ctx, tenantID)
}

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) —
// the registration refusals run before the stored-layer lookup, so a caller
// with no verified subject learns nothing about the layer the ID names: the
// refusal reads no layer.
func TestLayerRegister_CredentialRefusalReadsNoLayer(t *testing.T) {
	t.Parallel()
	anonymous := guardCaller{id: layer.Identity{}}
	cases := []struct {
		name string
		repo string
		body map[string]any
	}{
		{"url_credential", credRepoFull, nil},
		{"bad member", memberRepo, map[string]any{"repo_username": "acme:bot", "repo_token": "s3cr3tpw"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			counting := &lookupCountingStore{Store: store.NewMemory()}
			h := newGuardHarnessOn(t, anonymous, counting)
			seedLegacyLayer(t, counting, legacyGitLayer("x"), credRepoFull)
			resp, body := h.register(t, "x", tc.repo, tc.body)
			mustStatus(t, "register", resp, body, http.StatusBadRequest)
			if gets, deleted := counting.gets.Load(), counting.deleted.Load(); gets != 0 || deleted != 0 {
				t.Errorf("the refusal read %d live and %d deleted layers, want none", gets, deleted)
			}
		})
	}
}
