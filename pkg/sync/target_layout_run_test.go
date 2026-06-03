package sync_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

const payInvoiceAgent = "---\ntype: agent\nversion: 1.2.0\ndescription: Pay an invoice.\n---\n\nPay-invoice agent body\n"

// writePayInvoiceRegistry stages a single-layer filesystem registry holding the
// finance/ap/pay-invoice agent and returns its root.
func writePayInvoiceRegistry(t *testing.T) string {
	t.Helper()
	registry := filepath.Join(t.TempDir(), "registry")
	dir := filepath.Join(registry, "finance", "ap", "pay-invoice")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ARTIFACT.md"), []byte(payInvoiceAgent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return registry
}

func lockPathFor(t *testing.T, target, id string) string {
	t.Helper()
	lock, err := sync.ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock(%s): %v", target, err)
	}
	if lock == nil {
		t.Fatalf("no lock written under %s", target)
	}
	for _, a := range lock.Artifacts {
		if a.ID == id {
			return a.MaterializedPath
		}
	}
	t.Fatalf("lock has no entry for %q: %+v", id, lock.Artifacts)
	return ""
}

// spec: §7.5.3 / §14.11 (F-14.11.2) — a --target that already names the harness
// config directory (./build/.claude/) must not produce a doubled .claude/.claude/
// tree, and the lock's materialized_path is recorded relative to the target
// (agents/pay-invoice.md), matching the §7.5.3 lock example.
func TestRun_TargetNamesHarnessConfigDir_NoDoubledClaude(t *testing.T) {
	t.Parallel()
	registry := writePayInvoiceRegistry(t)
	// A target whose final segment is the claude-code config dir, as in §14.11's
	// `--target ./build/.claude/`.
	target := filepath.Join(t.TempDir(), "build", ".claude")

	if _, err := sync.Run(sync.Options{
		RegistryPath: registry, Target: target, AdapterID: "claude-code",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The agent lands directly under the target, a single .claude/ deep.
	wantFile := filepath.Join(target, "agents", "pay-invoice.md")
	if _, err := os.Stat(wantFile); err != nil {
		t.Errorf("expected %s, stat err=%v", wantFile, err)
	}
	// No doubled .claude/.claude/ segment.
	if _, err := os.Stat(filepath.Join(target, ".claude")); !os.IsNotExist(err) {
		t.Errorf("doubled .claude/ present under target; stat err=%v", err)
	}

	// The lock records the path relative to the target, per the §7.5.3 example.
	if got := lockPathFor(t, target, "finance/ap/pay-invoice"); got != "agents/pay-invoice.md" {
		t.Errorf("materialized_path = %q, want %q", got, "agents/pay-invoice.md")
	}
}

// spec: §6.7 / §7.5 — a workspace-root target (one that does not name the
// harness config dir) keeps the adapter's .claude/ prefix, so the agent lands at
// <target>/.claude/agents/pay-invoice.md and the lock records that full path.
// This is the control case that the strip must not over-trigger on.
func TestRun_WorkspaceRootTarget_KeepsClaudePrefix(t *testing.T) {
	t.Parallel()
	registry := writePayInvoiceRegistry(t)
	target := filepath.Join(t.TempDir(), "workspace")

	if _, err := sync.Run(sync.Options{
		RegistryPath: registry, Target: target, AdapterID: "claude-code",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantFile := filepath.Join(target, ".claude", "agents", "pay-invoice.md")
	if _, err := os.Stat(wantFile); err != nil {
		t.Errorf("expected %s, stat err=%v", wantFile, err)
	}
	if got := lockPathFor(t, target, "finance/ap/pay-invoice"); got != ".claude/agents/pay-invoice.md" {
		t.Errorf("materialized_path = %q, want %q", got, ".claude/agents/pay-invoice.md")
	}
}
