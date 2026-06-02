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
// Tier C (TestHarnessArtifactTypes) is double-gated (this build tag plus
// PODIUM_HARNESS_AGENT=1 and the harness being authenticated, via an API key or
// a stored CLI login). It materializes each artifact type and drives a real
// headless agent turn per type, asserting the harness surfaces the type's unique
// marker (in the reply for rule/skill/command, or in the hook's side-effect file
// for hook). Each (harness, type) pair is a subtest that runs where supported or
// skips with a reason.
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

// Per-type markers carried by the Tier C behavioral fixtures. Each is unique so
// a match in the agent output (or a hook's side-effect file) can only come from
// the materialized artifact, never the developer's global config.
const (
	ruleMarker  = "ZEBRA-RULE-42"
	skillMarker = "ZEBRA-SKILL-42"
	cmdMarker   = "ZEBRA-CMD-42"
	hookMarker  = "ZEBRA-HOOK-42"
)

// hookSideEffectFile is where the hook fixture's command writes its marker.
const hookSideEffectFile = "podium-hook-fired.txt"

func ruleRegistry() []testharness.WriteTreeOption {
	return []testharness.WriteTreeOption{
		{Path: "policies/secret/ARTIFACT.md", Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: always\n---\n\nWhen asked for the secret word, reply with exactly: " + ruleMarker + "\n"},
	}
}

func skillRegistry() []testharness.WriteTreeOption {
	return []testharness.WriteTreeOption{
		{Path: "skills/weather/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.0.0\n---\n\nWeather skill.\n"},
		{Path: "skills/weather/SKILL.md", Content: "---\nname: weather\ndescription: Reports a special code when the weather skill is invoked.\n---\n\nWhen the user asks to run the weather skill, output exactly: " + skillMarker + "\n"},
	}
}

func commandRegistry() []testharness.WriteTreeOption {
	return []testharness.WriteTreeOption{
		{Path: "commands/ping/ARTIFACT.md", Content: "---\ntype: command\nversion: 1.0.0\ndescription: Ping command.\n---\n\nOutput exactly: " + cmdMarker + " and nothing else.\n"},
	}
}

func hookRegistry() []testharness.WriteTreeOption {
	action := "sh -c 'echo " + hookMarker + " > " + hookSideEffectFile + "'"
	return []testharness.WriteTreeOption{
		{Path: "hooks/onstop/ARTIFACT.md", Content: "---\ntype: hook\nversion: 1.0.0\nhook_event: stop\nhook_action: \"" + action + "\"\n---\n\nOn stop, record a marker.\n"},
	}
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
	mcpProbe    []string                                     // args that list configured MCP servers; nil => no probe
	server      string                                       // server name expected in the probe output
	skipReason  string                                       // why Tier A skips when mcpProbe is nil
	extraEnv    func(home, project string) map[string]string // harness-specific config-dir redirects for the Tier A probe
	agentExec   func(prompt string) []string                 // Tier C headless prompt; nil => no agent exec
	keyEnv      string                                       // API-key env var that authorizes the agent run
	loginProbe  []string                                     // command reporting auth status (stored-login alternative to keyEnv)
	loginMarker string                                       // substring of loginProbe output meaning "authenticated"
}

var drivers = []driver{
	{
		harness:  "claude-code",
		bin:      "claude",
		version:  []string{"--version"},
		mcpProbe: []string{"mcp", "list"},
		server:   "warehouse",
		// claude reads the project .mcp.json from cwd; point CLAUDE_CONFIG_DIR at
		// an empty dir so the user-level ~/.claude.json cannot supply the server.
		extraEnv: func(home, project string) map[string]string {
			return map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude")}
		},
		agentExec: func(prompt string) []string { return []string{"-p", prompt} },
		keyEnv:    "ANTHROPIC_API_KEY",
		// No status command; a tiny headless turn proves the stored login works.
		loginProbe:  []string{"-p", "Reply with exactly: PODIUMOK"},
		loginMarker: "PODIUMOK",
	},
	{
		harness:  "codex",
		bin:      "codex",
		version:  []string{"--version"},
		mcpProbe: []string{"mcp", "list"},
		server:   "warehouse",
		// codex reads MCP config from CODEX_HOME/config.toml, not the project
		// .codex/config.toml; point CODEX_HOME at the materialized .codex dir so
		// `codex mcp list` validates the config.toml format Podium wrote.
		extraEnv: func(home, project string) map[string]string {
			return map[string]string{"CODEX_HOME": filepath.Join(project, ".codex")}
		},
		agentExec:   func(prompt string) []string { return []string{"exec", "--skip-git-repo-check", prompt} },
		keyEnv:      "OPENAI_API_KEY",
		loginProbe:  []string{"login", "status"},
		loginMarker: "Logged in",
	},
	{
		// Gemini CLI. `gemini mcp list` reads the project .gemini/settings.json,
		// but Podium tags each merged entry with x-podium-id for reconciliation
		// and Gemini's settings schema is strict ("Unrecognized key(s) in object:
		// 'x-podium-id'"), so it flags the config invalid (the server still lists,
		// with a loud warning). Gemini also gates project config behind folder
		// trust (--skip-trust). mcp-server is demoted to a skip until the
		// reconciliation marker moves out of the entry; rule/skill/command run via
		// Tier C with --skip-trust.
		harness:    "gemini",
		bin:        "gemini",
		version:    []string{"--version"},
		skipReason: "gemini settings.json schema rejects Podium's x-podium-id reconciliation key as an unrecognized key; mcp-server and hook need the marker moved out of the merged entry",
		agentExec:  func(prompt string) []string { return []string{"--skip-trust", "--yolo", "-p", prompt} },
		keyEnv:     "GEMINI_API_KEY",
		// OAuth login (no status command); a tiny headless turn proves it works.
		loginProbe:  []string{"--skip-trust", "-p", "Reply with exactly: PODIUMOK"},
		loginMarker: "PODIUMOK",
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
		// `status` command), so the agent run accepts that or CURSOR_API_KEY.
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

			// Record the harness version even when there is no config probe, so
			// the format the suite targets is pinned to a concrete version.
			if len(d.version) > 0 {
				if v, ok := runExternal(t, home, scopedEnv(home, nil), 30*time.Second, d.bin, d.version...); ok {
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
			var extra map[string]string
			if d.extraEnv != nil {
				extra = d.extraEnv(home, project)
			}
			env := scopedEnv(home, extra)
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

// ---- Tier C: per-type agent behavior ----------------------------------------

// behavior is one artifact type exercised through a real agent turn: a fixture
// carrying a unique marker, a prompt that should make the harness surface it,
// and the harnesses where that is expected to work (verified empirically). A
// hook is checked by its side-effect file rather than the agent's text.
type behavior struct {
	typ        string
	registry   []testharness.WriteTreeOption
	prompt     string
	marker     string
	sideEffect string            // relative file to read instead of stdout (hooks)
	run        []string          // harnesses that materialize+consume this type
	skip       map[string]string // harness -> reason it is not exercised
}

// behaviors covers the artifact types reachable through a single headless agent
// turn. mcp-server is covered by Tier A (TestHarnessConfigAccept); context is a
// harness-neutral .podium/context/ directory no harness loads natively; agent
// (subagent) delegation is model-dependent and omitted. The run/skip sets encode
// what was verified against the installed CLIs (claude 2.1.x, cursor-agent
// 2026.06, codex 0.136).
var behaviors = []behavior{
	{
		typ: "rule", registry: ruleRegistry(),
		prompt: "What is the secret word? Reply with only the word.", marker: ruleMarker,
		run: []string{"claude-code", "cursor", "codex", "gemini"},
	},
	{
		typ: "skill", registry: skillRegistry(),
		prompt: "Run the weather skill now.", marker: skillMarker,
		run: []string{"claude-code", "cursor", "codex", "gemini"},
	},
	{
		typ: "command", registry: commandRegistry(),
		prompt: "/ping", marker: cmdMarker,
		run:  []string{"claude-code", "cursor", "gemini"},
		skip: map[string]string{"codex": "command is ✗ for codex (§6.7.1): folded into skills"},
	},
	{
		typ: "hook", registry: hookRegistry(),
		prompt: "Reply with only: hi", marker: hookMarker, sideEffect: hookSideEffectFile,
		run: []string{"claude-code"},
		skip: map[string]string{
			// Materialization is correct in both cases; the limitation is the
			// harness's non-interactive runtime, not Podium's output.
			"cursor": "materialized .cursor/hooks.json is correct (verified against cursor-agent's projectConfigPath + stop event); cursor-agent --print does not run the stop lifecycle hook in headless mode",
			"codex":  "materialized .codex/config.toml [[hooks.Stop]] is the correct native schema (verified with codex --strict-config); codex exec does not fire config.toml lifecycle hooks (not even SessionStart) in codex-cli 0.136.0",
			// stop now maps to AfterAgent (gemini has no Stop event), but the
			// settings.json entry still carries x-podium-id, which gemini's strict
			// schema rejects; fix the reconciliation marker before claiming this.
			"gemini": "gemini maps stop->AfterAgent, but the .gemini/settings.json hook entry carries x-podium-id, which gemini's strict schema flags invalid; needs the reconciliation marker moved out of the entry",
		},
	},
}

// TestHarnessArtifactTypes materializes each artifact type through the real
// `podium sync` and drives the real harness agent to confirm it loads and
// applies the materialized artifact — a true end-to-end check per type. Each
// type's marker is unique, so a match can only come from Podium's output.
//
// Double-gated: the harness_integration build tag, PODIUM_HARNESS_AGENT=1, and
// the harness being authenticated. It runs with the real environment (the
// harness's stored login lives in $HOME; the synced project supplies the
// artifact via the working directory). Opt-in and tolerant; real agents need
// network and are nondeterministic.
func TestHarnessArtifactTypes(t *testing.T) {
	if os.Getenv("PODIUM_HARNESS_AGENT") != "1" {
		t.Skip("opt-in: set PODIUM_HARNESS_AGENT=1 (and authenticate the harness CLI) to run")
	}
	for _, harness := range []string{"claude-code", "cursor", "codex", "gemini"} {
		harness := harness
		d, found := driverFor(harness)
		t.Run(harness, func(t *testing.T) {
			if !found || d.agentExec == nil {
				t.Skipf("%s: no agent driver", harness)
			}
			if _, err := exec.LookPath(d.bin); err != nil {
				t.Skipf("%s: binary %q not installed", harness, d.bin)
			}
			if !agentAuthed(t, d) {
				t.Skipf("%s: not authenticated (set %s or log in via the harness CLI)", harness, d.keyEnv)
			}
			for _, b := range behaviors {
				b := b
				t.Run(b.typ, func(t *testing.T) {
					if reason, ok := b.skip[harness]; ok {
						t.Skipf("%s/%s: %s", harness, b.typ, reason)
					}
					if !contains(b.run, harness) {
						t.Skipf("%s/%s: not applicable", harness, b.typ)
					}
					project := syncProject(t, harness, b.registry)
					res, ok := runExternal(t, project, os.Environ(), 180*time.Second, d.bin, d.agentExec(b.prompt)...)
					if !ok {
						t.Skipf("%s: binary %q not installed", harness, d.bin)
					}
					raw := res.stdout + res.stderr
					// A credential revoked or expired mid-run (the loginProbe can
					// report a stale "logged in") is an auth failure, not a
					// materialization failure, so skip rather than fail.
					if sig := harnessAuthError(raw); sig != "" {
						t.Skipf("%s/%s: harness auth unavailable at run time (%q); not a materialization result", harness, b.typ, sig)
					}
					got := raw
					if b.sideEffect != "" {
						got = readSideEffect(project, b.sideEffect)
					}
					if !strings.Contains(got, b.marker) {
						t.Errorf("%s/%s: marker %q absent (exit=%d):\n%s", harness, b.typ, b.marker, res.exit, truncate(got, 1200))
						return
					}
					t.Logf("%s/%s: materialized and consumed (marker %q present)", harness, b.typ, b.marker)
				})
			}
		})
	}
}

// harnessAuthError returns a matched signature when the harness output shows an
// authentication failure (a revoked, expired, or missing credential). The
// signatures are credential-specific so ordinary model text does not trip them.
// A loginProbe can report a stale "logged in" after the token is revoked
// server-side, so the run itself is the authoritative auth signal.
func harnessAuthError(out string) string {
	lower := strings.ToLower(out)
	for _, sig := range []string{
		"token_revoked", "invalidated oauth token", "401 unauthorized",
		"invalid_api_key", "invalid api key", "authentication_error", "not logged in",
	} {
		if strings.Contains(lower, sig) {
			return sig
		}
	}
	return ""
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

func driverFor(harness string) (driver, bool) {
	for _, d := range drivers {
		if d.harness == harness {
			return d, true
		}
	}
	return driver{}, false
}

func readSideEffect(project, rel string) string {
	b, err := os.ReadFile(filepath.Join(project, rel))
	if err != nil {
		return ""
	}
	return string(b)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
