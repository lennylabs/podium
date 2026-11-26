package materialize

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

// zwnj is U+200C ZERO WIDTH NON-JOINER, a code point HFS+ ignores when it
// compares names, so "\u200c.git" and ".g\u200cit" both name .git there.
const zwnj = "\u200c"

// Spec: §7.5.3 — a segment names .git when HFS+ or NTFS resolves it to .git:
// any letter case, a code point HFS+ ignores, the trailing spaces and periods
// NTFS strips, an alternate data stream after a colon, and the short name
// GIT~1. The near misses Git accepts stay accepted, GIT~2 among them, because
// Git places .git first in its directory so its short name is always GIT~1.
func TestAliasesDotGit(t *testing.T) {
	t.Parallel()
	aliases := []string{
		".git",
		".GIT",
		".Git",
		".gIT",
		zwnj + ".git",
		".g" + zwnj + "it",
		".git" + zwnj,
		"\u200d.git",
		"\u200e.GIT",
		"\u202e.git",
		"\u206f.git",
		"\ufeff.git",
		".g\u200c\u200dit",
		"GIT~1",
		"git~1",
		"Git~1",
		".git.",
		".git ",
		".git. .",
		"git~1.",
		"git~1 ",
		".git::$INDEX_ALLOCATION",
		".git:$DATA",
		".git:stream",
		".git. ::$INDEX_ALLOCATION",
		"GIT~1::$INDEX_ALLOCATION",
		`.git\config`,
		".git/",
		".git/x",
	}
	for _, seg := range aliases {
		if !AliasesDotGit(seg) {
			t.Errorf("AliasesDotGit(%q) = false, want true", seg)
		}
	}
	distinct := []string{
		"",
		".",
		"git",
		"GIT",
		".gi",
		".gitkeep",
		".gitignore",
		".gitattributes",
		".gitmodules",
		".github",
		".git-blame-ignore-revs",
		".git.bak",
		".git x",
		"x.git",
		"..git",
		"GIT~2",
		"git~4",
		"GIT~",
		"GIT~10",
		"git1",
		"podium",
		".g\u0131t",        // dotless i: HFS+ folds no non-ASCII letter for Git's needle
		".\u0261it",        // Latin small letter script g
		".git\u00a0",       // no-break space is neither ignored by HFS+ nor stripped by NTFS
		zwnj + "git",       // the ignored code point is not a period
		".gi\xfft",         // malformed UTF-8 in place of a needle character
		"\xff.git",         // malformed UTF-8 before the period
		".git\xff",         // malformed UTF-8 after the name: HFS+ percent-escapes it
		".g\u200cit\u00e9", // a kept code point after the name
	}
	for _, seg := range distinct {
		if AliasesDotGit(seg) {
			t.Errorf("AliasesDotGit(%q) = true, want false", seg)
		}
	}
}

// Spec: §7.5.3 — a segment names .podium under the rules that govern .git,
// with every NTFS short name Windows can assign .podium: PODIUM~1 through
// PODIUM~4, and the fallback form of PO, hexadecimal digits, a tilde, and a
// nonzero digit, eight characters in all.
func TestAliasesDotPodium(t *testing.T) {
	t.Parallel()
	aliases := []string{
		".podium",
		".PODIUM",
		".Podium",
		zwnj + ".podium",
		".pod" + zwnj + "ium",
		".podium" + "\ufeff",
		".podium.",
		".podium ",
		".podium::$INDEX_ALLOCATION",
		".podium:$DATA",
		"PODIUM~1",
		"podium~2",
		"Podium~3",
		"PODIUM~4",
		"PODIUM~1.",
		"PODIUM~1:x",
		"PO1A2B~1",
		"po1a2b~9",
		"POFFFF~5",
		"PO12~123",
		"PO~12345",
		"PO1A2~10",
		"PO1A2B~1 ",
		"PO1A2B~1::$INDEX_ALLOCATION",
	}
	for _, seg := range aliases {
		if !AliasesDotPodium(seg) {
			t.Errorf("AliasesDotPodium(%q) = false, want true", seg)
		}
	}
	distinct := []string{
		"",
		"podium",
		"PODIUM",
		".podiums",
		".podium-cache",
		".podium.bak",
		".podiu",
		"podium.md",
		"PODIUM~5",
		"PODIUM~0",
		"PODIUM~",
		"PODIUM~1x",
		"PO1A2B~0",
		"PO1A2G~1",
		"PO1A2B3~",
		"PO1A2B~1x",
		"PO1A2B~",
		"PX1A2B~1",
		"PO1A2BC~1",
		"PO1~",
		"PO\xff1A2~1",
		"PO1A2~1x",
		"po1a2b~1longer",
		".git",
		"GIT~1",
	}
	for _, seg := range distinct {
		if AliasesDotPodium(seg) {
			t.Errorf("AliasesDotPodium(%q) = true, want false", seg)
		}
	}
}

// equalFoldASCII compares only strings of equal length; its callers slice to
// the length of the name, and a length mismatch reports no match.
func TestEqualFoldASCII(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		s, lower string
		want     bool
	}{
		{"GiT", "git", true},
		{"git", "git", true},
		{"gi", "git", false},
		{"gitx", "git", false},
		{"g\xc4t", "git", false},
	} {
		if got := equalFoldASCII(tc.s, tc.lower); got != tc.want {
			t.Errorf("equalFoldASCII(%q, %q) = %v, want %v", tc.s, tc.lower, got, tc.want)
		}
	}
}

// Spec: §7.5.3 — the recorded-path rule refuses a path with a segment that
// names .git at any depth, and treats a first segment that names .podium as
// .podium, so an aliased path to the sync state is refused and an aliased
// path into the bundled-resource bucket is accepted like the literal one.
func TestCheckRecordedPath_RefusesAliasedReservedNames(t *testing.T) {
	t.Parallel()
	refused := []string{
		zwnj + ".git/config",
		".g" + zwnj + "it/config",
		".GIT/config",
		".Git/hooks/post-checkout",
		"GIT~1/config",
		"git~1/hooks/pre-commit",
		".git./config",
		".git /config",
		".git::$INDEX_ALLOCATION/config",
		".git:$DATA",
		"sub/GIT~1/config",
		".claude/skills/x/" + zwnj + ".git/config",
		zwnj + ".podium/sync.lock",
		".Podium/sync.yaml",
		"PODIUM~1/sync.lock",
		".podium./overlay/x/ARTIFACT.md",
		"PO1A2B~1/sync.lock",
		"PODIUM~1/resources",
	}
	for _, p := range refused {
		if err := CheckRecordedPath(p); !errors.Is(err, ErrUnsafeRecordedPath) {
			t.Errorf("CheckRecordedPath(%q) = %v, want ErrUnsafeRecordedPath", p, err)
		}
	}
	accepted := []string{
		".gitkeep",
		"a/.gitignore",
		".gitattributes",
		".github/workflows/ci.yml",
		"GIT~2/config",
		"git/config",
		"PODIUM~1/resources/finance/run.sh",
		"sub/.podium/sync.lock",
		".podiums/x",
	}
	for _, p := range accepted {
		if err := CheckRecordedPath(p); err != nil {
			t.Errorf("CheckRecordedPath(%q) = %v, want nil", p, err)
		}
	}
}

// Spec: §6.6, §7.5.3 — the writer refuses a batch with a path whose segment
// names .git under the aliasing rule before it writes anything, so neither
// the repository's .git nor the rest of the batch is touched.
func TestWrite_RefusesAliasedDotGitPath(t *testing.T) {
	t.Parallel()
	for _, p := range []string{zwnj + ".git/config", ".GIT/config", "GIT~1/config", ".git./config"} {
		dest := t.TempDir()
		gitConfig := filepath.Join(dest, ".git", "config")
		if err := os.MkdirAll(filepath.Dir(gitConfig), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		const original = "[core]\n\tbare = false\n"
		if err := os.WriteFile(gitConfig, []byte(original), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		err := Write(dest, []adapter.File{
			{Path: "agents/ok.md", Content: []byte("ok\n")},
			{Path: p, Content: []byte("[core]\n\tfsmonitor = touch /tmp/pwned\n")},
		})
		if !errors.Is(err, ErrUnconfinedPath) || !errors.Is(err, ErrUnsafeRecordedPath) {
			t.Errorf("Write(%q) = %v, want ErrUnconfinedPath wrapping ErrUnsafeRecordedPath", p, err)
		}
		if got, err := os.ReadFile(gitConfig); err != nil || string(got) != original {
			t.Errorf("Write(%q) changed .git/config: (%q, %v)", p, got, err)
		}
		if _, err := os.Lstat(filepath.Join(dest, "agents")); !os.IsNotExist(err) {
			t.Errorf("Write(%q) wrote part of a refused batch: lstat err = %v", p, err)
		}
	}
}
