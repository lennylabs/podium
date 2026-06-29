package sync_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

// TestPlanMultiTarget_MixedKinds asserts that PlanMultiTarget flattens a config
// holding a kind: workspace target and a kind: marketplace target into one plan
// each, the workspace plan carries its scope and harness, and the marketplace
// plan carries its git remote, harness set, plugins, and resolved identity while
// skipping the workspace scope resolution (§7.5.2).
func TestPlanMultiTarget_MixedKinds(t *testing.T) {
	t.Parallel()
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{Registry: "https://reg", Harness: "claude-code", Identity: "publisher@acme.com"},
		Profiles: map[string]sync.Profile{
			"project-default": {Include: []string{"finance/**"}},
		},
		Targets: []sync.TargetEntry{
			{ID: "ws", Kind: "workspace", Target: "/out/ws", Profile: "project-default"},
			{
				ID:            "mp",
				Kind:          "marketplace",
				Target:        "/out/mp",
				Harnesses:     []string{"claude-code", "codex"},
				Git:           sync.GitRemote{Remote: "git@github.com:acme/m.git", Branch: "main"},
				CommitMessage: "Sync {{.ChangedCount}}",
				Plugins:       []sync.PluginFilter{{Name: "finance", Include: []string{"finance/**"}}},
			},
		},
	}
	plans, err := sync.PlanMultiTarget(cfg, sync.PlanInput{Workspace: "/ws"})
	if err != nil {
		t.Fatalf("PlanMultiTarget: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("got %d plans, want 2", len(plans))
	}

	ws := plans[0]
	if ws.Kind != sync.KindWorkspace {
		t.Errorf("plan[0].Kind = %q, want workspace", ws.Kind)
	}
	if ws.Harness != "claude-code" {
		t.Errorf("plan[0].Harness = %q, want claude-code", ws.Harness)
	}
	if !reflect.DeepEqual(ws.Scope.Include, []string{"finance/**"}) {
		t.Errorf("plan[0].Scope.Include = %v, want [finance/**]", ws.Scope.Include)
	}

	mp := plans[1]
	if mp.Kind != sync.KindMarketplace {
		t.Errorf("plan[1].Kind = %q, want marketplace", mp.Kind)
	}
	if mp.Git.Remote != "git@github.com:acme/m.git" || mp.Git.Branch != "main" {
		t.Errorf("plan[1].Git = %+v, want remote/main", mp.Git)
	}
	if !reflect.DeepEqual(mp.Harnesses, []string{"claude-code", "codex"}) {
		t.Errorf("plan[1].Harnesses = %v, want [claude-code codex]", mp.Harnesses)
	}
	if mp.CommitMessage != "Sync {{.ChangedCount}}" {
		t.Errorf("plan[1].CommitMessage = %q", mp.CommitMessage)
	}
	if len(mp.Plugins) != 1 || mp.Plugins[0].Name != "finance" {
		t.Errorf("plan[1].Plugins = %+v, want one finance plugin", mp.Plugins)
	}
	// Identity inherits defaults.identity when the entry sets none of its own.
	if mp.Identity != "publisher@acme.com" {
		t.Errorf("plan[1].Identity = %q, want publisher@acme.com (inherited)", mp.Identity)
	}
	// A marketplace plan skips the workspace scope resolution; it must not carry
	// a workspace harness or scope (§7.5.2).
	if mp.Harness != "" {
		t.Errorf("plan[1].Harness = %q, want empty (marketplace skips workspace harness)", mp.Harness)
	}
	if len(mp.Scope.Include) != 0 || len(mp.Scope.Exclude) != 0 || len(mp.Scope.Types) != 0 {
		t.Errorf("plan[1].Scope = %+v, want empty (marketplace skips scope resolution)", mp.Scope)
	}
}

// TestPlanMultiTarget_MarketplaceIdentityOverride asserts that a marketplace
// target's own identity field wins over defaults.identity (§7.5.2 Decision 6).
func TestPlanMultiTarget_MarketplaceIdentityOverride(t *testing.T) {
	t.Parallel()
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{Registry: "https://reg", Identity: "publisher@acme.com"},
		Targets: []sync.TargetEntry{
			{ID: "mp", Kind: "marketplace", Target: "/out", Harnesses: []string{"claude-code"}, Identity: "release@acme.com"},
		},
	}
	plans, err := sync.PlanMultiTarget(cfg, sync.PlanInput{Workspace: "/ws"})
	if err != nil {
		t.Fatalf("PlanMultiTarget: %v", err)
	}
	if plans[0].Identity != "release@acme.com" {
		t.Errorf("plan[0].Identity = %q, want release@acme.com (entry overrides defaults)", plans[0].Identity)
	}
}

// TestPlanMultiTarget_WorkspaceRejectsMarketplaceFields asserts that a kind:
// workspace target carrying any marketplace field is rejected with
// ErrConfigInvalid (§7.5.2).
func TestPlanMultiTarget_WorkspaceRejectsMarketplaceFields(t *testing.T) {
	t.Parallel()
	cases := map[string]sync.TargetEntry{
		"harnesses":      {ID: "x", Kind: "workspace", Target: "/o", Harnesses: []string{"claude-code"}},
		"git":            {ID: "x", Kind: "workspace", Target: "/o", Git: sync.GitRemote{Remote: "r"}},
		"commit_message": {ID: "x", Kind: "workspace", Target: "/o", CommitMessage: "m"},
		"identity":       {ID: "x", Kind: "workspace", Target: "/o", Identity: "a@b"},
		"plugins":        {ID: "x", Kind: "workspace", Target: "/o", Plugins: []sync.PluginFilter{{Name: "p"}}},
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := &sync.SyncConfig{
				Defaults: sync.Defaults{Registry: "https://reg"},
				Targets:  []sync.TargetEntry{entry},
			}
			_, err := sync.PlanMultiTarget(cfg, sync.PlanInput{Workspace: "/ws"})
			if !errors.Is(err, sync.ErrConfigInvalid) {
				t.Fatalf("got %v, want ErrConfigInvalid", err)
			}
		})
	}
}

// TestPlanMultiTarget_WorkspaceDefaultKindRejectsMarketplaceFields asserts that
// an omitted kind defaults to workspace and still rejects marketplace fields, so
// a target cannot smuggle marketplace fields in by leaving kind unset (§7.5.2).
func TestPlanMultiTarget_WorkspaceDefaultKindRejectsMarketplaceFields(t *testing.T) {
	t.Parallel()
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{Registry: "https://reg"},
		Targets:  []sync.TargetEntry{{ID: "x", Target: "/o", Harnesses: []string{"claude-code"}}},
	}
	_, err := sync.PlanMultiTarget(cfg, sync.PlanInput{Workspace: "/ws"})
	if !errors.Is(err, sync.ErrConfigInvalid) {
		t.Fatalf("got %v, want ErrConfigInvalid", err)
	}
}

// TestPlanMultiTarget_MarketplaceRejectsWorkspaceScopeFields asserts that a kind:
// marketplace target carrying any workspace scope field is rejected with
// ErrConfigInvalid (§7.5.2).
func TestPlanMultiTarget_MarketplaceRejectsWorkspaceScopeFields(t *testing.T) {
	t.Parallel()
	cases := map[string]sync.TargetEntry{
		"harness": {ID: "x", Kind: "marketplace", Target: "/o", Harnesses: []string{"claude-code"}, Harness: "claude-code"},
		"profile": {ID: "x", Kind: "marketplace", Target: "/o", Harnesses: []string{"claude-code"}, Profile: "p"},
		"include": {ID: "x", Kind: "marketplace", Target: "/o", Harnesses: []string{"claude-code"}, Include: []string{"a/**"}},
		"exclude": {ID: "x", Kind: "marketplace", Target: "/o", Harnesses: []string{"claude-code"}, Exclude: []string{"a/**"}},
		"type":    {ID: "x", Kind: "marketplace", Target: "/o", Harnesses: []string{"claude-code"}, Type: []string{"skill"}},
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := &sync.SyncConfig{
				Defaults: sync.Defaults{Registry: "https://reg"},
				Targets:  []sync.TargetEntry{entry},
			}
			_, err := sync.PlanMultiTarget(cfg, sync.PlanInput{Workspace: "/ws"})
			if !errors.Is(err, sync.ErrConfigInvalid) {
				t.Fatalf("got %v, want ErrConfigInvalid", err)
			}
		})
	}
}

// TestPlanMultiTarget_MarketplaceRejectsWatch asserts that a kind: marketplace
// target under --watch is rejected with ErrConfigInvalid (§7.5.2, §7.5.4).
func TestPlanMultiTarget_MarketplaceRejectsWatch(t *testing.T) {
	t.Parallel()
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{Registry: "https://reg"},
		Targets:  []sync.TargetEntry{{ID: "mp", Kind: "marketplace", Target: "/o", Harnesses: []string{"claude-code"}}},
	}
	_, err := sync.PlanMultiTarget(cfg, sync.PlanInput{Workspace: "/ws", Watch: true})
	if !errors.Is(err, sync.ErrConfigInvalid) {
		t.Fatalf("got %v, want ErrConfigInvalid", err)
	}
}

// TestPlanMultiTarget_MarketplaceRejectsOverride asserts that a kind:
// marketplace target run with an ephemeral override is rejected with
// ErrConfigInvalid (§7.5.2, §7.5.5).
func TestPlanMultiTarget_MarketplaceRejectsOverride(t *testing.T) {
	t.Parallel()
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{Registry: "https://reg"},
		Targets:  []sync.TargetEntry{{ID: "mp", Kind: "marketplace", Target: "/o", Harnesses: []string{"claude-code"}}},
	}
	_, err := sync.PlanMultiTarget(cfg, sync.PlanInput{Workspace: "/ws", Override: true})
	if !errors.Is(err, sync.ErrConfigInvalid) {
		t.Fatalf("got %v, want ErrConfigInvalid", err)
	}
}

// TestPlanMultiTarget_MarketplaceRejectsNonPublishHarness asserts that a
// marketplace harness set naming opencode or none is rejected with
// ErrConfigInvalid via adapter.EmitterForHarness (§7.8).
func TestPlanMultiTarget_MarketplaceRejectsNonPublishHarness(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"opencode", "none"} {
		t.Run(h, func(t *testing.T) {
			t.Parallel()
			cfg := &sync.SyncConfig{
				Defaults: sync.Defaults{Registry: "https://reg"},
				Targets: []sync.TargetEntry{
					{ID: "mp", Kind: "marketplace", Target: "/o", Harnesses: []string{"claude-code", h}},
				},
			}
			_, err := sync.PlanMultiTarget(cfg, sync.PlanInput{Workspace: "/ws"})
			if !errors.Is(err, sync.ErrConfigInvalid) {
				t.Fatalf("got %v, want ErrConfigInvalid", err)
			}
		})
	}
}

// TestPlanMultiTarget_WorkflowAcceptedOnBothKinds asserts that a workflow is
// accepted on a kind: workspace target and on a kind: marketplace target and is
// carried into the resulting plan (§7.5.2 Decision 3).
func TestPlanMultiTarget_WorkflowAcceptedOnBothKinds(t *testing.T) {
	t.Parallel()
	wf := sync.Workflow{
		Prepare: []sync.Command{{Run: []string{"true"}}},
		Publish: []sync.Command{{Run: []string{"true"}}},
	}
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{Registry: "https://reg", Harness: "claude-code"},
		Targets: []sync.TargetEntry{
			{ID: "ws", Kind: "workspace", Target: "/out/ws", Workflow: wf},
			{ID: "mp", Kind: "marketplace", Target: "/out/mp", Harnesses: []string{"claude-code"}, Workflow: wf},
		},
	}
	plans, err := sync.PlanMultiTarget(cfg, sync.PlanInput{Workspace: "/ws"})
	if err != nil {
		t.Fatalf("PlanMultiTarget: %v", err)
	}
	for i, p := range plans {
		if !reflect.DeepEqual(p.Workflow, wf) {
			t.Errorf("plan[%d].Workflow = %+v, want %+v", i, p.Workflow, wf)
		}
	}
}

// TestPlanMultiTarget_UnknownKindRejected asserts that a target naming a kind
// other than workspace or marketplace is rejected with ErrConfigInvalid
// (§7.5.2).
func TestPlanMultiTarget_UnknownKindRejected(t *testing.T) {
	t.Parallel()
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{Registry: "https://reg"},
		Targets:  []sync.TargetEntry{{ID: "x", Kind: "registry", Target: "/o"}},
	}
	_, err := sync.PlanMultiTarget(cfg, sync.PlanInput{Workspace: "/ws"})
	if !errors.Is(err, sync.ErrConfigInvalid) {
		t.Fatalf("got %v, want ErrConfigInvalid", err)
	}
}
