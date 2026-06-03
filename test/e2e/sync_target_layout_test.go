package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// spec: §7.5.3 / §14.11 (F-14.11.2) — the §14.11 CI pipeline runs
// `podium sync --harness claude-code … --target ./build/.claude/`, pointing
// --target at the harness config directory itself. The claude-code adapter
// prefixes .claude/ onto every emitted path, so without target normalization the
// result is a doubled ./build/.claude/.claude/agents/… tree and a lock recording
// materialized_path: .claude/agents/… rather than the §7.5.3 example's
// agents/pay-invoice.md. This drives the real binary against a filesystem source
// and asserts the single-.claude/ layout and the relative lock path.
func TestSync_TargetNamesHarnessConfigDir_NoDoubledClaude(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: agent\nversion: 1.2.0\ndescription: Pay an invoice.\n---\n\nPay-invoice body.\n",
	})
	// --target ./build/.claude/ as in the §14.11 pipeline step.
	target := filepath.Join(t.TempDir(), "build", ".claude")
	chSync(t, reg, target, "claude-code")

	// The agent lands a single .claude/ deep, directly under the target.
	got := readFile(t, filepath.Join(target, "agents", "pay-invoice.md"))
	if !strings.Contains(got, "Pay-invoice body.") {
		t.Errorf("agent file missing body:\n%s", got)
	}
	// No doubled .claude/.claude/ segment.
	mustNotExist(t, filepath.Join(target, ".claude"))

	// The committed lock (./build/.claude/.podium/sync.lock per §14.11 step 3)
	// records the materialized_path relative to the target.
	lock := readFile(t, filepath.Join(target, ".podium", "sync.lock"))
	if !strings.Contains(lock, "materialized_path: agents/pay-invoice.md") {
		t.Errorf("lock missing relative materialized_path:\n%s", lock)
	}
	if strings.Contains(lock, "materialized_path: .claude/") {
		t.Errorf("lock recorded a doubled .claude/ materialized_path:\n%s", lock)
	}
}

// spec: §6.7 / §7.5 — the control case: a workspace-root --target (not naming
// the harness config dir) keeps the adapter's .claude/ prefix, so the agent
// lands at <target>/.claude/agents/pay-invoice.md. The normalization must not
// over-trigger on an ordinary workspace target.
func TestSync_WorkspaceRootTarget_KeepsClaudePrefix(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: agent\nversion: 1.2.0\ndescription: Pay an invoice.\n---\n\nPay-invoice body.\n",
	})
	target := filepath.Join(t.TempDir(), "workspace")
	chSync(t, reg, target, "claude-code")

	mustExist(t, filepath.Join(target, ".claude", "agents", "pay-invoice.md"))
	lock := readFile(t, filepath.Join(target, ".podium", "sync.lock"))
	if !strings.Contains(lock, "materialized_path: .claude/agents/pay-invoice.md") {
		t.Errorf("workspace-root lock should keep the .claude/ prefix:\n%s", lock)
	}
}
