package sync_test

import (
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

// TestReadConfig_KindDefaultsToWorkspaceWhenOmitted asserts that a target entry
// written without a `kind:` field reads back with an empty Kind, the value
// §7.5.2 treats as the default workspace output, so a sync.yaml that predates
// the kind discriminant keeps materializing the project-files layout.
func TestReadConfig_KindDefaultsToWorkspaceWhenOmitted(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{Registry: "https://r.example"},
		Targets: []sync.TargetEntry{
			{ID: "claude-code", Harness: "claude-code", Target: "~/.claude/", Profile: "project-default"},
		},
	}
	if err := sync.WriteConfig(ws, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	got, err := sync.ReadConfig(ws)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if len(got.Targets) != 1 {
		t.Fatalf("targets len = %d, want 1", len(got.Targets))
	}
	if got.Targets[0].Kind != "" {
		t.Errorf("targets[0].kind = %q, want empty (the workspace default)", got.Targets[0].Kind)
	}
}

// TestReadConfig_KindParsesWorkspaceAndMarketplace asserts that an explicit
// `kind:` field reads back verbatim for both values §7.5.2 defines, so the
// dispatch path can distinguish a workspace target from a marketplace target.
func TestReadConfig_KindParsesWorkspaceAndMarketplace(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	cfg := &sync.SyncConfig{
		Targets: []sync.TargetEntry{
			{ID: "ws", Kind: "workspace", Harness: "claude-code"},
			{ID: "mp", Kind: "marketplace", Harnesses: []string{"claude-code"}},
		},
	}
	if err := sync.WriteConfig(ws, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	got, err := sync.ReadConfig(ws)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("targets len = %d, want 2", len(got.Targets))
	}
	if got.Targets[0].Kind != "workspace" {
		t.Errorf("targets[0].kind = %q, want workspace", got.Targets[0].Kind)
	}
	if got.Targets[1].Kind != "marketplace" {
		t.Errorf("targets[1].kind = %q, want marketplace", got.Targets[1].Kind)
	}
}

// TestMarketplaceTarget_RoundTrips writes a sync.yaml carrying a
// `defaults.identity` and a `kind: marketplace` target through WriteConfig, reads
// it back through ReadConfig, and asserts every field S2 adds to TargetEntry and
// Defaults survives the round-trip (§7.5.2): the kind discriminant, the harness
// set, the git remote and branch, the commit message, the marketplace identity,
// the plugin filter, the workflow, and the inherited defaults.identity.
func TestMarketplaceTarget_RoundTrips(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{
			Registry: "https://podium.acme.com",
			Identity: "publisher@acme.com",
		},
		Targets: []sync.TargetEntry{
			{
				ID:            "acme-agents",
				Kind:          "marketplace",
				Target:        "./build/acme-agents",
				Harnesses:     []string{"claude-code", "codex", "cursor"},
				Git:           sync.GitRemote{Remote: "git@github.com:acme/agent-marketplace.git", Branch: "main"},
				CommitMessage: "Sync Podium catalog ({{.ChangedCount}}) {{.Timestamp}}",
				Identity:      "marketplace@acme.com",
				Plugins: []sync.PluginFilter{
					{
						Name:        "finance-pack",
						Description: "Accounts-payable skills and commands.",
						Include:     []string{"finance/**"},
						Exclude:     []string{"finance/experimental/**"},
						Type:        []string{"skill", "command"},
					},
				},
				Workflow: sync.Workflow{
					Prepare: []sync.Command{{Run: []string{"git", "clone", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"}}},
					Publish: []sync.Command{
						{Run: []string{"git", "-C", "$PODIUM_WORKDIR", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"}, SkipIfNoChanges: true},
						{Run: []string{"git", "-C", "$PODIUM_WORKDIR", "push", "origin", "$PODIUM_GIT_BRANCH"}},
					},
				},
			},
		},
	}

	if err := sync.WriteConfig(ws, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	got, err := sync.ReadConfig(ws)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}

	if got.Defaults.Identity != "publisher@acme.com" {
		t.Errorf("defaults.identity = %q, want publisher@acme.com", got.Defaults.Identity)
	}
	if len(got.Targets) != 1 {
		t.Fatalf("targets len = %d, want 1", len(got.Targets))
	}
	if !reflect.DeepEqual(got.Targets[0], cfg.Targets[0]) {
		t.Errorf("round-trip target = %+v, want %+v", got.Targets[0], cfg.Targets[0])
	}
}
