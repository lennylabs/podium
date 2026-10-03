package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §6.3.1, §13.12 — a set PODIUM_SCIM_STORE_PATH that the registry
// cannot read or parse refuses startup with config.scim_store_unavailable and
// names the path, whether or not PODIUM_SCIM_TOKENS mounts the receiver. The
// refusal replaces the former in-memory fallback, which discarded every
// provisioned user at the next restart without telling the operator.
// Matrix: §6.10 (config.scim_store_unavailable)
func TestSCIMStore_UnusablePathRefusesStart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		path  func(t *testing.T) string
		extra []string
	}{
		{
			name: "unparseable file with receiver mounted",
			path: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "scim.json")
				if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
					t.Fatalf("write store: %v", err)
				}
				return p
			},
			extra: []string{"PODIUM_SCIM_TOKENS=tok"},
		},
		{
			name: "existing directory with receiver unmounted",
			path: func(t *testing.T) string {
				return t.TempDir()
			},
			// An empty value overrides an inherited token, which the
			// registry reads the same as an unset variable.
			extra: []string{"PODIUM_SCIM_TOKENS="},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := tc.path(t)
			env := append([]string{"PODIUM_SCIM_STORE_PATH=" + path}, tc.extra...)
			out := gwExpectStartupFailure(t, "config.scim_store_unavailable", env...)
			if !strings.Contains(out, path) {
				t.Errorf("refusal output does not name %s:\n%s", path, out)
			}
		})
	}
}
