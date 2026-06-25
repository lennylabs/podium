package publish

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// specExampleYAML is the §7.8 publish.yaml example verbatim. Parsing it exercises
// the defaults block, the workflow command forms (run argv lists with
// skip_if_no_changes and continue_on_error), and the three marketplace outputs,
// including the per-output workflow override.
const specExampleYAML = `defaults:
  registry: https://podium.acme.com
  identity: publisher@acme.com
  workflow:
    prepare:
      - run: ["git", "clone", "--branch", "$PODIUM_GIT_BRANCH", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"]
    publish:
      - run: ["git", "-C", "$PODIUM_WORKDIR", "add", "-A"]
      - run: ["git", "-C", "$PODIUM_WORKDIR", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"]
        skip_if_no_changes: true
      - run: ["git", "-C", "$PODIUM_WORKDIR", "push", "origin", "$PODIUM_GIT_BRANCH"]

marketplaces:
  - id: acme-agents
    git:
      remote: git@github.com:acme/agent-marketplace.git
      branch: main
    harnesses: [claude-code, codex, cursor]
    commit_message: "Sync Podium catalog ({{.ChangedCount}} changes) {{.Timestamp}}"
    plugins:
      - name: finance-pack
        include: ["finance/**"]
        exclude: ["finance/experimental/**"]
        type: [skill, command, rule]
      - name: security-baseline
        include: ["security/baseline/**"]

  - id: acme-gemini
    git:
      remote: git@github.com:acme/gemini-extension.git
      branch: main
    harnesses: [gemini]
    plugins:
      - name: house-rules
        include: ["rules/**"]

  - id: acme-editors-pr
    git:
      remote: git@github.com:acme/editor-config.git
      branch: podium-sync
    harnesses: [cursor]
    plugins:
      - name: house-rules
        include: ["rules/**"]
    workflow:
      prepare:
        - run: ["git", "clone", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"]
        - run: ["git", "-C", "$PODIUM_WORKDIR", "checkout", "-B", "$PODIUM_GIT_BRANCH"]
      publish:
        - run: ["git", "-C", "$PODIUM_WORKDIR", "add", "-A"]
        - run: ["git", "-C", "$PODIUM_WORKDIR", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"]
          skip_if_no_changes: true
        - run: ["git", "-C", "$PODIUM_WORKDIR", "push", "origin", "$PODIUM_GIT_BRANCH"]
        - run: ["gh", "pr", "create", "--fill", "--base", "main", "--head", "$PODIUM_GIT_BRANCH"]
          continue_on_error: true
`

// writeScope writes a publish config file under dir/.podium/<name>.
func writeScope(t *testing.T, dir, name, content string) {
	t.Helper()
	podiumDir := filepath.Join(dir, ".podium")
	if err := os.MkdirAll(podiumDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", podiumDir, err)
	}
	if err := os.WriteFile(filepath.Join(podiumDir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestLoadMergedConfig_ParsesSpecExample parses the §7.8 example and asserts the
// decoded structure: the defaults block, the three outputs, the git blocks, the
// harness sets, the plugin scope filters, and the per-command workflow flags.
func TestLoadMergedConfig_ParsesSpecExample(t *testing.T) {
	ws := t.TempDir()
	writeScope(t, ws, configFileName, specExampleYAML)

	cfg, workspace, err := LoadMergedConfig(ws, "")
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}
	if workspace != ws {
		t.Fatalf("workspace = %q, want %q", workspace, ws)
	}

	if got := cfg.Defaults.Registry; got != "https://podium.acme.com" {
		t.Errorf("defaults.registry = %q", got)
	}
	if got := cfg.Defaults.Identity; got != "publisher@acme.com" {
		t.Errorf("defaults.identity = %q", got)
	}
	if got := len(cfg.Defaults.Workflow.Prepare); got != 1 {
		t.Fatalf("defaults.workflow.prepare len = %d, want 1", got)
	}
	if got := len(cfg.Defaults.Workflow.Publish); got != 3 {
		t.Fatalf("defaults.workflow.publish len = %d, want 3", got)
	}
	// The commit command in the default publish workflow carries skip_if_no_changes.
	if commit := cfg.Defaults.Workflow.Publish[1]; !commit.SkipIfNoChanges {
		t.Errorf("default publish commit skip_if_no_changes = false, want true")
	}

	if got := len(cfg.Marketplaces); got != 3 {
		t.Fatalf("marketplaces len = %d, want 3", got)
	}

	agents := cfg.Marketplaces[0]
	if agents.ID != "acme-agents" {
		t.Errorf("marketplaces[0].id = %q", agents.ID)
	}
	if agents.Git.Remote != "git@github.com:acme/agent-marketplace.git" || agents.Git.Branch != "main" {
		t.Errorf("acme-agents git = %+v", agents.Git)
	}
	wantHarnesses := []string{"claude-code", "codex", "cursor"}
	if !equalStrings(agents.Harnesses, wantHarnesses) {
		t.Errorf("acme-agents harnesses = %v, want %v", agents.Harnesses, wantHarnesses)
	}
	if agents.CommitMessage != "Sync Podium catalog ({{.ChangedCount}} changes) {{.Timestamp}}" {
		t.Errorf("acme-agents commit_message = %q", agents.CommitMessage)
	}
	if got := len(agents.Plugins); got != 2 {
		t.Fatalf("acme-agents plugins len = %d, want 2", got)
	}
	finance := agents.Plugins[0]
	if finance.Name != "finance-pack" {
		t.Errorf("plugin[0].name = %q", finance.Name)
	}
	if !equalStrings(finance.Include, []string{"finance/**"}) {
		t.Errorf("finance include = %v", finance.Include)
	}
	if !equalStrings(finance.Exclude, []string{"finance/experimental/**"}) {
		t.Errorf("finance exclude = %v", finance.Exclude)
	}
	if !equalStrings(finance.Type, []string{"skill", "command", "rule"}) {
		t.Errorf("finance type = %v", finance.Type)
	}
	// ScopeFilter mirrors the plugin's selection into the sync machinery.
	sf := finance.ScopeFilter()
	if !equalStrings(sf.Include, finance.Include) || !equalStrings(sf.Exclude, finance.Exclude) || !equalStrings(sf.Types, finance.Type) {
		t.Errorf("ScopeFilter() = %+v, does not mirror plugin filter", sf)
	}

	// The third output overrides the default workflow with a four-command
	// publish phase whose last command carries continue_on_error.
	pr := cfg.Marketplaces[2]
	if pr.ID != "acme-editors-pr" {
		t.Errorf("marketplaces[2].id = %q", pr.ID)
	}
	if pr.Workflow.IsZero() {
		t.Fatal("acme-editors-pr workflow is zero, want the override")
	}
	if got := len(pr.Workflow.Publish); got != 4 {
		t.Fatalf("acme-editors-pr publish len = %d, want 4", got)
	}
	if last := pr.Workflow.Publish[3]; !last.ContinueOnError {
		t.Errorf("acme-editors-pr last publish continue_on_error = false, want true")
	}
}

// TestLoadMergedConfig_ScopePrecedence asserts the §7.5.2 precedence the publish
// loader mirrors. Defaults resolve per key: a higher-precedence non-empty value
// wins, and a key absent from the higher scopes keeps the lower-scope value. The
// marketplaces list is the structural analog of the sync.yaml `targets:` list,
// which §7.5.2 resolves by whole-list replacement, so the highest-precedence
// scope that declares a non-empty `marketplaces:` replaces the entire list and a
// lower-scope output does not survive alongside it.
func TestLoadMergedConfig_ScopePrecedence(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()

	writeScope(t, home, configFileName, `defaults:
  registry: https://user-global.example.com
  identity: user@example.com
marketplaces:
  - id: from-user-global
    harnesses: [claude-code]
`)
	writeScope(t, ws, configFileName, `defaults:
  registry: https://project-shared.example.com
marketplaces:
  - id: shared-output
    harnesses: [codex]
    git:
      remote: shared-remote
`)
	writeScope(t, ws, localConfigFileName, `defaults:
  registry: https://project-local.example.com
marketplaces:
  - id: local-output
    harnesses: [cursor]
    git:
      remote: local-remote
`)

	cfg, _, err := LoadMergedConfig(ws, home)
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}

	// Project-local registry wins; identity is unset in the higher scopes so the
	// user-global value survives the per-key merge.
	if got := cfg.Defaults.Registry; got != "https://project-local.example.com" {
		t.Errorf("registry = %q, want project-local value", got)
	}
	if got := cfg.Defaults.Identity; got != "user@example.com" {
		t.Errorf("identity = %q, want user-global value to survive", got)
	}

	// Whole-list replacement: the project-local scope declares a non-empty
	// marketplaces list, so it replaces the lists from the lower scopes entirely.
	// Neither the user-global output nor the project-shared output survives.
	if got := len(cfg.Marketplaces); got != 1 {
		t.Fatalf("marketplaces len = %d, want 1 (project-local list replaces the lower scopes)", got)
	}
	only := cfg.Marketplaces[0]
	if only.ID != "local-output" {
		t.Errorf("marketplaces[0].id = %q, want local-output", only.ID)
	}
	if !equalStrings(only.Harnesses, []string{"cursor"}) {
		t.Errorf("local-output harnesses = %v, want [cursor]", only.Harnesses)
	}
	if only.Git.Remote != "local-remote" {
		t.Errorf("local-output git.remote = %q, want local-remote", only.Git.Remote)
	}
}

// TestLoadMergedConfig_MarketplacesWholeListReplace asserts that a
// higher-precedence scope's marketplaces list replaces a lower scope's list as a
// unit even when the higher scope omits an id the lower scope declared. This is
// the §7.5.2 `targets:` whole-list-replace rule: the project-shared list wins
// over the user-global list when no project-local list is present, and the
// user-global-only output does not survive.
func TestLoadMergedConfig_MarketplacesWholeListReplace(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()

	writeScope(t, home, configFileName, `marketplaces:
  - id: user-only
    harnesses: [claude-code]
  - id: shared-id
    harnesses: [claude-code]
`)
	writeScope(t, ws, configFileName, `marketplaces:
  - id: shared-id
    harnesses: [cursor]
`)

	cfg, _, err := LoadMergedConfig(ws, home)
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}

	if got := len(cfg.Marketplaces); got != 1 {
		t.Fatalf("marketplaces len = %d, want 1 (project-shared list replaces user-global)", got)
	}
	if got := cfg.Marketplaces[0].ID; got != "shared-id" {
		t.Errorf("marketplaces[0].id = %q, want shared-id", got)
	}
	if !equalStrings(cfg.Marketplaces[0].Harnesses, []string{"cursor"}) {
		t.Errorf("shared-id harnesses = %v, want [cursor] (project-shared definition)", cfg.Marketplaces[0].Harnesses)
	}
}

// TestLoadMergedConfig_LowerScopeListSurvivesWhenHigherOmits asserts that a
// lower-precedence marketplaces list survives when no higher scope declares one,
// because whole-list replacement only fires for a non-empty higher-scope list.
func TestLoadMergedConfig_LowerScopeListSurvivesWhenHigherOmits(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()

	writeScope(t, home, configFileName, `marketplaces:
  - id: user-output
    harnesses: [claude-code]
`)
	// The project-shared scope sets only a default; it declares no marketplaces.
	writeScope(t, ws, configFileName, `defaults:
  registry: https://project-shared.example.com
`)

	cfg, _, err := LoadMergedConfig(ws, home)
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}

	if got := cfg.Defaults.Registry; got != "https://project-shared.example.com" {
		t.Errorf("registry = %q, want project-shared value", got)
	}
	if got := len(cfg.Marketplaces); got != 1 {
		t.Fatalf("marketplaces len = %d, want 1 (user-global list survives)", got)
	}
	if got := cfg.Marketplaces[0].ID; got != "user-output" {
		t.Errorf("marketplaces[0].id = %q, want user-output", got)
	}
}

// TestResolve_WorkflowOverride asserts that a marketplace inherits the default
// workflow when it declares none, and that a marketplace declaring a workflow
// replaces the default workflow in full rather than merging command-by-command.
func TestResolve_WorkflowOverride(t *testing.T) {
	cfg := &PublishConfig{
		Defaults: Defaults{
			Registry: "https://podium.acme.com",
			Identity: "publisher@acme.com",
			Workflow: Workflow{
				Prepare: []Command{{Run: []string{"git", "clone", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"}}},
				Publish: []Command{{Run: []string{"git", "push"}}},
			},
		},
		Marketplaces: []MarketplaceOutput{
			{ID: "inherits", Harnesses: []string{"claude-code"}},
			{
				ID:        "overrides",
				Harnesses: []string{"cursor"},
				Workflow: Workflow{
					Publish: []Command{{Run: []string{"gh", "pr", "create"}}},
				},
			},
		},
	}

	outputs, err := cfg.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(outputs) != 2 {
		t.Fatalf("outputs len = %d, want 2", len(outputs))
	}

	inherits := outputs[0]
	if inherits.Registry != "https://podium.acme.com" || inherits.Identity != "publisher@acme.com" {
		t.Errorf("inherits did not inherit defaults: registry=%q identity=%q", inherits.Registry, inherits.Identity)
	}
	if len(inherits.Workflow.Prepare) != 1 || len(inherits.Workflow.Publish) != 1 {
		t.Errorf("inherits workflow = %+v, want the default workflow", inherits.Workflow)
	}
	if inherits.Workflow.Publish[0].Run[1] != "push" {
		t.Errorf("inherits publish[0] = %v, want the default git push", inherits.Workflow.Publish[0].Run)
	}

	overrides := outputs[1]
	// The override replaces the default workflow in full: the default prepare
	// clone does not leak into the override, which declares only a publish phase.
	if len(overrides.Workflow.Prepare) != 0 {
		t.Errorf("overrides prepare = %+v, want empty (full replacement)", overrides.Workflow.Prepare)
	}
	if len(overrides.Workflow.Publish) != 1 || overrides.Workflow.Publish[0].Run[0] != "gh" {
		t.Errorf("overrides publish = %+v, want the gh pr create override", overrides.Workflow.Publish)
	}
}

// TestResolve_RegistryEnvPrecedence asserts the §7.5.2 precedence level the
// publish loader had been missing: PODIUM_REGISTRY (level 2) wins over the
// merged defaults.registry (a lower file scope), and applies even when no
// defaults.registry is set. The §7.8 Pattern A CI run depends on this, because
// it sets PODIUM_REGISTRY as an env var with no defaults.registry in
// publish.yaml.
func TestResolve_RegistryEnvPrecedence(t *testing.T) {
	t.Run("env overrides defaults.registry", func(t *testing.T) {
		cfg := &PublishConfig{
			Defaults:     Defaults{Registry: "https://from-defaults.example.com"},
			Marketplaces: []MarketplaceOutput{{ID: "out", Harnesses: []string{"claude-code"}}},
		}
		env := func(k string) string {
			if k == "PODIUM_REGISTRY" {
				return "https://from-env.example.com"
			}
			return ""
		}
		outputs, err := cfg.Resolve(env)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got := outputs[0].Registry; got != "https://from-env.example.com" {
			t.Errorf("registry = %q, want the PODIUM_REGISTRY value", got)
		}
	})

	t.Run("env applies with no defaults.registry", func(t *testing.T) {
		cfg := &PublishConfig{
			Marketplaces: []MarketplaceOutput{{ID: "out", Harnesses: []string{"claude-code"}}},
		}
		env := func(k string) string {
			if k == "PODIUM_REGISTRY" {
				return "https://from-env.example.com"
			}
			return ""
		}
		outputs, err := cfg.Resolve(env)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got := outputs[0].Registry; got != "https://from-env.example.com" {
			t.Errorf("registry = %q, want the PODIUM_REGISTRY value", got)
		}
	})

	t.Run("defaults.registry survives an unset env var", func(t *testing.T) {
		cfg := &PublishConfig{
			Defaults:     Defaults{Registry: "https://from-defaults.example.com"},
			Marketplaces: []MarketplaceOutput{{ID: "out", Harnesses: []string{"claude-code"}}},
		}
		outputs, err := cfg.Resolve(func(string) string { return "" })
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got := outputs[0].Registry; got != "https://from-defaults.example.com" {
			t.Errorf("registry = %q, want the defaults value when the env var is unset", got)
		}
	})
}

// TestResolve_NilConfig confirms Resolve on a nil config returns no outputs and
// no error, so a caller that loaded an absent config does not have to nil-check
// before resolving.
func TestResolve_NilConfig(t *testing.T) {
	var cfg *PublishConfig
	outputs, err := cfg.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve on nil config: %v", err)
	}
	if outputs != nil {
		t.Errorf("outputs = %v, want nil", outputs)
	}
}

// TestResolve_RejectsNonPublishHarness asserts that an output whose harness set
// names a non-publish-target harness (opencode or none) is rejected with
// config.invalid (§6.10), reusing the §7.8 publish-target selector.
func TestResolve_RejectsNonPublishHarness(t *testing.T) {
	for _, h := range []string{"opencode", "none", "unknown-harness"} {
		t.Run(h, func(t *testing.T) {
			cfg := &PublishConfig{
				Marketplaces: []MarketplaceOutput{
					{ID: "bad", Harnesses: []string{"claude-code", h}},
				},
			}
			_, err := cfg.Resolve(nil)
			if err == nil {
				t.Fatalf("Resolve with harness %q = nil error, want config.invalid", h)
			}
			if !errors.Is(err, ErrConfigInvalid) {
				t.Errorf("Resolve error = %v, want errors.Is ErrConfigInvalid", err)
			}
		})
	}
}

// TestResolve_AcceptsPublishHarnesses confirms the publish-target harnesses pass
// validation, so the rejection above is specific to the excluded harnesses.
func TestResolve_AcceptsPublishHarnesses(t *testing.T) {
	cfg := &PublishConfig{
		Marketplaces: []MarketplaceOutput{
			{ID: "ok", Harnesses: []string{"claude-code", "claude-desktop", "claude-cowork", "codex", "cursor", "gemini", "pi", "hermes"}},
		},
	}
	if _, err := cfg.Resolve(nil); err != nil {
		t.Errorf("Resolve with publish-target harnesses = %v, want nil", err)
	}
}

// TestResolve_RejectsMalformedGlob asserts that a plugin with a malformed scope
// glob is rejected with config.invalid (§6.10), reusing the sync glob validator.
func TestResolve_RejectsMalformedGlob(t *testing.T) {
	cfg := &PublishConfig{
		Marketplaces: []MarketplaceOutput{
			{
				ID:        "bad-glob",
				Harnesses: []string{"claude-code"},
				Plugins: []PluginFilter{
					{Name: "broken", Include: []string{"finance/{a,b"}},
				},
			},
		},
	}
	_, err := cfg.Resolve(nil)
	if err == nil {
		t.Fatal("Resolve with malformed glob = nil error, want config.invalid")
	}
	if !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Resolve error = %v, want errors.Is ErrConfigInvalid", err)
	}
}

// TestResolve_RejectsMalformedCommand asserts that a workflow command declaring
// neither run: nor sh:, or both, is rejected at config resolution with
// config.invalid (§6.10), so a malformed command does not survive to a live run.
// The per-phase cleanup lists are validated too, not only prepare and publish.
func TestResolve_RejectsMalformedCommand(t *testing.T) {
	for name, wf := range map[string]Workflow{
		"prepare neither run nor sh": {Prepare: []Command{{}}},
		"publish both run and sh":    {Publish: []Command{{Run: []string{"true"}, Sh: "true"}}},
		"prepare_on_error empty":     {Publish: []Command{{Run: []string{"git", "push"}}}, PrepareOnError: []Command{{}}},
		"publish_on_error empty":     {Publish: []Command{{Run: []string{"git", "push"}}}, PublishOnError: []Command{{}}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &PublishConfig{
				Marketplaces: []MarketplaceOutput{
					{ID: "bad-cmd", Harnesses: []string{"claude-code"}, Workflow: wf},
				},
			}
			_, err := cfg.Resolve(nil)
			if err == nil {
				t.Fatalf("Resolve with %s = nil error, want config.invalid", name)
			}
			if !errors.Is(err, ErrConfigInvalid) {
				t.Errorf("Resolve error = %v, want errors.Is ErrConfigInvalid", err)
			}
		})
	}
}

// TestLoadMergedConfig_PerPhaseOnError asserts the per-phase cleanup keys
// (prepare_on_error, publish_on_error) parse into the workflow's separate
// cleanup lists, so an operator can scope cleanup to the phase that failed.
func TestLoadMergedConfig_PerPhaseOnError(t *testing.T) {
	ws := t.TempDir()
	writeScope(t, ws, configFileName, `marketplaces:
  - id: with-cleanup
    harnesses: [claude-code]
    workflow:
      prepare:
        - run: ["git", "clone", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"]
      prepare_on_error:
        - run: ["rm", "-rf", "$PODIUM_WORKDIR"]
      publish:
        - run: ["git", "push"]
      publish_on_error:
        - run: ["git", "-C", "$PODIUM_WORKDIR", "reset", "--hard"]
`)
	cfg, _, err := LoadMergedConfig(ws, "")
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}
	wf := cfg.Marketplaces[0].Workflow
	if len(wf.PrepareOnError) != 1 || wf.PrepareOnError[0].Run[0] != "rm" {
		t.Errorf("prepare_on_error = %+v, want the rm cleanup", wf.PrepareOnError)
	}
	if len(wf.PublishOnError) != 1 || wf.PublishOnError[0].Run[3] != "reset" {
		t.Errorf("publish_on_error = %+v, want the git reset cleanup", wf.PublishOnError)
	}
	// A workflow that declares only a cleanup list is non-zero, so a marketplace
	// declaring its own cleanup does not silently inherit the default workflow.
	if (Workflow{PublishOnError: []Command{{Sh: "true"}}}).IsZero() {
		t.Error("a workflow with only a publish_on_error list must not be zero")
	}
}

// TestCommand_TimeoutParsing asserts the per-command timeout decodes a Go
// duration string and rejects a value without a unit.
func TestCommand_TimeoutParsing(t *testing.T) {
	ws := t.TempDir()
	writeScope(t, ws, configFileName, `marketplaces:
  - id: with-timeout
    harnesses: [claude-code]
    workflow:
      publish:
        - run: ["git", "push"]
          timeout: 45s
`)
	cfg, _, err := LoadMergedConfig(ws, "")
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}
	cmd := cfg.Marketplaces[0].Workflow.Publish[0]
	if got := cmd.Timeout.Duration(); got != 45*time.Second {
		t.Errorf("timeout = %v, want 45s", got)
	}

	writeScope(t, ws, configFileName, `marketplaces:
  - id: bad-timeout
    harnesses: [claude-code]
    workflow:
      publish:
        - run: ["git", "push"]
          timeout: 45
`)
	if _, _, err := LoadMergedConfig(ws, ""); err == nil {
		t.Error("LoadMergedConfig with unitless timeout = nil error, want a parse failure")
	}
}

// TestLoadMergedConfig_Absent confirms a workspace without publish.yaml loads to
// an empty config rather than an error, so callers distinguish "no config" from
// "invalid config".
func TestLoadMergedConfig_Absent(t *testing.T) {
	ws := t.TempDir()
	// A .podium/ directory exists (so the workspace is discovered) but holds no
	// publish.yaml.
	if err := os.MkdirAll(filepath.Join(ws, ".podium"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg, workspace, err := LoadMergedConfig(ws, "")
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}
	if workspace != ws {
		t.Errorf("workspace = %q, want %q", workspace, ws)
	}
	if len(cfg.Marketplaces) != 0 {
		t.Errorf("marketplaces = %v, want empty", cfg.Marketplaces)
	}
}

// TestReadConfigFile_Malformed confirms a syntactically invalid publish.yaml
// surfaces an error rather than a silent empty config.
func TestReadConfigFile_Malformed(t *testing.T) {
	ws := t.TempDir()
	writeScope(t, ws, configFileName, "defaults:\n  registry: [unterminated\n")
	if _, _, err := LoadMergedConfig(ws, ""); err == nil {
		t.Error("LoadMergedConfig with malformed YAML = nil error, want a parse failure")
	}
}

// TestConfigPath asserts the canonical publish.yaml path under a workspace.
func TestConfigPath(t *testing.T) {
	got := ConfigPath("/home/alice/project")
	want := filepath.Join("/home/alice/project", ".podium", "publish.yaml")
	if got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
