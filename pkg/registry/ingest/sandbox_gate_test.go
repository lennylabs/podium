package ingest_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

func sandboxArtifact(profile string) string {
	fm := "---\ntype: context\nversion: 1.0.0\ndescription: A sandboxed widget.\nsensitivity: low\n"
	if profile != "" {
		fm += "sandbox_profile: " + profile + "\n"
	}
	return fm + "---\n\nbody\n"
}

// spec: §13.10 — with PODIUM_ENFORCE_SANDBOX_PROFILE=true (Request.EnforceSandboxProfile)
// the registry refuses to ingest an artifact whose sandbox_profile cannot be
// honored by the local host (PODIUM_HOST_SANDBOXES); the empty profile and
// unrestricted are always enforceable. Off by default, sandbox_profile is
// informational and never blocks ingest.
func TestIngest_SandboxProfileGate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		profile     string
		enforce     bool
		enforceable []string
		wantReject  bool
	}{
		{"enforce off keeps restrictive profile informational", "seccomp-strict", false, []string{"unrestricted"}, false},
		{"enforce on accepts unrestricted", "unrestricted", true, []string{"unrestricted"}, false},
		{"enforce on accepts empty profile", "", true, []string{"unrestricted"}, false},
		{"enforce on accepts a profile the host honors", "seccomp-strict", true, []string{"unrestricted", "seccomp-strict"}, false},
		{"enforce on rejects an unenforceable profile", "seccomp-strict", true, []string{"unrestricted"}, true},
		{"enforce on with default host set rejects restrictive", "network-isolated", true, nil, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := store.NewMemory()
			if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
				t.Fatalf("CreateTenant: %v", err)
			}
			res, err := ingest.Ingest(context.Background(), st, ingest.Request{
				TenantID: "t", LayerID: "L",
				Files: fstest.MapFS{
					"eng/widget/ARTIFACT.md": &fstest.MapFile{Data: []byte(sandboxArtifact(tc.profile))},
				},
				EnforceSandboxProfile:      tc.enforce,
				EnforceableSandboxProfiles: tc.enforceable,
			})
			if err != nil {
				t.Fatalf("Ingest: %v", err)
			}
			if tc.wantReject {
				if res.Accepted != 0 {
					t.Errorf("Accepted = %d, want 0 (rejected)", res.Accepted)
				}
				if len(res.Rejected) != 1 {
					t.Fatalf("Rejected = %d, want 1: %+v", len(res.Rejected), res.Rejected)
				}
				if got := res.Rejected[0].Code; got != "ingest.sandbox_profile_unenforceable" {
					t.Errorf("Rejected code = %q, want ingest.sandbox_profile_unenforceable", got)
				}
			} else if res.Accepted != 1 {
				t.Errorf("Accepted = %d, want 1; rejects=%+v lint=%+v", res.Accepted, res.Rejected, res.LintFailures)
			}
		})
	}
}
