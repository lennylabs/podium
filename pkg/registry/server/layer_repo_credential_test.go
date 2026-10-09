package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
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
