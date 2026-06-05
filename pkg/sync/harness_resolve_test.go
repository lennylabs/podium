package sync_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

// Spec: §7.5.2 — harness resolves per key by precedence: CLI flag, then
// PODIUM_HARNESS, then the active profile's harness, then defaults.harness,
// then the built-in "none" adapter.
func TestResolve_HarnessPrecedence(t *testing.T) {
	t.Parallel()
	merged := &sync.MergedConfig{
		Defaults: sync.Defaults{Harness: "codex", Profile: "team"},
		Profiles: map[string]sync.Profile{
			"team": {Harness: "claude-code"},
			"bare": {},
		},
	}
	env := func(k string) string {
		if k == "PODIUM_HARNESS" {
			return "cursor"
		}
		return ""
	}

	cases := []struct {
		name string
		in   sync.ResolveInput
		env  func(string) string
		want string
	}{
		{"cli flag wins", sync.ResolveInput{Harness: "gemini", Profile: "team"}, env, "gemini"},
		{"env over profile", sync.ResolveInput{Profile: "team"}, env, "cursor"},
		{"profile over defaults", sync.ResolveInput{Profile: "team"}, noEnv, "claude-code"},
		{"defaults when profile bare", sync.ResolveInput{Profile: "bare"}, noEnv, "codex"},
		{"none when nothing set", sync.ResolveInput{Profile: "bare"}, noEnv, "codex"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sync.Resolve(c.in, merged, c.env)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.Harness != c.want {
				t.Errorf("Harness = %q, want %q", got.Harness, c.want)
			}
		})
	}
}

// Spec: §7.5.2 — with nothing configured anywhere, the harness falls back to
// the built-in "none" adapter.
func TestResolve_HarnessDefaultsToNone(t *testing.T) {
	t.Parallel()
	got, err := sync.Resolve(sync.ResolveInput{}, &sync.MergedConfig{}, noEnv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Harness != "none" {
		t.Errorf("Harness = %q, want none", got.Harness)
	}
}
