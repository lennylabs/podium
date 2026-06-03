package sync

import (
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

// spec: §7.5.3 / §14.11 (F-14.11.2) — when --target already names the harness
// config directory (e.g. ./build/.claude/), the adapter's config-dir prefix is
// stripped from each emitted path so the on-disk tree is not doubled
// (.claude/.claude/…) and the lock records the spec's relative materialized_path
// (agents/pay-invoice.md). Neutral buckets and a workspace-root target are left
// untouched.
func TestStripHarnessConfigPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		target string
		in     string
		want   string
	}{
		{
			name:   "target names config dir strips leading segment",
			target: filepath.Join("build", ".claude"),
			in:     ".claude/agents/pay-invoice.md",
			want:   "agents/pay-invoice.md",
		},
		{
			name:   "trailing separator on target still matches",
			target: filepath.Join("build", ".claude") + string(filepath.Separator),
			in:     ".claude/skills/greet/SKILL.md",
			want:   "skills/greet/SKILL.md",
		},
		{
			name:   "absolute config-dir target matches",
			target: string(filepath.Separator) + filepath.Join("Users", "alice", ".claude"),
			in:     ".claude/rules/style.md",
			want:   "rules/style.md",
		},
		{
			name:   "neutral .podium bucket is untouched",
			target: filepath.Join("build", ".claude"),
			in:     ".podium/resources/finance/ap/x.sh",
			want:   ".podium/resources/finance/ap/x.sh",
		},
		{
			name:   "single-segment path (no slash) is untouched",
			target: filepath.Join("build", ".claude"),
			in:     ".mcp.json",
			want:   ".mcp.json",
		},
		{
			name:   "workspace-root target keeps the config prefix",
			target: filepath.Join("home", "alice", "project"),
			in:     ".claude/agents/pay-invoice.md",
			want:   ".claude/agents/pay-invoice.md",
		},
		{
			name:   "dot target is a no-op (defaults to CWD)",
			target: ".",
			in:     ".claude/agents/pay-invoice.md",
			want:   ".claude/agents/pay-invoice.md",
		},
		{
			name:   "matching segment with empty remainder is untouched",
			target: filepath.Join("build", ".claude"),
			in:     ".claude/",
			want:   ".claude/",
		},
		{
			name:   "cursor config-dir target strips .cursor",
			target: filepath.Join("home", "alice", ".cursor"),
			in:     ".cursor/rules/naming.mdc",
			want:   "rules/naming.mdc",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := []adapter.File{{Path: tc.in}}
			stripHarnessConfigPrefix(tc.target, files)
			if files[0].Path != tc.want {
				t.Errorf("stripHarnessConfigPrefix(%q, %q) = %q, want %q",
					tc.target, tc.in, files[0].Path, tc.want)
			}
		})
	}
}
