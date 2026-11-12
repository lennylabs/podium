package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// keepCase is one re-registration of layer "x" under the keep rule.
type keepCase struct {
	name string
	// seed writes layer "x" as whichever caller it needs. The registration
	// then runs as caller.
	seed   func(t *testing.T, h *guardHarness)
	caller guardCaller
	repo   string
	extra  map[string]any
	status int
	// clone is the stored CloneRepo after the request, and reported the
	// layer.repo a 201 response carries.
	clone, reported string
	// check runs extra assertions on the stored row after a 201.
	check func(t *testing.T, cfg store.LayerConfig)
}

// storedAny returns the row of id, live or soft-deleted.
func (h *guardHarness) storedAny(t *testing.T, id string) store.LayerConfig {
	t.Helper()
	if cfg, ok := h.lookup(t, id); ok {
		return cfg
	}
	deleted, err := h.st.ListDeletedLayerConfigs(context.Background(), "t")
	if err != nil {
		t.Fatalf("ListDeletedLayerConfigs: %v", err)
	}
	for _, cfg := range deleted {
		if cfg.ID == id {
			return cfg
		}
	}
	t.Fatalf("no row holds %s", id)
	return store.LayerConfig{}
}

// seedThroughMembers returns a seed that registers "x" on credRepoReported
// with the fixture credential members as who, with any extra members.
func seedThroughMembers(who guardCaller, extra map[string]any) func(*testing.T, *guardHarness) {
	return func(t *testing.T, h *guardHarness) {
		h.actAs(who)
		h.seed(t, "x", credRepoReported, credMembers(extra))
	}
}

// seedLegacy returns a seed that writes cfg with registered as its stored repo.
func seedLegacy(cfg store.LayerConfig, registered string) func(*testing.T, *guardHarness) {
	return func(t *testing.T, h *guardHarness) { seedLegacyLayer(t, h.st, cfg, registered) }
}

// Spec: §7.3.1 (Re-registration with a reported repo) — a registration that
// sends back the reported repo of a git layer holding a credential keeps the
// stored registered value when both source types are git, the reported value
// matches byte for byte, the class and, on a user-defined layer, the owner are
// unchanged, and the request carries neither repo_token nor
// clear_repo_credential. Every other registration stores the repo it carries
// with the credential it carries. A [redacted] re-registration names a host
// path to go-git, so it runs on the admin arm of the local-source rule.
func TestLayerRegister_KeepsStoredRemote(t *testing.T) {
	t.Parallel()
	const (
		redacted  = "https://git.acme.com/acme/x.git@s3cr3tpw"
		xBare     = "https://git.acme.com/acme/x.git"
		otherBare = "https://git.acme.com/acme/other.git"
		sshStored = "ssh://git@git.acme.com/acme/x.git"
		sshBare   = "ssh://git.acme.com/acme/x.git"
		xFull     = "https://alice-user:s3cr3tpw@git.acme.com/acme/x.git"
	)
	adminDefinedLocal := localLegacyLayer("x")
	cases := []keepCase{
		// X6-0.7.0.
		{name: "legacy userinfo re-registered by its owner", seed: seedLegacy(aliceGitLayer("x"), credRepoFull),
			caller: guardAlice, repo: credRepoReported, status: http.StatusCreated, clone: credRepoFull, reported: credRepoReported},
		{name: "empty userinfo", seed: seedLegacy(legacyGitLayer("x"), "https://@git.acme.com/acme/x.git"),
			caller: guardAdmin, repo: xBare, status: http.StatusCreated, clone: "https://@git.acme.com/acme/x.git", reported: xBare},
		{name: "redacted class re-registered by a tenant admin", seed: seedLegacy(legacyGitLayer("x"), redacted),
			caller: guardAdmin, repo: "[redacted]", status: http.StatusCreated, clone: redacted, reported: "[redacted]"},
		// X6-0.7.0: the local-source rule refuses the owner without the admin role.
		{name: "redacted class re-registered by its non-admin owner", seed: seedLegacy(aliceGitLayer("x"), redacted),
			caller: guardAlice, repo: "[redacted]", status: http.StatusForbidden, clone: redacted},
		{name: "redacted class re-registered with another remote", seed: seedLegacy(legacyGitLayer("x"), redacted),
			caller: guardAdmin, repo: otherBare, status: http.StatusCreated, clone: otherBare, reported: otherBare},
		// X3-0.7.0.
		{name: "reported value with clear_repo_credential", seed: seedThroughMembers(guardAdmin, nil),
			caller: guardAdmin, repo: credRepoReported, extra: map[string]any{"clear_repo_credential": true},
			status: http.StatusCreated, clone: credRepoReported, reported: credRepoReported},
		{name: "reported value with a new token", seed: seedThroughMembers(guardAdmin, nil),
			caller: guardAdmin, repo: credRepoReported, extra: map[string]any{"repo_token": "n3wpw"},
			status: http.StatusCreated, clone: "https://:n3wpw@git.acme.com/acme/private.git", reported: credRepoReported},
		{name: "soft-deleted layer inside its recovery window",
			seed: func(t *testing.T, h *guardHarness) {
				seedThroughMembers(guardAdmin, nil)(t, h)
				resp, body := mustDelete(t, h.base, "/v1/layers?id=x")
				mustStatus(t, "unregister", resp, body, http.StatusOK)
			},
			caller: guardAdmin, repo: credRepoReported, status: http.StatusCreated, clone: credRepoFull, reported: credRepoReported},
		{name: "admin re-registers alice's layer as admin-defined", seed: seedThroughMembers(guardAlice, nil),
			caller: guardAdmin, repo: credRepoReported, extra: map[string]any{"public": true},
			status: http.StatusCreated, clone: credRepoReported, reported: credRepoReported,
			check: func(t *testing.T, cfg store.LayerConfig) {
				if cfg.UserDefined {
					t.Errorf("stored row = %+v, want an admin-defined layer", cfg)
				}
			}},
		{name: "admin re-registers alice's layer as user-defined", seed: seedThroughMembers(guardAlice, nil),
			caller: guardAdmin, repo: credRepoReported, extra: map[string]any{"user_defined": true},
			status: http.StatusCreated, clone: credRepoReported, reported: credRepoReported,
			check: func(t *testing.T, cfg store.LayerConfig) {
				if !cfg.UserDefined || cfg.Owner != "carol@acme.com" {
					t.Errorf("stored row = %+v, want a user-defined layer owned by carol@acme.com", cfg)
				}
			}},
		{name: "alice re-registers her own layer", seed: seedThroughMembers(guardAlice, nil),
			caller: guardAlice, repo: credRepoReported, status: http.StatusCreated, clone: credRepoFull, reported: credRepoReported},
		{name: "admin-defined layer with a different owner",
			seed:   seedThroughMembers(guardAdmin, map[string]any{"owner": "alice@acme.com"}),
			caller: guardAdmin, repo: credRepoReported, extra: map[string]any{"owner": "bob@acme.com"},
			status: http.StatusCreated, clone: credRepoFull, reported: credRepoReported,
			check: func(t *testing.T, cfg store.LayerConfig) {
				if cfg.Owner != "bob@acme.com" {
					t.Errorf("stored owner = %q, want bob@acme.com", cfg.Owner)
				}
			}},
		// X7-0.7.0.
		{name: "ssh login name", seed: seedLegacy(legacyGitLayer("x"), sshStored),
			caller: guardAdmin, repo: sshBare, status: http.StatusCreated, clone: sshBare, reported: sshBare},
		// D-12, Source type: the stored source type.
		{name: "local layer re-registered as git", seed: seedLegacy(adminDefinedLocal, xFull),
			caller: guardAdmin, repo: xBare, status: http.StatusCreated, clone: xBare, reported: xBare},
		// D-12, Source type: the request's source type.
		{name: "git layer re-registered as local", seed: seedThroughMembers(guardAdmin, nil),
			caller: guardAdmin, repo: credRepoReported, extra: map[string]any{"source_type": "local", "local_path": "/srv/x"},
			status: http.StatusCreated, clone: credRepoReported, reported: credRepoReported},
		// An unknown member is ignored, so a 0.6.0 client is kept.
		{name: "force_repo_overwrite is ignored", seed: seedThroughMembers(guardAdmin, nil),
			caller: guardAdmin, repo: credRepoReported, extra: map[string]any{"force_repo_overwrite": true},
			status: http.StatusCreated, clone: credRepoFull, reported: credRepoReported},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newGuardHarness(t, guardAdmin)
			tc.seed(t, h)
			before := h.storedAny(t, "x")
			h.actAs(tc.caller)
			resp, body := h.register(t, "x", tc.repo, tc.extra)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.status, body)
			}
			after := h.storedAny(t, "x")
			if got := after.CloneRepo(); got != tc.clone {
				t.Errorf("stored CloneRepo = %q, want %q", got, tc.clone)
			}
			if tc.status != http.StatusCreated {
				assertKeepRefusal(t, body, before, after)
				return
			}
			assertKeepAccepted(t, body, before, after, tc)
			if tc.check != nil {
				tc.check(t, after)
			}
		})
	}
}

// assertKeepRefusal asserts the local_source refusal of a [redacted]
// re-registration and that the stored webhook secret is unchanged.
func assertKeepRefusal(t *testing.T, body []byte, before, after store.LayerConfig) {
	t.Helper()
	if code, _, constraint := decodeErrorEnvelope(t, body); code != "auth.forbidden" || constraint != "local_source" {
		t.Errorf("code = %q, details.constraint = %q, want auth.forbidden and local_source", code, constraint)
	}
	if after.WebhookSecret != before.WebhookSecret {
		t.Errorf("a refused registration changed the webhook secret")
	}
}

// assertKeepAccepted asserts the reported repo of a 201 and the webhook secret
// the registration minted: a fresh one on a git layer and none on a local one.
func assertKeepAccepted(t *testing.T, body []byte, before, after store.LayerConfig, tc keepCase) {
	t.Helper()
	repo, _, secret := decodeRegisterResponse(t, body)
	if repo != tc.reported {
		t.Errorf("layer.repo = %q, want %q", repo, tc.reported)
	}
	if after.SourceType != "git" {
		if after.WebhookSecret != "" || secret != "" {
			t.Errorf("a %s registration minted a webhook secret", after.SourceType)
		}
		return
	}
	if secret == "" || after.WebhookSecret != secret || after.WebhookSecret == before.WebhookSecret {
		t.Errorf("webhook secret: response %t, stored matches %t; want a freshly minted secret",
			secret != "", after.WebhookSecret == secret)
	}
}
