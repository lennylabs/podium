package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
)

// runImport runs importCmd with args and returns its exit code and stderr.
func runImport(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var rc int
	stderr := captureStderr(t, func() {
		captureStdout(t, func() { rc = importCmd(args) })
	})
	return rc, stderr
}

// importedSkillMD returns the SKILL.md import wrote for id under target.
func importedSkillMD(t *testing.T, target, id string) []byte {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(target, id, "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md missing: %v", err)
	}
	return got
}

// assertEmptyDir fails when dir holds any entry.
func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("%s holds %d entries, want none", dir, len(entries))
	}
}

// bodyAfterDelimiter returns the bytes after the closing frontmatter
// delimiter of src.
func bodyAfterDelimiter(t *testing.T, src []byte) []byte {
	t.Helper()
	_, body, err := manifest.SplitFrontmatter(src)
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v", err)
	}
	return src[closingDelimiterEnd(src, body):]
}

// Spec: §4.3.4 — `podium import --source <skills-dir>` writes each
// subdirectory as a plain skill directory: SKILL.md with metadata.version
// set from --version, the bundled files as resources, and no ARTIFACT.md.
func TestImport_ConvertsSkillTree(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	mustWrite(t, filepath.Join(source, "hello-world", "SKILL.md"), "---\nname: hello\n---\nBody.")
	mustWrite(t, filepath.Join(source, "hello-world", "data.json"), `{"k":"v"}`)

	if rc, stderr := runImport(t, "--source", source, "--target", target, "--version", "1.2.3"); rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr=%s", rc, stderr)
	}
	if _, err := os.Stat(filepath.Join(target, "hello-world", "ARTIFACT.md")); !os.IsNotExist(err) {
		t.Errorf("ARTIFACT.md written (stat err=%v), want absent", err)
	}
	skill, err := manifest.ParseSkill(importedSkillMD(t, target, "hello-world"))
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	if skill.Metadata["version"] != "1.2.3" || skill.Name != "hello" || skill.Body != "Body." {
		t.Errorf("skill = %+v, want name hello, metadata.version 1.2.3, body Body.", skill)
	}
	if got, err := os.ReadFile(filepath.Join(target, "hello-world", "data.json")); err != nil || string(got) != `{"k":"v"}` {
		t.Errorf("bundled resource = %q, %v", got, err)
	}
}

// Spec: §4.3.4 — an empty source returns an error so the operator notices.
func TestImport_EmptySourceFails(t *testing.T) {
	rc, stderr := runImport(t, "--source", t.TempDir(), "--target", t.TempDir())
	if rc != 1 || !strings.Contains(stderr, "source directory contained no skills") {
		t.Errorf("rc = %d stderr=%q, want 1 and the no-skills message", rc, stderr)
	}
}

// Spec: §4.3.4 — --dry-run prints the plan without touching the target.
func TestImport_DryRun(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	mustWrite(t, filepath.Join(source, "x", "SKILL.md"), "---\nname: x\n---\n")
	if rc, stderr := runImport(t, "--source", source, "--target", target, "--dry-run"); rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr=%s", rc, stderr)
	}
	if _, err := os.Stat(filepath.Join(target, "x")); err == nil {
		t.Errorf("dry-run created target")
	}
}

// Spec: §4.3.4 — the --type flag is removed, so passing it is a usage error.
func TestImport_TypeFlagRemoved(t *testing.T) {
	source := t.TempDir()
	mustWrite(t, filepath.Join(source, "x", "SKILL.md"), "---\nname: x\n---\n")
	target := t.TempDir()
	if rc, _ := runImport(t, "--source", source, "--target", target, "--type", "skill"); rc != 2 {
		t.Errorf("rc = %d, want 2", rc)
	}
	assertEmptyDir(t, target)
}

// Spec: §4.3.4 — --source and --target are both required.
func TestImport_RequiresSourceAndTarget(t *testing.T) {
	if rc, stderr := runImport(t, "--source", t.TempDir()); rc != 2 || !strings.Contains(stderr, "required") {
		t.Errorf("rc = %d stderr=%q, want 2 and the required message", rc, stderr)
	}
}

// Spec: §4.3.4, §4.7.6 — --version must be major.minor.patch, which also
// refuses the registry-reserved 0.0.0-auto.<N> form, and a refused
// --version writes nothing.
func TestImport_InvalidVersionExits2(t *testing.T) {
	for _, v := range []string{"1.0", "0.0.0-auto.0"} {
		t.Run(v, func(t *testing.T) {
			source := t.TempDir()
			mustWrite(t, filepath.Join(source, "x", "SKILL.md"), "---\nname: x\n---\n")
			target := filepath.Join(t.TempDir(), "out")
			rc, stderr := runImport(t, "--source", source, "--target", target, "--version", v)
			if rc != 2 || !strings.Contains(stderr, "--version") {
				t.Errorf("rc = %d stderr=%q, want 2 naming --version", rc, stderr)
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Errorf("target created (stat err=%v)", err)
			}
		})
	}
}

// Spec: §4.3.4 — each refusal exits 1 naming the directory, before any
// write and also under --dry-run.
func TestImport_Refusals(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "source ARTIFACT.md",
			files: map[string]string{
				"ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n",
				"SKILL.md":    "---\nname: bad\n---\nbody\n",
			},
			want: "ARTIFACT.md",
		},
		{
			name: "nested ARTIFACT.md",
			files: map[string]string{
				"SKILL.md":        "---\nname: bad\n---\nbody\n",
				"sub/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\n---\n",
			},
			want: "sub/ARTIFACT.md",
		},
		{
			name: "several root md files and no SKILL.md",
			files: map[string]string{
				"a.md": "---\nname: a\n---\n",
				"b.md": "---\nname: b\n---\n",
			},
			want: "a.md, b.md",
		},
		{
			name:  "no frontmatter block",
			files: map[string]string{"SKILL.md": "just prose\n"},
			want:  "no frontmatter block",
		},
		{
			name:  "differing metadata.version",
			files: map[string]string{"SKILL.md": "---\nname: bad\nmetadata:\n  version: \"2.0.0\"\n---\nbody\n"},
			want:  `metadata.version "2.0.0" differs from --version "1.0.0"`,
		},
		{
			name:  "frontmatter not a mapping",
			files: map[string]string{"SKILL.md": "---\n- a\n---\nbody\n"},
			want:  "frontmatter is not a mapping",
		},
		{
			name:  "frontmatter does not parse",
			files: map[string]string{"SKILL.md": "---\nname: [unclosed\n---\nbody\n"},
			want:  "parse frontmatter",
		},
	}
	for _, tc := range cases {
		for _, dryRun := range []bool{false, true} {
			name := tc.name
			if dryRun {
				name += " dry-run"
			}
			t.Run(name, func(t *testing.T) {
				source := t.TempDir()
				mustWrite(t, filepath.Join(source, "good", "SKILL.md"), "---\nname: good\n---\nbody\n")
				for rel, body := range tc.files {
					mustWrite(t, filepath.Join(source, "bad", rel), body)
				}
				target := t.TempDir()
				args := []string{"--source", source, "--target", target}
				if dryRun {
					args = append(args, "--dry-run")
				}
				rc, stderr := runImport(t, args...)
				if rc != 1 {
					t.Fatalf("rc = %d, want 1; stderr=%s", rc, stderr)
				}
				if !strings.Contains(stderr, filepath.Join(source, "bad")) || !strings.Contains(stderr, tc.want) {
					t.Errorf("stderr = %q, want the directory and %q", stderr, tc.want)
				}
				assertEmptyDir(t, target)
			})
		}
	}
}

// Spec: §4.3.4, §4.7.6 — a metadata that is not a mapping, or a
// metadata.version that is not a string, fails with exit 1 naming the file.
func TestImport_MalformedMetadataFails(t *testing.T) {
	cases := map[string]struct{ header, want string }{
		"metadata sequence": {"metadata: [a]", "metadata is not a mapping"},
		"integer version":   {"metadata: {version: 1}", "metadata.version is not a string"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			source := t.TempDir()
			file := filepath.Join(source, "x", "SKILL.md")
			mustWrite(t, file, "---\nname: x\n"+tc.header+"\n---\nbody\n")
			target := t.TempDir()
			rc, stderr := runImport(t, "--source", source, "--target", target)
			if rc != 1 || !strings.Contains(stderr, file) || !strings.Contains(stderr, tc.want) {
				t.Errorf("rc = %d stderr=%q, want 1 naming %s and %q", rc, stderr, file, tc.want)
			}
			assertEmptyDir(t, target)
		})
	}
}

// Spec: §4.3.4, §4.7.6 — a SKILL.md whose metadata.version already equals
// --version is copied byte for byte, formatting and key order included.
func TestImport_EqualVersionCopiesBytes(t *testing.T) {
	source := t.TempDir()
	src := "---\n# kept comment\nname:   x\nmetadata:\n    version: 1.0.0\ndescription: 'quoted'\n---\n\n\nbody\n"
	mustWrite(t, filepath.Join(source, "x", "SKILL.md"), src)
	target := t.TempDir()
	if rc, stderr := runImport(t, "--source", source, "--target", target); rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr=%s", rc, stderr)
	}
	if got := importedSkillMD(t, target, "x"); string(got) != src {
		t.Errorf("SKILL.md = %q, want the source bytes %q", got, src)
	}
}

// Spec: §4.3.4, §4.7.6 — a SKILL.md without metadata gains a metadata
// mapping holding version, and every body byte after the closing delimiter
// survives, HTML comments and blank lines included.
func TestImport_PreservesBodyBytes(t *testing.T) {
	source := t.TempDir()
	src := []byte("---\nname: x\ndescription: Does x.\n---\n\n<!-- keep this comment -->\n\n\n# Heading\n\n<!--\nmulti-line\n-->\ntrailing   \n\n")
	mustWrite(t, filepath.Join(source, "x", "SKILL.md"), string(src))
	target := t.TempDir()
	if rc, stderr := runImport(t, "--source", source, "--target", target, "--version", "3.1.4"); rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr=%s", rc, stderr)
	}
	got := importedSkillMD(t, target, "x")
	want := "---\nname: x\ndescription: Does x.\nmetadata:\n  version: \"3.1.4\"\n---"
	if !bytes.HasPrefix(got, []byte(want)) {
		t.Errorf("SKILL.md header = %q, want prefix %q", got, want)
	}
	if b := bodyAfterDelimiter(t, got); !bytes.Equal(b, bodyAfterDelimiter(t, src)) {
		t.Errorf("body bytes = %q, want %q", b, bodyAfterDelimiter(t, src))
	}
}

// Spec: §4.3.4, §4.7.6 — version is inserted into an existing metadata
// mapping, and the re-marshaled header keeps the other keys and values.
func TestImport_InsertsIntoExistingMetadata(t *testing.T) {
	source := t.TempDir()
	src := []byte("---\nname: x\ndescription: Does x.\nlicense: MIT\nallowed-tools:\n  - Read\n  - Bash\n  - Grep\nmetadata:\n  author: alice\n---\nBody line.\n\n<!-- note -->\n")
	mustWrite(t, filepath.Join(source, "x", "SKILL.md"), string(src))
	target := t.TempDir()
	if rc, stderr := runImport(t, "--source", source, "--target", target); rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr=%s", rc, stderr)
	}
	got := importedSkillMD(t, target, "x")
	skill, err := manifest.ParseSkill(got)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	if skill.Metadata["version"] != "1.0.0" || skill.Metadata["author"] != "alice" || skill.License != "MIT" {
		t.Errorf("skill = %+v, want metadata.version 1.0.0, metadata.author alice, license MIT", skill)
	}
	if strings.Join(skill.AllowedTools, ",") != "Read,Bash,Grep" {
		t.Errorf("allowed-tools = %v, want [Read Bash Grep]", skill.AllowedTools)
	}
	if b := bodyAfterDelimiter(t, got); !bytes.Equal(b, bodyAfterDelimiter(t, src)) {
		t.Errorf("body bytes = %q, want %q", b, bodyAfterDelimiter(t, src))
	}
}

// Spec: §4.3.4, §4.7.6 — an empty frontmatter block gains a metadata
// mapping holding version.
func TestImport_EmptyFrontmatterGainsMetadata(t *testing.T) {
	source := t.TempDir()
	mustWrite(t, filepath.Join(source, "x", "SKILL.md"), "---\n\n---\nbody\n")
	target := t.TempDir()
	if rc, stderr := runImport(t, "--source", source, "--target", target); rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr=%s", rc, stderr)
	}
	if got, want := string(importedSkillMD(t, target, "x")), "---\nmetadata:\n  version: \"1.0.0\"\n---\nbody\n"; got != want {
		t.Errorf("SKILL.md = %q, want %q", got, want)
	}
}

// Spec: §4.3.4 — with no root SKILL.md, the single root-level .md file is
// the body and is written as SKILL.md.
func TestImport_SingleMarkdownFileIsBody(t *testing.T) {
	source := t.TempDir()
	mustWrite(t, filepath.Join(source, "x", "notes.md"), "---\nname: x\n---\nNotes body.\n")
	mustWrite(t, filepath.Join(source, "x", "scripts", "run.sh"), "echo hi\n")
	target := t.TempDir()
	if rc, stderr := runImport(t, "--source", source, "--target", target); rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr=%s", rc, stderr)
	}
	skill, err := manifest.ParseSkill(importedSkillMD(t, target, "x"))
	if err != nil || skill.Body != "Notes body.\n" {
		t.Errorf("SKILL.md = %+v, %v; want the notes.md body", skill, err)
	}
	if _, err := os.Stat(filepath.Join(target, "x", "notes.md")); !os.IsNotExist(err) {
		t.Errorf("notes.md copied as a resource (stat err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(target, "x", "scripts", "run.sh")); err != nil {
		t.Errorf("scripts/run.sh missing: %v", err)
	}
}

// Spec: §4.3.4, §4.4 — a SKILL.md in a subdirectory is copied verbatim as
// a bundled resource while the root SKILL.md stays the body, and other root
// .md files are resources.
func TestImport_NestedSkillMDIsResource(t *testing.T) {
	source := t.TempDir()
	nested := "---\nname: nested\n---\nNested body.\n"
	mustWrite(t, filepath.Join(source, "x", "SKILL.md"), "---\nname: x\n---\nRoot body.\n")
	mustWrite(t, filepath.Join(source, "x", "references", "SKILL.md"), nested)
	mustWrite(t, filepath.Join(source, "x", "README.md"), "readme\n")
	target := t.TempDir()
	if rc, stderr := runImport(t, "--source", source, "--target", target); rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr=%s", rc, stderr)
	}
	skill, err := manifest.ParseSkill(importedSkillMD(t, target, "x"))
	if err != nil || skill.Body != "Root body.\n" {
		t.Errorf("root SKILL.md = %+v, %v; want the root body", skill, err)
	}
	if got, err := os.ReadFile(filepath.Join(target, "x", "references", "SKILL.md")); err != nil || string(got) != nested {
		t.Errorf("nested SKILL.md = %q, %v; want the verbatim source", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(target, "x", "README.md")); err != nil || string(got) != "readme\n" {
		t.Errorf("README.md = %q, %v", got, err)
	}
}

// Spec: §4.3.4 — a subdirectory with no root-level .md file is skipped, so
// a skill nested one level deeper is not imported.
func TestImport_SkipsDirectoryWithoutRootMarkdown(t *testing.T) {
	source := t.TempDir()
	mustWrite(t, filepath.Join(source, "skills", "greet", "SKILL.md"), "---\nname: greet\n---\nbody\n")
	target := t.TempDir()
	rc, stderr := runImport(t, "--source", source, "--target", target)
	if rc != 1 || !strings.Contains(stderr, "source directory contained no skills") {
		t.Errorf("rc = %d stderr=%q, want 1 and the no-skills message", rc, stderr)
	}
	assertEmptyDir(t, target)

	mustWrite(t, filepath.Join(source, "top", "SKILL.md"), "---\nname: top\n---\nbody\n")
	if rc, stderr := runImport(t, "--source", source, "--target", target); rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr=%s", rc, stderr)
	}
	if _, err := os.Stat(filepath.Join(target, "skills")); !os.IsNotExist(err) {
		t.Errorf("skipped directory imported (stat err=%v)", err)
	}
	importedSkillMD(t, target, "top")
}

// Spec: §4.2, §4.3.4 — a dot-prefixed subdirectory of --source is skipped,
// so a Git clone's .github directory holding several .md files neither
// refuses the import nor becomes a skill.
func TestImport_SkipsDotPrefixedDirectories(t *testing.T) {
	source := t.TempDir()
	mustWrite(t, filepath.Join(source, ".github", "CONTRIBUTING.md"), "contributing\n")
	mustWrite(t, filepath.Join(source, ".github", "PULL_REQUEST_TEMPLATE.md"), "template\n")
	mustWrite(t, filepath.Join(source, ".hidden", "SKILL.md"), "---\nname: hidden\n---\nbody\n")
	mustWrite(t, filepath.Join(source, "greet", "SKILL.md"), "---\nname: greet\n---\nbody\n")
	target := t.TempDir()
	if rc, stderr := runImport(t, "--source", source, "--target", target); rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr=%s", rc, stderr)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "greet" {
		t.Errorf("target entries = %v, want only greet", entries)
	}
	importedSkillMD(t, target, "greet")
}

// Spec: §4.3.4 — an unreadable source fails with exit 1.
func TestImport_MissingSourceDirFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	if rc, _ := runImport(t, "--source", missing, "--target", t.TempDir()); rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
}

// Spec: §4.3.4 — a source file that cannot be read fails the import with
// exit 1 before any write.
func TestImport_UnreadableResourceFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	source := t.TempDir()
	mustWrite(t, filepath.Join(source, "x", "SKILL.md"), "---\nname: x\n---\nbody\n")
	locked := filepath.Join(source, "x", "secret.txt")
	mustWrite(t, locked, "data")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	target := t.TempDir()
	if rc, stderr := runImport(t, "--source", source, "--target", target); rc != 1 || !strings.Contains(stderr, "secret.txt") {
		t.Errorf("rc = %d stderr=%q, want 1 naming secret.txt", rc, stderr)
	}
	assertEmptyDir(t, target)
}

// Spec: §4.3.4 — a target that cannot be written fails with exit 1.
func TestImport_WriteFailureExits1(t *testing.T) {
	source := t.TempDir()
	mustWrite(t, filepath.Join(source, "x", "SKILL.md"), "---\nname: x\n---\nbody\n")
	t.Run("target under a file", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "file")
		mustWrite(t, blocker, "")
		if rc, _ := runImport(t, "--source", source, "--target", filepath.Join(blocker, "out")); rc != 1 {
			t.Errorf("rc = %d, want 1", rc)
		}
	})
	// Each blocker occupies a path the import writes, so the write that
	// reaches it fails.
	blockers := map[string]func(t *testing.T, target string){
		"skill directory is a file": func(t *testing.T, target string) {
			mustWrite(t, filepath.Join(target, "x"), "")
		},
		"SKILL.md is a directory": func(t *testing.T, target string) {
			mustWrite(t, filepath.Join(target, "x", "SKILL.md", "keep"), "")
		},
		"resource parent is a file": func(t *testing.T, target string) {
			mustWrite(t, filepath.Join(target, "x", "scripts"), "")
		},
		"resource is a directory": func(t *testing.T, target string) {
			mustWrite(t, filepath.Join(target, "x", "scripts", "run.sh", "keep"), "")
		},
	}
	mustWrite(t, filepath.Join(source, "x", "scripts", "run.sh"), "echo hi\n")
	for name, block := range blockers {
		t.Run(name, func(t *testing.T) {
			target := t.TempDir()
			block(t, target)
			if rc, stderr := runImport(t, "--source", source, "--target", target); rc != 1 || !strings.Contains(stderr, "write x") {
				t.Errorf("rc = %d stderr=%q, want 1 naming the skill", rc, stderr)
			}
		})
	}
}
