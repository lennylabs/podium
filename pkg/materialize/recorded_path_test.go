package materialize

import (
	"errors"
	"runtime"
	"testing"
)

// Spec: §7.5.3 — a recorded path is used only when it is a non-empty
// slash-separated relative path with no empty, ".", "..", or ".git" segment,
// no backslash, no control character, and no volume name, and it does not
// name the sync's own state under .podium/.
func TestCheckRecordedPath(t *testing.T) {
	t.Parallel()
	refused := []string{
		"",
		"../x",
		"../victim/file.txt",
		"a/../../x",
		"a/..",
		"/abs",
		"/etc/passwd",
		`a\b`,
		`..\x`,
		`pack/a/notes\x.md`,
		".git/HEAD",
		"sub/.git/config",
		".GIT/HEAD",
		".podium/sync.lock",
		".podium/sync.lock.tmp",
		".podium/sync.yaml",
		".podium/sync.local.yaml",
		".podium/publish-change-summary.json",
		".podium/overlay/x/ARTIFACT.md",
		".PODIUM/sync.lock",
		".podium/resources",
		"a//b",
		"a/",
		"./a",
		"a/./b",
		"a\x00b",
		"a\nb",
		"a\x7fb",
	}
	if runtime.GOOS == "windows" {
		refused = append(refused, "C:/x", "C:x")
	}
	for _, p := range refused {
		if err := CheckRecordedPath(p); !errors.Is(err, ErrUnsafeRecordedPath) {
			t.Errorf("CheckRecordedPath(%q) = %v, want ErrUnsafeRecordedPath", p, err)
		}
	}
	accepted := []string{
		"agents/pay-invoice.md",
		".claude/skills/hello/SKILL.md",
		".claude/settings.json",
		".podium/resources/finance/ap/pay-invoice/run.sh",
		"claude/finance-pack/skills/pay-invoice/SKILL.md",
		".gitignore",
		"a/.github/workflows/x.yml",
		"..foo/bar",
		"AGENTS.md",
	}
	for _, p := range accepted {
		if err := CheckRecordedPath(p); err != nil {
			t.Errorf("CheckRecordedPath(%q) = %v, want nil", p, err)
		}
	}
}
