package server

import (
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.3.1 (Repository credentials) — reportedRepo redacts the bytes a
// clone uses exactly once: Repo for a config the store did not split, and the
// registered URL for one it did. The last row pairs a registered URL with a
// Repo that disagrees with it and still carries userinfo, which pins that the
// helper reads the registered bytes and never returns Repo as it stands.
func TestReportedRepo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  store.LayerConfig
		want string
	}{
		{"unsplit URL with userinfo", store.LayerConfig{Repo: "https://ghp_tok3n@host/x.git"}, "https://host/x.git"},
		{"unsplit fail-closed value", store.LayerConfig{Repo: "https://ghp_tok/3n@host/x.git"}, "[redacted]"},
		{"registered URL beside a disagreeing Repo", store.LayerConfig{
			Repo:           "https://alice-user:s3cr3tpw@host/y.git",
			RegisteredRepo: store.RegisteredRepo("https://ghp_tok3n@host/x.git"),
		}, "https://host/x.git"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := reportedRepo(tc.cfg); got != tc.want {
				t.Errorf("reportedRepo = %q, want %q", got, tc.want)
			}
			if got := wireLayer(tc.cfg).Repo; got != tc.want {
				t.Errorf("wireLayer(...).Repo = %q, want %q", got, tc.want)
			}
		})
	}
}
