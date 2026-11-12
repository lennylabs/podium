package server_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// repoCredentialOf decodes repo_credential from one layer object on the wire:
// nil when the object omits the member.
func repoCredentialOf(t *testing.T, raw json.RawMessage) *bool {
	t.Helper()
	var obj struct {
		RepoCredential *bool `json:"repo_credential"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode layer object: %v\nobject: %s", err, raw)
	}
	return obj.RepoCredential
}

// presence renders a decoded repo_credential for a message.
func presence(v *bool) string {
	switch {
	case v == nil:
		return "omitted"
	case *v:
		return "true"
	default:
		return "false"
	}
}

// assertPresence fails unless the layer object carries want, where nil means
// the member is omitted. It also fails when the object carries a fixture
// username or token.
func assertPresence(t *testing.T, where string, raw json.RawMessage, want *bool) {
	t.Helper()
	if got := repoCredentialOf(t, raw); presence(got) != presence(want) {
		t.Errorf("%s: repo_credential = %s, want %s", where, presence(got), presence(want))
	}
	assertAbsent(t, where, string(raw), "alice-user", "s3cr3tpw", "tok@")
}

// presenceStore is one store the presence cases run over. path is the SQLite
// file for a case that writes raw rows, and empty for the memory store.
type presenceStore struct {
	name string
	open func(t *testing.T) (store.Store, string)
}

func presenceStores() []presenceStore {
	return []presenceStore{
		{"memory", func(*testing.T) (store.Store, string) { return store.NewMemory(), "" }},
		{"sqlite", openSQLiteForCredential},
	}
}

var (
	presentTrue  = func() *bool { v := true; return &v }()
	presentFalse = func() *bool { v := false; return &v }()
)

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) —
// the layer object carries repo_credential on a git layer whose stored repo
// is an http or https URL outside the [redacted] class, true when the
// registered value carries userinfo, and omits it on every other layer. Each
// route that returns a layer reports it, a caller who can see the layer reads
// the value the admin reads, and the member agrees with the update refusal of
// clear_repo_credential on every stored row (X17-0.7.0).
func TestLayerRepoCredential_PresenceMember(t *testing.T) {
	t.Parallel()
	for _, ps := range presenceStores() {
		ps := ps
		t.Run(ps.name+"/routes", func(t *testing.T) {
			t.Parallel()
			st, _ := ps.open(t)
			assertPresenceRoutes(t, newGuardHarnessOn(t, guardAdmin, st))
		})
		t.Run(ps.name+"/stored rows", func(t *testing.T) {
			t.Parallel()
			st, path := ps.open(t)
			assertPresenceRows(t, newGuardHarnessOn(t, guardAdmin, st), path)
		})
	}
}

// assertPresenceRoutes drives one credential layer through every route that
// returns a layer object.
func assertPresenceRoutes(t *testing.T, h *guardHarness) {
	t.Helper()
	resp, body := h.register(t, "private", credRepoReported, credMembers(map[string]any{"users": []string{"alice@acme.com"}}))
	mustStatus(t, "register", resp, body, http.StatusCreated)
	assertPresence(t, "register response", singleLayer(t, body), presentTrue)
	assertPresence(t, "list", findLayer(t, mustGet(t, h.base, "/v1/layers"), "private"), presentTrue)
	resp, body = mustPost(t, h.base, "/v1/layers/reorder", map[string]any{"order": []string{"private"}})
	mustStatus(t, "reorder", resp, body, http.StatusOK)
	assertPresence(t, "reorder response", findLayer(t, body, "private"), presentTrue)

	resp, body = h.update(t, "private", map[string]any{"repo_token": "n3wpw"})
	mustStatus(t, "rotation", resp, body, http.StatusOK)
	assertPresence(t, "rotation response", singleLayer(t, body), presentTrue)
	// A restating update stores nothing and answers the unchanged object.
	resp, body = h.update(t, "private", map[string]any{"ref": h.stored(t, "private").Ref})
	mustStatus(t, "restating update", resp, body, http.StatusOK)
	assertPresence(t, "restating update response", singleLayer(t, body), presentTrue)
	resp, body = h.register(t, "private", credRepoReported, map[string]any{"users": []string{"alice@acme.com"}})
	mustStatus(t, "kept re-registration", resp, body, http.StatusCreated)
	assertPresence(t, "kept re-registration response", singleLayer(t, body), presentTrue)

	// A caller who can see the layer reads what the admin reads.
	h.actAs(guardAlice)
	assertPresence(t, "list as alice", findLayer(t, mustGet(t, h.base, "/v1/layers"), "private"), presentTrue)
	h.actAs(guardAdmin)

	resp, body = h.update(t, "private", map[string]any{"clear_repo_credential": true})
	mustStatus(t, "clearing update", resp, body, http.StatusOK)
	assertPresence(t, "clearing update response", singleLayer(t, body), presentFalse)
	assertPresence(t, "list after the clear", findLayer(t, mustGet(t, h.base, "/v1/layers"), "private"), presentFalse)

	h.seed(t, "gone", credRepoReported, credMembers(nil))
	resp, body = mustDelete(t, h.base, "/v1/layers?id=gone")
	mustStatus(t, "unregister", resp, body, http.StatusOK)
	assertPresence(t, "deleted list", findLayer(t, mustGet(t, h.base, "/v1/layers?deleted=true"), "gone"), presentTrue)
}

// presenceRow is one stored row and the repo_credential its layer object
// carries.
type presenceRow struct {
	id   string
	seed func(t *testing.T, st store.Store, path string)
	want *bool
	// reported is the layer.repo the list carries, checked when non-empty.
	reported string
}

// presenceRows returns the rows seeded with seedLegacyLayer, and the rows
// written through the raw SQLite connection when path names a file.
func presenceRows(path string) []presenceRow {
	legacy := func(cfg store.LayerConfig, registered string) func(*testing.T, store.Store, string) {
		return func(t *testing.T, st store.Store, _ string) { seedLegacyLayer(t, st, cfg, registered) }
	}
	raw := func(id, repo string, userinfo sql.NullString) func(*testing.T, store.Store, string) {
		return func(t *testing.T, st store.Store, path string) {
			seedLegacyLayer(t, st, legacyGitLayer(id), "https://git.acme.com/acme/placeholder.git")
			writeRawRepoRow(t, path, id, rawRepoRow{Repo: repo, Userinfo: userinfo})
		}
	}
	stale := sql.NullString{String: "alice-user:s3cr3tpw", Valid: true}
	rows := []presenceRow{
		{id: "userinfo", seed: legacy(legacyGitLayer("userinfo"), "https://alice-user:s3cr3tpw@git.acme.com/acme/x.git"), want: presentTrue},
		{id: "empty", seed: legacy(legacyGitLayer("empty"), "https://@git.acme.com/acme/x.git"), want: presentTrue},
		{id: "token-only", seed: legacy(legacyGitLayer("token-only"), "https://tok@git.acme.com/acme/x.git"), want: presentTrue},
		{id: "redacted", seed: legacy(legacyGitLayer("redacted"), "https://git.acme.com/acme/x.git@s3cr3tpw"), reported: "[redacted]"},
		{id: "ssh", seed: legacy(legacyGitLayer("ssh"), "ssh://git@git.acme.com/acme/x.git")},
		{id: "local", seed: legacy(localLegacyLayer("local"), "")},
		{id: "local-credential", seed: legacy(localLegacyLayer("local-credential"), "https://alice-user:s3cr3tpw@git.acme.com/acme/x.git")},
		// The reported repo is "https:", so a member computed from the
		// reported value would read no userinfo.
		{id: "hostless", seed: legacy(legacyGitLayer("hostless"), "https://tok@"), want: presentTrue, reported: "https:"},
		{id: "plain", seed: legacy(legacyGitLayer("plain"), "https://git.acme.com/acme/plain.git"), want: presentFalse},
	}
	if path == "" {
		return rows
	}
	return append(rows,
		// A whole credential URL with NULL repo_userinfo, the form a row takes
		// before the start-time rewrite, which the handler harness never runs.
		presenceRow{id: "whole", seed: raw("whole", "https://alice-user:s3cr3tpw@git.acme.com/acme/legacy.git", sql.NullString{}), want: presentTrue},
		// The X12 row of TestLayerRepoCredential_SQLiteRowHoldsUserinfoApart.
		presenceRow{id: "x12", seed: raw("x12", "https://git.acme.com/acme/other.git", stale), want: presentTrue},
		// The read ignores the column beside an scp-like repo.
		presenceRow{id: "scp", seed: raw("scp", "git@github.com:acme/x.git", stale)},
		// The read joins the column into an ssh URL, which takes no member.
		presenceRow{id: "ssh-x12", seed: raw("ssh-x12", "ssh://git.acme.com/acme/x.git", stale)},
	)
}

// assertPresenceRows reads the member on each stored row, then sends
// clear_repo_credential to each and asserts that the update agrees: a row
// whose object omits the member is refused and unchanged, and a row that
// carries it is answered 200 with false, its CloneRepo changing exactly when
// the member was true.
func assertPresenceRows(t *testing.T, h *guardHarness, path string) {
	t.Helper()
	rows := presenceRows(path)
	for _, row := range rows {
		row.seed(t, h.st, path)
	}
	list := mustGet(t, h.base, "/v1/layers")
	for _, row := range rows {
		obj := findLayer(t, list, row.id)
		assertPresence(t, "list "+row.id, obj, row.want)
		if row.reported != "" {
			var got struct {
				Repo string `json:"repo"`
			}
			if err := json.Unmarshal(obj, &got); err != nil || got.Repo != row.reported {
				t.Errorf("list %s: repo = %q (%v), want %q", row.id, got.Repo, err, row.reported)
			}
		}
	}
	for _, row := range rows {
		before := h.stored(t, row.id)
		resp, body := h.update(t, row.id, map[string]any{"clear_repo_credential": true})
		after := h.stored(t, row.id)
		if row.want == nil {
			_, message, _ := decodeErrorEnvelope(t, body)
			if resp.StatusCode != http.StatusBadRequest || !strings.Contains(message, "clear_repo_credential") {
				t.Errorf("clear %s: status %d, message %q; want 400 naming clear_repo_credential", row.id, resp.StatusCode, message)
			}
			if after.CloneRepo() != before.CloneRepo() {
				t.Errorf("clear %s: CloneRepo %q became %q on a refusal", row.id, before.CloneRepo(), after.CloneRepo())
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("clear %s: status %d, want 200: %s", row.id, resp.StatusCode, body)
			continue
		}
		assertPresence(t, "clear "+row.id, singleLayer(t, body), presentFalse)
		if changed := after.CloneRepo() != before.CloneRepo(); changed != *row.want {
			t.Errorf("clear %s: CloneRepo changed %t, want %t (%q to %q)", row.id, changed, *row.want, before.CloneRepo(), after.CloneRepo())
		}
	}
}
