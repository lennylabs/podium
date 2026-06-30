package sync

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// mixedKindYAML is a sync.yaml carrying a `kind: workspace` target and a
// `kind: marketplace` target. Parsing it through ReadConfigFile exercises the
// kind discriminant, the workspace scope fields, and the marketplace fields the
// component types decode: the git remote and branch, the harness set, the
// plugins, the publishing identity, and the workflow.
const mixedKindYAML = `defaults:
  registry: https://podium.acme.com
  identity: publisher@acme.com

targets:
  - id: claude-code
    kind: workspace
    harness: claude-code
    target: ~/.claude/
    profile: project-default

  - id: acme-agents
    kind: marketplace
    harnesses: [claude-code, codex, cursor]
    target: ./build/acme-agents
    git:
      remote: git@github.com:acme/agent-marketplace.git
      branch: main
    commit_message: "Sync Podium catalog ({{.ChangedCount}}) {{.Timestamp}}"
    identity: marketplace@acme.com
    plugins:
      - name: finance-pack
        description: Accounts-payable skills and commands.
        include: ["finance/**"]
        exclude: ["finance/experimental/**"]
        type: [skill, command]
    workflow:
      prepare:
        - run: ["git", "clone", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"]
      publish:
        - run: ["git", "-C", "$PODIUM_WORKDIR", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"]
          skip_if_no_changes: true
        - run: ["git", "-C", "$PODIUM_WORKDIR", "push", "origin", "$PODIUM_GIT_BRANCH"]
`

// writeSyncConfig writes content to a workspace's .podium/sync.yaml and returns
// its path.
func writeSyncConfig(t *testing.T, dir, content string) string {
	t.Helper()
	podiumDir := filepath.Join(dir, ".podium")
	if err := os.MkdirAll(podiumDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", podiumDir, err)
	}
	path := filepath.Join(podiumDir, "sync.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write sync.yaml: %v", err)
	}
	return path
}

// TestReadConfigFile_MixedKindTargets parses a sync.yaml carrying a
// `kind: workspace` target and a `kind: marketplace` target through
// ReadConfigFile, and asserts the marketplace target's git remote and branch,
// harness set, plugins, identity, and workflow decode into the relocated
// component types (§7.5.2).
func TestReadConfigFile_MixedKindTargets(t *testing.T) {
	path := writeSyncConfig(t, t.TempDir(), mixedKindYAML)

	cfg, err := ReadConfigFile(path)
	if err != nil {
		t.Fatalf("ReadConfigFile: %v", err)
	}
	if got := cfg.Defaults.Identity; got != "publisher@acme.com" {
		t.Errorf("defaults.identity = %q, want publisher@acme.com", got)
	}
	if got := len(cfg.Targets); got != 2 {
		t.Fatalf("targets len = %d, want 2", got)
	}

	ws := cfg.Targets[0]
	if ws.Kind != "workspace" {
		t.Errorf("targets[0].kind = %q, want workspace", ws.Kind)
	}
	if ws.Harness != "claude-code" || ws.Profile != "project-default" {
		t.Errorf("workspace target = %+v, want the claude-code project-default entry", ws)
	}

	mp := cfg.Targets[1]
	if mp.Kind != "marketplace" {
		t.Errorf("targets[1].kind = %q, want marketplace", mp.Kind)
	}
	if mp.Git.Remote != "git@github.com:acme/agent-marketplace.git" || mp.Git.Branch != "main" {
		t.Errorf("marketplace git = %+v, want the acme remote on main", mp.Git)
	}
	wantHarnesses := []string{"claude-code", "codex", "cursor"}
	if !reflect.DeepEqual(mp.Harnesses, wantHarnesses) {
		t.Errorf("marketplace harnesses = %v, want %v", mp.Harnesses, wantHarnesses)
	}
	if mp.Identity != "marketplace@acme.com" {
		t.Errorf("marketplace identity = %q, want marketplace@acme.com", mp.Identity)
	}
	if mp.CommitMessage != "Sync Podium catalog ({{.ChangedCount}}) {{.Timestamp}}" {
		t.Errorf("marketplace commit_message = %q", mp.CommitMessage)
	}
	if got := len(mp.Plugins); got != 1 {
		t.Fatalf("marketplace plugins len = %d, want 1", got)
	}
	finance := mp.Plugins[0]
	if finance.Name != "finance-pack" || finance.Description != "Accounts-payable skills and commands." {
		t.Errorf("plugin = %+v, want finance-pack with its description", finance)
	}
	if !reflect.DeepEqual(finance.Include, []string{"finance/**"}) {
		t.Errorf("plugin include = %v, want [finance/**]", finance.Include)
	}
	if !reflect.DeepEqual(finance.Exclude, []string{"finance/experimental/**"}) {
		t.Errorf("plugin exclude = %v, want [finance/experimental/**]", finance.Exclude)
	}
	if !reflect.DeepEqual(finance.Type, []string{"skill", "command"}) {
		t.Errorf("plugin type = %v, want [skill command]", finance.Type)
	}

	if mp.Workflow.IsZero() {
		t.Fatal("marketplace workflow is zero, want the prepare/publish phases")
	}
	if got := len(mp.Workflow.Prepare); got != 1 {
		t.Errorf("workflow prepare len = %d, want 1", got)
	}
	if got := len(mp.Workflow.Publish); got != 2 {
		t.Fatalf("workflow publish len = %d, want 2", got)
	}
	if !mp.Workflow.Publish[0].SkipIfNoChanges {
		t.Error("workflow publish[0] skip_if_no_changes = false, want true")
	}
}

// TestPluginFilter_ScopeFilterRoundTrips asserts PluginFilter.ScopeFilter()
// mirrors the plugin's include, exclude, and type into a same-package
// ScopeFilter, so plugin selection and sync selection share glob semantics.
func TestPluginFilter_ScopeFilterRoundTrips(t *testing.T) {
	p := PluginFilter{
		Name:    "finance-pack",
		Include: []string{"finance/**"},
		Exclude: []string{"finance/experimental/**"},
		Type:    []string{"skill", "command"},
	}
	sf := p.ScopeFilter()
	if !reflect.DeepEqual(sf.Include, p.Include) {
		t.Errorf("ScopeFilter().Include = %v, want %v", sf.Include, p.Include)
	}
	if !reflect.DeepEqual(sf.Exclude, p.Exclude) {
		t.Errorf("ScopeFilter().Exclude = %v, want %v", sf.Exclude, p.Exclude)
	}
	if !reflect.DeepEqual(sf.Types, p.Type) {
		t.Errorf("ScopeFilter().Types = %v, want %v", sf.Types, p.Type)
	}
}

// TestValidateOutput_RejectsNonPublishHarness asserts that a marketplace target
// whose harness set names a non-publish-target harness is rejected with
// config.invalid (§6.10), reusing the §7.8 publish-target selector.
func TestValidateOutput_RejectsNonPublishHarness(t *testing.T) {
	for _, h := range []string{"opencode", "none", "unknown-harness"} {
		t.Run(h, func(t *testing.T) {
			out := ResolvedOutput{ID: "bad", Harnesses: []string{"claude-code", h}}
			err := ValidateOutput(out)
			if err == nil {
				t.Fatalf("ValidateOutput with harness %q = nil error, want config.invalid", h)
			}
			if !errors.Is(err, ErrConfigInvalid) {
				t.Errorf("validateOutput error = %v, want errors.Is ErrConfigInvalid", err)
			}
		})
	}
}

// TestValidateOutput_AcceptsPublishHarnesses confirms the publish-target
// harnesses pass validation, so the rejection above is specific to the excluded
// harnesses.
func TestValidateOutput_AcceptsPublishHarnesses(t *testing.T) {
	out := ResolvedOutput{
		ID:        "ok",
		Harnesses: []string{"claude-code", "codex", "cursor", "gemini"},
	}
	if err := ValidateOutput(out); err != nil {
		t.Errorf("ValidateOutput with publish-target harnesses = %v, want nil", err)
	}
}

// TestValidateOutput_RejectsMalformedGlob asserts that a plugin with a malformed
// scope glob is rejected with config.invalid (§6.10), reusing the sync glob
// validator.
func TestValidateOutput_RejectsMalformedGlob(t *testing.T) {
	out := ResolvedOutput{
		ID:        "bad-glob",
		Harnesses: []string{"claude-code"},
		Plugins: []PluginFilter{
			{Name: "broken", Include: []string{"finance/{a,b"}},
		},
	}
	err := ValidateOutput(out)
	if err == nil {
		t.Fatal("ValidateOutput with malformed glob = nil error, want config.invalid")
	}
	if !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("validateOutput error = %v, want errors.Is ErrConfigInvalid", err)
	}
}

// TestValidateOutput_RejectsMalformedCommand asserts that a workflow command
// declaring neither run: nor sh:, or both, is rejected at config validation with
// config.invalid (§6.10), so a malformed command does not survive to a live run.
func TestValidateOutput_RejectsMalformedCommand(t *testing.T) {
	for name, wf := range map[string]Workflow{
		"prepare neither run nor sh": {Prepare: []Command{{}}},
		"publish both run and sh":    {Publish: []Command{{Run: []string{"true"}, Sh: "true"}}},
		"prepare_on_error empty":     {Publish: []Command{{Run: []string{"git", "push"}}}, PrepareOnError: []Command{{}}},
		"publish_on_error empty":     {Publish: []Command{{Run: []string{"git", "push"}}}, PublishOnError: []Command{{}}},
	} {
		t.Run(name, func(t *testing.T) {
			out := ResolvedOutput{ID: "bad-cmd", Harnesses: []string{"claude-code"}, Workflow: wf}
			err := ValidateOutput(out)
			if err == nil {
				t.Fatalf("ValidateOutput with %s = nil error, want config.invalid", name)
			}
			if !errors.Is(err, ErrConfigInvalid) {
				t.Errorf("validateOutput error = %v, want errors.Is ErrConfigInvalid", err)
			}
		})
	}
}

// TestValidateOutput_AcceptsWellFormed confirms a marketplace target with a
// valid harness set, well-formed globs, and well-formed commands passes
// validation, so the rejections above are specific to the malformed inputs.
func TestValidateOutput_AcceptsWellFormed(t *testing.T) {
	out := ResolvedOutput{
		ID:        "ok",
		Harnesses: []string{"claude-code"},
		Plugins:   []PluginFilter{{Name: "house", Include: []string{"rules/**"}}},
		Workflow: Workflow{
			Prepare: []Command{{Run: []string{"git", "clone", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"}}},
			Publish: []Command{{Sh: "git -C $PODIUM_WORKDIR push"}},
		},
	}
	if err := ValidateOutput(out); err != nil {
		t.Errorf("ValidateOutput with a well-formed target = %v, want nil", err)
	}
}

// TestDuration_ParsesAndRejectsBareInteger asserts the per-command Duration
// decodes a Go duration string and rejects a value without a unit, so an
// operator who writes "timeout: 30" gets a parse error rather than 30
// nanoseconds.
func TestDuration_ParsesAndRejectsBareInteger(t *testing.T) {
	var c Command
	if err := yaml.Unmarshal([]byte("run: [git, push]\ntimeout: 30s\n"), &c); err != nil {
		t.Fatalf("Unmarshal with 30s timeout: %v", err)
	}
	if got := c.Timeout.Duration(); got != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", got)
	}

	if err := yaml.Unmarshal([]byte("run: [git, push]\ntimeout: 30\n"), &c); err == nil {
		t.Error("Unmarshal with a bare integer timeout = nil error, want a parse failure")
	}
}

// TestCommand_Display asserts the human label distinguishes a run: argv list
// from an sh: string, the form error messages and skip logs use.
func TestCommand_Display(t *testing.T) {
	if got := (Command{Run: []string{"git", "push"}}).display(); got != "git push" {
		t.Errorf("run display = %q, want %q", got, "git push")
	}
	if got := (Command{Sh: "git push"}).display(); got != `sh -c "git push"` {
		t.Errorf("sh display = %q, want %q", got, `sh -c "git push"`)
	}
}
