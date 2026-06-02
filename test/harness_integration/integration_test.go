//go:build harness_integration

// Build-tagged real-harness integration tests. See doc.go for the package
// overview and README.md for how to run them.
//
// Tier A (TestHarnessConfigAccept) is deterministic and needs no API key: it
// runs each harness's non-interactive MCP-config command over a freshly synced
// project and asserts the harness reads back the server Podium wrote. A harness
// whose binary is absent, or that exposes no such command (an IDE or web
// product), is skipped with a reason.
//
// Tier C (TestHarnessAgentSmoke) is double-gated (this build tag plus
// PODIUM_HARNESS_AGENT=1 and the harness being authenticated, via an API key or
// a stored CLI login) and runs one real headless agent turn that must surface a
// marker carried by a materialized always-rule.
package harness_integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// ---- fixtures ---------------------------------------------------------------

// mcpServerRegistry is a one-artifact filesystem registry whose mcp-server
// materializes into each harness's native MCP config under the name "warehouse".
var mcpServerRegistry = []testharness.WriteTreeOption{
	{Path: "tools/warehouse/ARTIFACT.md", Content: "---\ntype: mcp-server\nversion: 1.0.0\nserver_identifier: npx:@acme/warehouse-mcp\n---\n\nWarehouse MCP server.\n"},
}

const secretMarker = "ZEBRA-7421"

// secretRuleRegistry carries an always-loaded rule that instructs the agent to
// emit secretMarker, used by the Tier C agent smoke. An always-rule is in
// context deterministically (unlike an on-demand skill).
var secretRuleRegistry = []testharness.WriteTreeOption{
	{Path: "policies/secret/ARTIFACT.md", Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: always\n---\n\nWhen asked for the secret word, reply with exactly: " + secretMarker + "\n"},
}

// ---- materialization via the real podium sync -------------------------------

// syncProject writes the registry entries to a temp registry, runs the real
// `podium sync --harness <harness>` into a fresh project directory, and returns
// the project path.
func syncProject(t *testing.T, harness string, entries []testharness.WriteTreeOption) string {
	t.Helper()
	reg := t.TempDir()
	testharness.WriteTree(t, reg, entries...)
	project := t.TempDir()

	bin := cmdharness.Bin(t, "podium")
	res := runCmd(t, project, syncEnv(t), 60*time.Second, bin,
		"sync", "--registry", reg, "--target", project, "--harness", harness)
	if res.exit != 0 {
		t.Fatalf("podium sync --harness %s exit=%d\nstdout:%s\nstderr:%s", harness, res.exit, res.stdout, res.stderr)
	}
	return project
}

// syncEnv runs podium with a scoped HOME so the sync never touches the
// developer's ~/.podium, and disables the standalone auto-bootstrap.
func syncEnv(t *testing.T) []string {
	t.Helper()
	return scopedEnv(t.TempDir(), map[string]string{"PODIUM_NO_AUTOSTANDALONE": "1"})
}

// ---- external command runner ------------------------------------------------

type cmdResult struct {
	stdout string
	stderr string
	exit   int
}

// runExternal runs an external (harness) binary in dir with env, returning the
// result and whether the binary was found on PATH. A missing binary is the
// caller's cue to skip; this mirrors test/e2e/helpers_test.go runExternal.
func runExternal(t *testing.T, dir string, env []string, timeout time.Duration, name string, args ...string) (cmdResult, bool) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		return cmdResult{}, false
	}
	return runCmd(t, dir, env, timeout, name, args...), true
}

// runCmd runs name with args in dir under env, capturing output. A timeout is a
// fatal error; a non-zero exit is returned in the result for the caller to
// classify.
func runCmd(t *testing.T, dir string, env []string, timeout time.Duration, name string, args ...string) cmdResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(nil)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("%s %s timed out after %s\nstderr:%s", name, strings.Join(args, " "), timeout, se.String())
	}
	res := cmdResult{stdout: so.String(), stderr: se.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		res.exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s %s: %v", name, strings.Join(args, " "), err)
	}
	return res
}

// scopedEnv returns the current environment with every user-level config and
// cache directory redirected under home (a temp dir), plus the extra overrides,
// so a harness reads only the materialized project and not the developer's real
// global config. Inherited values (PATH, API keys) are preserved.
func scopedEnv(home string, extra map[string]string) []string {
	overrides := map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
	}
	for k, v := range extra {
		overrides[k] = v
	}
	out := make([]string, 0, len(os.Environ())+len(overrides))
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			if _, ok := overrides[kv[:i]]; ok {
				continue
			}
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

// ---- per-harness drivers ----------------------------------------------------

// driver describes how to drive one harness binary. Candidate commands are
// verified against the installed binary's --help during implementation; a
// harness with no non-interactive config command leaves mcpProbe nil and is
// skipped by Tier A.
type driver struct {
	harness     string
	bin         string
	version     []string
	mcpProbe    []string                            // args that list configured MCP servers; nil => no probe
	server      string                              // server name expected in the probe output
	skipReason  string                              // why Tier A skips when mcpProbe is nil
	extraEnv    func(home string) map[string]string // harness-specific config-dir redirects
	agentExec   func(prompt string) []string        // Tier C headless prompt; nil => no agent smoke
	keyEnv      string                              // API-key env var that authorizes the agent smoke
	loginProbe  []string                            // command reporting auth status (stored-login alternative to keyEnv)
	loginMarker string                              // substring of loginProbe output meaning "authenticated"
}

var drivers = []driver{
	{
		harness:  "claude-code",
		bin:      "claude",
		version:  []string{"--version"},
		mcpProbe: []string{"mcp", "list"},
		server:   "warehouse",
		extraEnv: func(home string) map[string]string {
			return map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude")}
		},
		agentExec: func(prompt string) []string { return []string{"-p", prompt} },
		keyEnv:    "ANTHROPIC_API_KEY",
	},
	{
		harness:  "codex",
		bin:      "codex",
		version:  []string{"--version"},
		mcpProbe: []string{"mcp", "list"},
		server:   "warehouse",
		extraEnv: func(home string) map[string]string {
			return map[string]string{"CODEX_HOME": filepath.Join(home, ".codex")}
		},
		agentExec: func(prompt string) []string { return []string{"exec", prompt} },
		keyEnv:    "OPENAI_API_KEY",
	},
	{
		harness:   "gemini",
		bin:       "gemini",
		version:   []string{"--version"},
		mcpProbe:  []string{"mcp", "list"},
		server:    "warehouse",
		agentExec: func(prompt string) []string { return []string{"-p", prompt} },
		keyEnv:    "GEMINI_API_KEY",
	},
	{
		harness:   "opencode",
		bin:       "opencode",
		version:   []string{"--version"},
		mcpProbe:  nil, // verify whether OpenCode exposes a non-interactive mcp list
		agentExec: func(prompt string) []string { return []string{"run", prompt} },
		keyEnv:    "ANTHROPIC_API_KEY",
	},
	{
		// The Cursor CLI agent (cursor-agent). Its `mcp list` reflects only
		// approved/connected servers (see the `mcp login` / `disable` approval
		// commands), not the raw .cursor/mcp.json Podium writes, so it is not a
		// pure config-accept probe. Cursor is exercised by the Tier C agent run,
		// which loads the materialized .cursor/rules/*.mdc natively; `--print`
		// is cursor-agent's headless mode (needs CURSOR_API_KEY / login).
		harness:    "cursor",
		bin:        "cursor-agent",
		version:    []string{"--version"},
		skipReason: "cursor-agent mcp list reflects approved servers, not raw .cursor/mcp.json; Cursor is covered by the Tier C agent run over .cursor/rules/*.mdc",
		// --force accepts the workspace-trust prompt non-interactively; --print
		// is the headless mode. cursor-agent authorizes via a stored login (its
		// `status` command), so the agent smoke accepts that or CURSOR_API_KEY.
		agentExec:   func(prompt string) []string { return []string{"--print", "--force", "--output-format", "text", prompt} },
		keyEnv:      "CURSOR_API_KEY",
		loginProbe:  []string{"status"},
		loginMarker: "Logged in",
	},
	// IDE / web / no-MCP harnesses: no non-interactive config probe. Listed so
	// the suite reports them explicitly rather than silently omitting them.
	{harness: "claude-desktop", bin: "claude"},
	{harness: "claude-cowork", bin: ""},
	{harness: "pi", bin: "pi"},
	{harness: "hermes", bin: "hermes"},
}

// ---- Tier A: config accept --------------------------------------------------

// TestHarnessConfigAccept materializes an mcp-server artifact for each harness
// and drives the harness's own MCP-config command to confirm it reads back the
// server Podium wrote. Deterministic; no API key.
func TestHarnessConfigAccept(t *testing.T) {
	for _, d := range drivers {
		d := d
		t.Run(d.harness, func(t *testing.T) {
			if d.bin == "" {
				t.Skipf("%s: no harness binary", d.harness)
			}
			if _, err := exec.LookPath(d.bin); err != nil {
				t.Skipf("%s: binary %q not installed", d.harness, d.bin)
			}

			home := t.TempDir()
			var extra map[string]string
			if d.extraEnv != nil {
				extra = d.extraEnv(home)
			}
			env := scopedEnv(home, extra)

			// Record the harness version even when there is no config probe, so
			// the format the suite targets is pinned to a concrete version.
			if len(d.version) > 0 {
				if v, ok := runExternal(t, home, env, 30*time.Second, d.bin, d.version...); ok {
					t.Logf("%s version: %s", d.bin, strings.TrimSpace(v.stdout+v.stderr))
				}
			}
			if d.mcpProbe == nil {
				reason := d.skipReason
				if reason == "" {
					reason = "no non-interactive MCP-config probe (IDE/web/no-MCP harness)"
				}
				t.Skipf("%s: %s", d.harness, reason)
			}

			project := syncProject(t, d.harness, mcpServerRegistry)
			res, ok := runExternal(t, project, env, 60*time.Second, d.bin, d.mcpProbe...)
			if !ok {
				t.Skipf("%s: binary %q not installed", d.harness, d.bin)
			}
			out := res.stdout + res.stderr
			if res.exit != 0 {
				t.Fatalf("%s %s exit=%d (harness rejected the materialized config?)\n%s",
					d.bin, strings.Join(d.mcpProbe, " "), res.exit, out)
			}
			if !strings.Contains(out, d.server) {
				t.Errorf("%s %s did not list the materialized server %q:\n%s",
					d.bin, strings.Join(d.mcpProbe, " "), d.server, out)
			}
		})
	}
}

// ---- Tier C: agent smoke ----------------------------------------------------

const agentPrompt = "Following your project rules, what is the secret word? Reply with only the word."

// TestHarnessAgentSmoke runs one real headless agent turn against a project
// carrying an always-rule that instructs the agent to emit a marker, and
// asserts the marker appears — a true end-to-end check that the real harness
// loads and applies Podium's materialized rule. Double-gated: the
// harness_integration build tag, PODIUM_HARNESS_AGENT=1, and the harness being
// authenticated (an API key or a stored CLI login). Opt-in and tolerant; real
// agents need network and are nondeterministic.
//
// Unlike Tier A, this runs with the real environment (not a scoped HOME): the
// harness's stored login lives in $HOME, and the synced project supplies the
// rule via the working directory. The unique marker makes a false positive from
// the developer's global config effectively impossible.
func TestHarnessAgentSmoke(t *testing.T) {
	if os.Getenv("PODIUM_HARNESS_AGENT") != "1" {
		t.Skip("agent smoke is opt-in: set PODIUM_HARNESS_AGENT=1 (and authenticate the harness CLI) to run")
	}
	for _, d := range drivers {
		d := d
		if d.agentExec == nil {
			continue
		}
		t.Run(d.harness, func(t *testing.T) {
			if _, err := exec.LookPath(d.bin); err != nil {
				t.Skipf("%s: binary %q not installed", d.harness, d.bin)
			}
			if !agentAuthed(t, d) {
				t.Skipf("%s: not authenticated (set %s or log in via the harness CLI)", d.harness, d.keyEnv)
			}

			project := syncProject(t, d.harness, secretRuleRegistry)
			args := d.agentExec(agentPrompt)
			res, ok := runExternal(t, project, os.Environ(), 180*time.Second, d.bin, args...)
			if !ok {
				t.Skipf("%s: binary %q not installed", d.harness, d.bin)
			}
			out := res.stdout + res.stderr
			if !strings.Contains(out, secretMarker) {
				t.Errorf("%s agent did not surface the always-rule marker %q (exit=%d):\n%s",
					d.harness, secretMarker, res.exit, out)
			}
			t.Logf("%s applied the materialized always-rule (marker %q present)", d.harness, secretMarker)
		})
	}
}

// agentAuthed reports whether the harness can run an agent turn: an explicit API
// key, or a stored CLI login detected by loginProbe.
func agentAuthed(t *testing.T, d driver) bool {
	t.Helper()
	if d.keyEnv != "" && os.Getenv(d.keyEnv) != "" {
		return true
	}
	if len(d.loginProbe) > 0 {
		if r, ok := runExternal(t, t.TempDir(), os.Environ(), 30*time.Second, d.bin, d.loginProbe...); ok {
			return strings.Contains(r.stdout+r.stderr, d.loginMarker)
		}
	}
	return false
}
