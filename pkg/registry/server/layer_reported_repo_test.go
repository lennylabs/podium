package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.3.1 (Repository credentials) — reportedRepo redacts the bytes a
// clone uses exactly once: Repo for a config the store did not split, and the
// registered URL for one it did. The third row pairs a registered URL with a
// Repo that disagrees with it and still carries userinfo, which pins that the
// helper reads the registered bytes and never returns Repo as it stands.
//
// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) —
// wireLayer carries repo_credential where credentialApplies holds for the
// stored source type and the registered value, true when that value carries
// userinfo, and omits it everywhere else. The [redacted] row fails without the
// TakesCredential conjunct, the plain-URL row fails without
// CarriesURLCredential, the local row with a credential Repo fails without the
// source-type conjunct, and the hostless row fails on a member computed from
// the reported repo.
func TestWireLayer(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	git := func(repo string) store.LayerConfig { return store.LayerConfig{SourceType: "git", Repo: repo} }
	cases := []struct {
		name string
		cfg  store.LayerConfig
		want string
		// credential is the repo_credential wireLayer carries, nil where the
		// layer object omits the member.
		credential *bool
	}{
		{"unsplit URL with userinfo", git("https://ghp_tok3n@host/x.git"), "https://host/x.git", &yes},
		{"unsplit fail-closed value", git("https://ghp_tok/3n@host/x.git"), "[redacted]", nil},
		{"registered URL beside a disagreeing Repo", store.LayerConfig{
			SourceType:     "git",
			Repo:           "https://alice-user:s3cr3tpw@host/y.git",
			RegisteredRepo: store.RegisteredRepo("https://ghp_tok3n@host/x.git"),
		}, "https://host/x.git", &yes},
		{"split registered URL", store.LayerConfig{
			SourceType:     "git",
			Repo:           "https://host/x.git",
			RegisteredRepo: store.RegisteredRepo("https://alice-user:s3cr3tpw@host/x.git"),
		}, "https://host/x.git", &yes},
		{"split empty userinfo", store.LayerConfig{
			SourceType:     "git",
			Repo:           "https://host/x.git",
			RegisteredRepo: store.RegisteredRepo("https://@host/x.git"),
		}, "https://host/x.git", &yes},
		{"upper-case scheme", git("HTTPS://tok@Host/x.git"), "https://Host/x.git", &yes},
		{"plain URL", git("https://host/x.git"), "https://host/x.git", &no},
		{"ssh login name", store.LayerConfig{
			SourceType:     "git",
			Repo:           "ssh://host/x.git",
			RegisteredRepo: store.RegisteredRepo("ssh://git@host/x.git"),
		}, "ssh://host/x.git", nil},
		{"scp-like", git("git@github.com:acme/x.git"), "git@github.com:acme/x.git", nil},
		{"local", store.LayerConfig{SourceType: "local", LocalPath: "/srv/x"}, "", nil},
		{"local with a credential repo", store.LayerConfig{
			SourceType: "local", LocalPath: "/srv/x", Repo: "https://alice-user:s3cr3tpw@host/x.git",
		}, "https://host/x.git", nil},
		{"literal [redacted]", git("[redacted]"), "[redacted]", nil},
		{"hostless userinfo", git("https://tok@"), "https:", &yes},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := reportedRepo(tc.cfg); got != tc.want {
				t.Errorf("reportedRepo = %q, want %q", got, tc.want)
			}
			obj := wireLayer(tc.cfg)
			if obj.Repo != tc.want {
				t.Errorf("wireLayer(...).Repo = %q, want %q", obj.Repo, tc.want)
			}
			assertRepoCredential(t, obj, tc.credential)
		})
	}
}

// assertRepoCredential fails unless obj carries want as repo_credential, and,
// for a nil want, unless the marshaled object omits the member.
func assertRepoCredential(t *testing.T, obj LayerObject, want *bool) {
	t.Helper()
	got := obj.RepoCredential
	switch {
	case want == nil && got != nil:
		t.Errorf("RepoCredential = %t, want the member omitted", *got)
	case want != nil && got == nil:
		t.Errorf("RepoCredential omitted, want %t", *want)
	case want != nil && *got != *want:
		t.Errorf("RepoCredential = %t, want %t", *got, *want)
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal the layer object: %v", err)
	}
	if carried := strings.Contains(string(raw), `"repo_credential"`); carried != (want != nil) {
		t.Errorf("marshaled object carries repo_credential %t, want %t: %s", carried, want != nil, raw)
	}
}
