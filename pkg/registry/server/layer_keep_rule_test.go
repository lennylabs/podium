package server

import (
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.3.1 (Re-registration with a reported repo) — keepsStoredRemote
// holds for a git re-registration of a git layer holding a credential that
// sends back the reported repo, resolves to the stored class and, on a
// user-defined layer, the stored owner, and carries neither repo_token nor
// clear_repo_credential. Each row breaks one condition and must not keep. The
// register handler tests a request's repo_token before it calls the
// predicate, so the token row is reachable here alone.
func TestKeepsStoredRemote_EachCondition(t *testing.T) {
	t.Parallel()
	const registered = "https://alice-user:s3cr3tpw@git.acme.com/acme/x.git"
	base := func() (store.LayerConfig, LayerRegisterRequest, store.LayerConfig) {
		stored := store.LayerConfig{SourceType: "git", Repo: registered, UserDefined: true, Owner: "alice@acme.com"}
		req := LayerRegisterRequest{SourceType: "git", Repo: "https://git.acme.com/acme/x.git"}
		cfg := store.LayerConfig{SourceType: "git", UserDefined: true, Owner: "alice@acme.com"}
		return stored, req, cfg
	}
	if stored, req, cfg := base(); !keepsStoredRemote(stored, req, cfg) {
		t.Fatalf("keepsStoredRemote on the base case = false, want true")
	}
	cases := []struct {
		name   string
		mutate func(stored *store.LayerConfig, req *LayerRegisterRequest, cfg *store.LayerConfig)
	}{
		{"stored local source", func(s *store.LayerConfig, _ *LayerRegisterRequest, _ *store.LayerConfig) { s.SourceType = "local" }},
		{"request local source", func(_ *store.LayerConfig, r *LayerRegisterRequest, _ *store.LayerConfig) { r.SourceType = "local" }},
		{"stored repo holds no credential", func(s *store.LayerConfig, r *LayerRegisterRequest, _ *store.LayerConfig) {
			s.Repo = r.Repo
		}},
		{"another remote", func(_ *store.LayerConfig, r *LayerRegisterRequest, _ *store.LayerConfig) {
			r.Repo = "https://git.acme.com/acme/other.git"
		}},
		{"another class", func(_ *store.LayerConfig, _ *LayerRegisterRequest, c *store.LayerConfig) {
			c.UserDefined, c.Owner = false, ""
		}},
		{"another owner", func(_ *store.LayerConfig, _ *LayerRegisterRequest, c *store.LayerConfig) { c.Owner = "bob@acme.com" }},
		{"repo_token", func(_ *store.LayerConfig, r *LayerRegisterRequest, _ *store.LayerConfig) { r.RepoToken = "n3wpw" }},
		{"clear_repo_credential", func(_ *store.LayerConfig, r *LayerRegisterRequest, _ *store.LayerConfig) {
			r.ClearRepoCredential = true
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stored, req, cfg := base()
			tc.mutate(&stored, &req, &cfg)
			if keepsStoredRemote(stored, req, cfg) {
				t.Errorf("keepsStoredRemote = true, want false")
			}
		})
	}
}
