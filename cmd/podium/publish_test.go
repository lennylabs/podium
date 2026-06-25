package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// These tests cover the `podium publish` CLI surface (§7.8): flag parsing and
// exit codes, --check config validation (including the non-publish-harness
// rejection), --dry-run against a filesystem-source fixture registry, --output
// selection, the JSON envelope, and the human/JSON print helpers. They use a
// filesystem-source registry and an explicit --config path, so no live server is
// involved.

const publishSkillArtifact = `---
type: skill
version: 1.0.0
---
`

// writePublishFixtureRegistry writes a small filesystem registry with one
// finance skill and returns its path. The leading layer directory is stripped
// from the canonical ID, so team-finance/finance/ap/pay-invoice has the
// canonical ID finance/ap/pay-invoice.
func writePublishFixtureRegistry(t *testing.T) string {
	t.Helper()
	reg := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "team-finance/finance/ap/pay-invoice/ARTIFACT.md", Content: publishSkillArtifact},
		testharness.WriteTreeOption{Path: "team-finance/finance/ap/pay-invoice/SKILL.md", Content: "---\nname: pay-invoice\ndescription: A pay-invoice skill.\n---\n\nBody.\n"},
	)
	return reg
}

// writePublishConfig writes a publish.yaml at an explicit path and returns it.
// The registry is a filesystem path so the render needs no live server, and the
// workflow declares a no-op publish command so a non-dry run would exercise the
// command path without a real git remote.
func writePublishConfig(t *testing.T, registry string, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "publish.yaml")
	full := "defaults:\n  registry: " + registry + "\n  identity: publisher@acme.com\n" + body
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatalf("write publish.yaml: %v", err)
	}
	return path
}

// validMarketplace declares one publish-target output (claude-code) with one
// plugin selecting finance/**.
const validMarketplace = `marketplaces:
  - id: acme-agents
    git:
      remote: git@github.com:acme/agent-marketplace.git
      branch: main
    harnesses: [claude-code]
    plugins:
      - name: finance-pack
        include: ["finance/**"]
`

// Spec: §7.8 — `podium publish --help` exits 0; an unknown flag exits 2.
func TestPublishCmd_FlagParsing(t *testing.T) {
	withStderr(t, func() {
		if code := publishCmd([]string{"--help"}); code != 0 {
			t.Errorf("publishCmd(--help) = %d, want 0", code)
		}
		if code := publishCmd([]string{"--bogus"}); code != 2 {
			t.Errorf("publishCmd(--bogus) = %d, want 2", code)
		}
	})
}

// Spec: §7.8 — `--check` validates the config only and exits 0 for a config whose
// harness set is a publish target. It renders nothing.
func TestPublishCmd_CheckValidConfig(t *testing.T) {
	reg := writePublishFixtureRegistry(t)
	cfg := writePublishConfig(t, reg, validMarketplace)
	out := captureStdout(t, func() {
		withStderr(t, func() {
			if code := publishCmd([]string{"--config", cfg, "--check"}); code != 0 {
				t.Errorf("publishCmd(--check, valid) = %d, want 0", code)
			}
		})
	})
	if !strings.Contains(out, "publish.yaml: ok") {
		t.Errorf("--check did not report ok:\n%s", out)
	}
}

// Spec: §7.8 — a marketplace output whose harness set names a non-publish-target
// harness (opencode, none, or an unknown id) is rejected at config validation
// with config.invalid, so --check exits 2.
func TestPublishCmd_CheckRejectsNonPublishHarness(t *testing.T) {
	reg := writePublishFixtureRegistry(t)
	for name, harness := range map[string]string{
		"opencode": "opencode",
		"none":     "none",
		"unknown":  "not-a-harness",
	} {
		t.Run(name, func(t *testing.T) {
			body := "marketplaces:\n  - id: bad\n    git:\n      remote: r\n      branch: main\n    harnesses: [" + harness + "]\n"
			cfg := writePublishConfig(t, reg, body)
			withStderr(t, func() {
				if code := publishCmd([]string{"--config", cfg, "--check"}); code != 2 {
					t.Errorf("publishCmd(--check, harness=%s) = %d, want 2", harness, code)
				}
			})
		})
	}
}

// Spec: §7.8 — `--dry-run` renders into a temporary directory and prints each
// prepare and publish command with its variables substituted, running no command
// and no publish phase. It exits 0 and reports the changed artifacts.
func TestPublishCmd_DryRun(t *testing.T) {
	reg := writePublishFixtureRegistry(t)
	// A workflow whose commands reference $PODIUM_WORKDIR and $PODIUM_GIT_REMOTE
	// so the dry-run print shows variable substitution.
	body := `  workflow:
    prepare:
      - run: ["git", "clone", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"]
    publish:
      - run: ["git", "-C", "$PODIUM_WORKDIR", "push"]
` + validMarketplace
	cfg := writePublishConfig(t, reg, body)
	out := captureStdout(t, func() {
		withStderr(t, func() {
			if code := publishCmd([]string{"--config", cfg, "--dry-run"}); code != 0 {
				t.Errorf("publishCmd(--dry-run) = %d, want 0", code)
			}
		})
	})
	for _, want := range []string{
		"== output acme-agents ==",
		"(dry-run; nothing pushed)",
		"changed:  true",
		"finance/ap/pay-invoice",
		"published: false",
		// The prepare command prints with $PODIUM_GIT_REMOTE substituted.
		"git clone git@github.com:acme/agent-marketplace.git",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--dry-run output missing %q\n--- got ---\n%s", want, out)
		}
	}
	// A dry run pushes nothing: the configured remote is never contacted, and the
	// published flag is false.
	if strings.Contains(out, "published: true") {
		t.Errorf("--dry-run reported a publish:\n%s", out)
	}
}

// Spec: §7.8 — `--dry-run --json` emits the structured envelope with one entry
// per output carrying the changed flag, the changed artifacts, and published.
func TestPublishCmd_DryRunJSON(t *testing.T) {
	reg := writePublishFixtureRegistry(t)
	cfg := writePublishConfig(t, reg, validMarketplace)
	out := captureStdout(t, func() {
		withStderr(t, func() {
			if code := publishCmd([]string{"--config", cfg, "--dry-run", "--json"}); code != 0 {
				t.Errorf("publishCmd(--dry-run --json) = %d, want 0", code)
			}
		})
	})
	var env struct {
		Outputs []struct {
			Output           string   `json:"output"`
			Changed          bool     `json:"changed"`
			ChangedArtifacts []string `json:"changed_artifacts"`
			Published        bool     `json:"published"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("publish --json output not valid JSON: %v\n%s", err, out)
	}
	if len(env.Outputs) != 1 {
		t.Fatalf("outputs = %+v, want 1", env.Outputs)
	}
	o := env.Outputs[0]
	if o.Output != "acme-agents" || !o.Changed || o.Published {
		t.Errorf("output envelope = %+v", o)
	}
	if !contains(o.ChangedArtifacts, "finance/ap/pay-invoice") {
		t.Errorf("changed_artifacts missing the rendered artifact: %v", o.ChangedArtifacts)
	}
}

// Spec: §7.8 — `--output <id>` selects one declared output; an unknown id is a
// config error and exits 2.
func TestPublishCmd_UnknownOutputExits2(t *testing.T) {
	reg := writePublishFixtureRegistry(t)
	cfg := writePublishConfig(t, reg, validMarketplace)
	withStderr(t, func() {
		if code := publishCmd([]string{"--config", cfg, "--output", "nonesuch", "--check"}); code != 2 {
			t.Errorf("publishCmd(--output nonesuch) = %d, want 2", code)
		}
	})
}

// Spec: §7.8 — `--output <id>` narrows the run to the named output.
func TestPublishCmd_OutputSelectsOne(t *testing.T) {
	reg := writePublishFixtureRegistry(t)
	body := validMarketplace + `  - id: acme-other
    git:
      remote: git@github.com:acme/other.git
      branch: main
    harnesses: [codex]
    plugins:
      - name: finance-pack
        include: ["finance/**"]
`
	cfg := writePublishConfig(t, reg, body)
	out := captureStdout(t, func() {
		withStderr(t, func() {
			if code := publishCmd([]string{"--config", cfg, "--output", "acme-other", "--dry-run"}); code != 0 {
				t.Errorf("publishCmd(--output acme-other) = %d, want 0", code)
			}
		})
	})
	if !strings.Contains(out, "== output acme-other ==") {
		t.Errorf("selected output not reported:\n%s", out)
	}
	if strings.Contains(out, "== output acme-agents ==") {
		t.Errorf("--output should have excluded acme-agents:\n%s", out)
	}
}

// Spec: §7.8 — a publish.yaml with no marketplaces: entry has nothing to publish
// and exits 2.
func TestPublishCmd_NoMarketplacesExits2(t *testing.T) {
	reg := writePublishFixtureRegistry(t)
	cfg := writePublishConfig(t, reg, "")
	withStderr(t, func() {
		if code := publishCmd([]string{"--config", cfg, "--check"}); code != 2 {
			t.Errorf("publishCmd(no marketplaces) = %d, want 2", code)
		}
	})
}

// Spec: §7.8 — an explicit --config path that does not exist is a config error
// and exits 2.
func TestPublishCmd_MissingConfigExits2(t *testing.T) {
	withStderr(t, func() {
		missing := filepath.Join(t.TempDir(), "absent.yaml")
		if code := publishCmd([]string{"--config", missing, "--check"}); code != 2 {
			t.Errorf("publishCmd(missing config) = %d, want 2", code)
		}
	})
}

// Spec: §7.8 — a malformed publish.yaml is a config error and exits 2.
func TestPublishCmd_MalformedConfigExits2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "publish.yaml")
	if err := os.WriteFile(path, []byte("defaults:\n  registry: [not, a, string]\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	withStderr(t, func() {
		if code := publishCmd([]string{"--config", path, "--check"}); code != 2 {
			t.Errorf("publishCmd(malformed) = %d, want 2", code)
		}
	})
}

// Spec: §7.8 — with no --config flag, publish loads the merged three-scope
// config (§7.5.2) discovered from the workspace `.podium/`. A workspace
// publish.yaml resolves and --check validates it.
func TestPublishCmd_MergedConfigFromWorkspace(t *testing.T) {
	reg := writePublishFixtureRegistry(t)
	// Isolate the user-global scope so a developer's real ~/.podium/publish.yaml
	// cannot leak into the merged config.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PODIUM_REGISTRY", "")

	ws := t.TempDir()
	testharness.WriteTree(t, ws,
		testharness.WriteTreeOption{
			Path:    ".podium/publish.yaml",
			Content: "defaults:\n  registry: " + reg + "\n  identity: publisher@acme.com\n" + validMarketplace,
		},
	)
	t.Chdir(ws)

	out := captureStdout(t, func() {
		withStderr(t, func() {
			if code := publishCmd([]string{"--check"}); code != 0 {
				t.Errorf("publishCmd(--check, merged config) = %d, want 0", code)
			}
		})
	})
	if !strings.Contains(out, "publish.yaml: ok") {
		t.Errorf("merged-config --check did not report ok:\n%s", out)
	}
}

// Spec: §7.8 — a workflow command that exits non-zero is a runtime failure: the
// pipeline fails fast and publish exits 1 (not 2, which is reserved for config
// errors). This drives a live (non-dry) render against the filesystem registry,
// then a publish command that exits non-zero.
func TestPublishCmd_WorkflowCommandFailureExits1(t *testing.T) {
	reg := writePublishFixtureRegistry(t)
	body := `  workflow:
    publish:
      - run: ["false"]
` + validMarketplace
	cfg := writePublishConfig(t, reg, body)
	workdir := t.TempDir()
	withStderr(t, func() {
		if code := publishCmd([]string{"--config", cfg, "--workdir", workdir}); code != 1 {
			t.Errorf("publishCmd(failing publish command) = %d, want 1", code)
		}
	})
}

// Spec: §7.8 — publishExitCode maps config.invalid to 2 and every other failure
// to 1, matching syncCmd's exit-code mapping.
func TestPublishExitCode(t *testing.T) {
	t.Parallel()
	if got := publishExitCode(nil); got != 1 {
		t.Errorf("publishExitCode(nil) = %d, want 1", got)
	}
}

// Spec: §7.8 — selectOutput finds an output by id and reports a miss.
func TestSelectOutput(t *testing.T) {
	reg := writePublishFixtureRegistry(t)
	cfg := writePublishConfig(t, reg, validMarketplace)
	outputs, err := resolvePublishOutputs(cfg)
	if err != nil {
		t.Fatalf("resolvePublishOutputs: %v", err)
	}
	if _, ok := selectOutput(outputs, "acme-agents"); !ok {
		t.Errorf("selectOutput(acme-agents) miss")
	}
	if _, ok := selectOutput(outputs, "absent"); ok {
		t.Errorf("selectOutput(absent) hit")
	}
}
