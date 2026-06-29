package sync_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

// writeScope writes a sync.yaml (or sync.local.yaml) under dir/.podium.
func writeScope(t *testing.T, dir, name, content string) {
	t.Helper()
	pod := filepath.Join(dir, ".podium")
	if err := os.MkdirAll(pod, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pod, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func noEnv(string) string { return "" }

// Spec: §7.5.2 — workspace discovery walks up from CWD until a .podium/
// directory is found, mirroring how git finds .git.
func TestDiscoverWorkspace_WalksUp(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeScope(t, root, "sync.yaml", "defaults:\n  registry: /reg\n")
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, ok := sync.DiscoverWorkspace(nested)
	if !ok {
		t.Fatalf("expected to discover workspace from %s", nested)
	}
	// EvalSymlinks because t.TempDir on macOS lives under /var -> /private/var.
	if filepath.Clean(got) != filepath.Clean(root) {
		t.Errorf("workspace = %q, want %q", got, root)
	}
}

// Spec: §7.5.2 — no .podium/ above the start directory returns not found.
func TestDiscoverWorkspace_NoneFound(t *testing.T) {
	t.Parallel()
	if _, ok := sync.DiscoverWorkspace(t.TempDir()); ok {
		t.Errorf("expected no workspace in an empty temp dir")
	}
}

// Spec: §7.5.2 — per-key precedence: project-local > project-shared >
// user-global > built-in defaults. The user-global value is overridden by the
// project-shared, which is overridden by the project-local.
func TestLoadMergedConfig_Precedence(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	ws := t.TempDir()
	writeScope(t, home, "sync.yaml", "defaults:\n  registry: https://user-global\n  harness: codex\n")
	writeScope(t, ws, "sync.yaml", "defaults:\n  registry: https://project-shared\n")
	writeScope(t, ws, "sync.local.yaml", "defaults:\n  registry: https://project-local\n")

	merged, workspace, err := sync.LoadMergedConfig(ws, home)
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}
	if filepath.Clean(workspace) != filepath.Clean(ws) {
		t.Errorf("workspace = %q, want %q", workspace, ws)
	}
	if merged.Defaults.Registry != "https://project-local" {
		t.Errorf("registry = %q, want project-local to win", merged.Defaults.Registry)
	}
	// harness is set only in user-global, so it survives.
	if merged.Defaults.Harness != "codex" {
		t.Errorf("harness = %q, want codex from user-global", merged.Defaults.Harness)
	}
}

// Spec: §7.5.2 — profiles are an additive union across scopes; on a name
// collision the highest-precedence file wins entirely and the collision is
// reported.
func TestLoadMergedConfig_ProfileCollisionHighestWins(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	ws := t.TempDir()
	writeScope(t, home, "sync.yaml",
		"profiles:\n  shared-name:\n    include: [user/**]\n  only-user:\n    include: [u/**]\n")
	writeScope(t, ws, "sync.yaml",
		"profiles:\n  shared-name:\n    include: [project/**]\n")

	merged, _, err := sync.LoadMergedConfig(ws, home)
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}
	// Union: only-user survives from user-global.
	if _, ok := merged.Profiles["only-user"]; !ok {
		t.Errorf("only-user profile lost in union")
	}
	// Whole-profile overwrite: project-shared wins for shared-name.
	got := merged.Profiles["shared-name"].Include
	if len(got) != 1 || got[0] != "project/**" {
		t.Errorf("shared-name include = %v, want [project/**] (higher scope wins)", got)
	}
	// Collision recorded.
	if len(merged.Collisions["shared-name"]) < 2 {
		t.Errorf("shared-name collision not recorded: %+v", merged.Collisions)
	}
	if _, ok := merged.Collisions["only-user"]; ok {
		t.Errorf("only-user should not be a collision")
	}
}

// Spec: §7.5.2 — Resolve selects the active profile and merges its scope onto
// defaults; the profile's include/exclude/type become the scope.
func TestResolve_ProfileScope(t *testing.T) {
	t.Parallel()
	merged := &sync.MergedConfig{
		Defaults: sync.Defaults{Registry: "https://reg"},
		Profiles: map[string]sync.Profile{
			"finance": {Include: []string{"finance/**"}, Exclude: []string{"finance/legacy/**"}, Type: []string{"skill"}},
		},
	}
	got, err := sync.Resolve(sync.ResolveInput{Profile: "finance"}, merged, noEnv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Profile != "finance" {
		t.Errorf("Profile = %q, want finance", got.Profile)
	}
	if got.Registry != "https://reg" {
		t.Errorf("Registry = %q, want https://reg", got.Registry)
	}
	if len(got.Scope.Include) != 1 || got.Scope.Include[0] != "finance/**" {
		t.Errorf("Scope.Include = %v", got.Scope.Include)
	}
	if len(got.Scope.Types) != 1 || got.Scope.Types[0] != "skill" {
		t.Errorf("Scope.Types = %v", got.Scope.Types)
	}
}

// Spec: §7.5.2 — explicit CLI lists replace the profile's lists for the same
// field rather than appending.
func TestResolve_CLIOverridesProfile(t *testing.T) {
	t.Parallel()
	merged := &sync.MergedConfig{
		Profiles: map[string]sync.Profile{
			"finance": {Include: []string{"finance/**"}},
		},
	}
	got, err := sync.Resolve(sync.ResolveInput{
		Profile: "finance",
		Include: []string{"platform/**"},
	}, merged, noEnv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got.Scope.Include) != 1 || got.Scope.Include[0] != "platform/**" {
		t.Errorf("CLI include must replace profile include, got %v", got.Scope.Include)
	}
}

// Spec: §7.5.2 — precedence for registry: CLI flag > PODIUM_REGISTRY env >
// defaults.registry.
func TestResolve_RegistryPrecedence(t *testing.T) {
	t.Parallel()
	merged := &sync.MergedConfig{Defaults: sync.Defaults{Registry: "https://from-config"}}
	env := func(k string) string {
		if k == "PODIUM_REGISTRY" {
			return "https://from-env"
		}
		return ""
	}
	// env beats config.
	got, _ := sync.Resolve(sync.ResolveInput{}, merged, env)
	if got.Registry != "https://from-env" {
		t.Errorf("env should beat config, got %q", got.Registry)
	}
	// CLI beats env.
	got, _ = sync.Resolve(sync.ResolveInput{Registry: "https://from-cli"}, merged, env)
	if got.Registry != "https://from-cli" {
		t.Errorf("CLI should beat env, got %q", got.Registry)
	}
}

// Spec: §7.5.2 — invoking a profile defined in multiple scopes emits a
// collision warning; a non-colliding profile stays quiet.
func TestResolve_CollisionWarning(t *testing.T) {
	t.Parallel()
	// Build collisions through the public API by loading two scopes that
	// both define profile x.
	home := t.TempDir()
	ws := t.TempDir()
	writeScope(t, home, "sync.yaml", "profiles:\n  x:\n    include: [a]\n")
	writeScope(t, ws, "sync.yaml", "profiles:\n  x:\n    include: [b]\n  y:\n    include: [c]\n")
	m, _, err := sync.LoadMergedConfig(ws, home)
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}
	got, err := sync.Resolve(sync.ResolveInput{Profile: "x"}, m, noEnv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.CollisionWarning == "" {
		t.Errorf("expected collision warning for multiply-defined profile x")
	}
	// Non-colliding profile y is quiet.
	gotY, _ := sync.Resolve(sync.ResolveInput{Profile: "y"}, m, noEnv)
	if gotY.CollisionWarning != "" {
		t.Errorf("non-colliding profile must be quiet, got %q", gotY.CollisionWarning)
	}
}

// Spec: §7.5.2 — an explicit --profile that names a missing profile is an
// error (unresolved profile reference).
func TestResolve_UnknownExplicitProfileErrors(t *testing.T) {
	t.Parallel()
	_, err := sync.Resolve(sync.ResolveInput{Profile: "ghost"}, &sync.MergedConfig{}, noEnv)
	if !errors.Is(err, sync.ErrProfileNotFound) {
		t.Fatalf("got %v, want ErrProfileNotFound", err)
	}
}

// Spec: §7.5.2 — a stale defaults.profile that names nothing is ignored rather
// than fatal.
func TestResolve_StaleDefaultProfileIgnored(t *testing.T) {
	t.Parallel()
	merged := &sync.MergedConfig{Defaults: sync.Defaults{Profile: "gone", Registry: "https://r"}}
	got, err := sync.Resolve(sync.ResolveInput{}, merged, noEnv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Profile != "" {
		t.Errorf("stale default profile should resolve to no active profile, got %q", got.Profile)
	}
}

// Spec: §7.5.2 — `podium sync --config` plans one sync per targets: entry,
// each with its own target, harness, and scope (named profile or inline).
func TestPlanMultiTarget_PerEntryScope(t *testing.T) {
	t.Parallel()
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{Registry: "https://reg", Harness: "claude-code"},
		Profiles: map[string]sync.Profile{
			"project-default": {Include: []string{"finance/**"}},
		},
		Targets: []sync.TargetEntry{
			{ID: "claude", Target: "/out/claude", Profile: "project-default"},
			{ID: "codex", Harness: "codex", Target: "/out/codex", Include: []string{"shared/runbooks/**"}},
		},
	}
	plans, err := sync.PlanMultiTarget(cfg, sync.PlanInput{Workspace: "/ws"})
	if err != nil {
		t.Fatalf("PlanMultiTarget: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("got %d plans, want 2", len(plans))
	}
	// First target: named profile scope, defaults.harness.
	if plans[0].Harness != "claude-code" || plans[0].Target != "/out/claude" {
		t.Errorf("plan[0] = %+v", plans[0])
	}
	if len(plans[0].Scope.Include) != 1 || plans[0].Scope.Include[0] != "finance/**" {
		t.Errorf("plan[0] scope = %v", plans[0].Scope.Include)
	}
	// Second target: inline scope, per-target harness.
	if plans[1].Harness != "codex" {
		t.Errorf("plan[1] harness = %q, want codex", plans[1].Harness)
	}
	if len(plans[1].Scope.Include) != 1 || plans[1].Scope.Include[0] != "shared/runbooks/**" {
		t.Errorf("plan[1] scope = %v", plans[1].Scope.Include)
	}
	if plans[0].Registry != "https://reg" {
		t.Errorf("plan[0] registry = %q", plans[0].Registry)
	}
}

// Spec: §7.5.2 — a target naming an unresolved profile is an error.
func TestPlanMultiTarget_UnknownProfileErrors(t *testing.T) {
	t.Parallel()
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{Registry: "https://reg"},
		Targets:  []sync.TargetEntry{{ID: "x", Target: "/out", Profile: "ghost"}},
	}
	_, err := sync.PlanMultiTarget(cfg, sync.PlanInput{Workspace: "/ws"})
	if !errors.Is(err, sync.ErrProfileNotFound) {
		t.Fatalf("got %v, want ErrProfileNotFound", err)
	}
}
