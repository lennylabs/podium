package e2e

// End-to-end tests for a registry of plain Agent Skills directories. Each
// skill in the fixture testdata/registries/plain-skills is a SKILL.md with its
// bundled resources and no ARTIFACT.md, laid out as a skills repository: the
// skills sit under skills/, beside a .github/workflows directory and a
// top-level README.md, and no .registry-config makes the tree multi-layer.
// The tests sync the fixture through a filesystem source, serve it from a
// standalone server that signs at ingest, load a skill through the MCP bridge
// with signature verification on, and sync it from that server. greet declares
// no version, so the registry assigns one, and summarize declares
// metadata.version 1.0.0. greet bundles examples/nested/SKILL.md, which is a
// resource of greet rather than a package of its own.
//
// Spec: §4.2, §4.3.4, §4.4, §6.7, §13.11.3.

import (
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
	podsync "github.com/lennylabs/podium/pkg/sync"
)

// plainSkillGreetDocument is the ARTIFACT.md the registry serves for greet at
// the version a filesystem source and a first ingest assign. It is copied from
// the §4.3.4 synthesized-document bytes rather than built with the
// pkg/manifest helper, so a change to the helper fails this test.
const plainSkillGreetDocument = "---\ntype: skill\nversion: 0.0.0-auto.0\n---\n" +
	"<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n"

// plainSkillsFixture returns the absolute path of the plain-skills fixture.
// Every case reads it in place; nothing writes into it.
func plainSkillsFixture(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "testdata", "registries", "plain-skills")
}

// lockVersions returns the version the sync.lock of target records for each
// artifact ID. A skill contributes one lock entry per materialized file, and
// every entry of one ID carries the same version.
func lockVersions(t *testing.T, target string) map[string]string {
	t.Helper()
	lock, err := podsync.ReadLock(target)
	if err != nil {
		t.Fatalf("read sync.lock: %v", err)
	}
	out := map[string]string{}
	for _, a := range lock.Artifacts {
		if prev, ok := out[a.ID]; ok && prev != a.Version {
			t.Errorf("lock records %s at %q and %q", a.ID, prev, a.Version)
		}
		out[a.ID] = a.Version
	}
	return out
}

// assertLockVersions fails unless the lock of target records exactly want.
func assertLockVersions(t *testing.T, target string, want map[string]string) {
	t.Helper()
	got := lockVersions(t, target)
	if len(got) != len(want) {
		t.Errorf("lock IDs = %v, want %v", got, want)
	}
	for id, v := range want {
		if got[id] != v {
			t.Errorf("lock version of %s = %q, want %q", id, got[id], v)
		}
	}
}

// relFiles returns the regular files under root as sorted slash paths
// relative to root.
func relFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return rerr
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// assertNoArtifactManifest fails when any file under root is named
// ARTIFACT.md, because a plain skill materializes as a plain Agent Skills
// directory.
func assertNoArtifactManifest(t *testing.T, root string) {
	t.Helper()
	for _, rel := range relFiles(t, root) {
		if filepath.Base(rel) == "ARTIFACT.md" {
			t.Errorf("materialized %s, want no ARTIFACT.md", rel)
		}
	}
}

// assertGreetSkillDir checks one materialized greet skill directory: SKILL.md
// is byte-identical to the source, and the bundled resources, the nested
// SKILL.md among them, sit beside it.
func assertGreetSkillDir(t *testing.T, fixtureSkills, dir string) {
	t.Helper()
	if got, want := readFile(t, filepath.Join(dir, "SKILL.md")), readFile(t, filepath.Join(fixtureSkills, "greet", "SKILL.md")); got != want {
		t.Errorf("materialized greet SKILL.md differs from the source:\n%s", got)
	}
	for _, rel := range []string{"scripts/hello.sh", "references/notes.md", "examples/nested/SKILL.md"} {
		mustExist(t, filepath.Join(dir, filepath.FromSlash(rel)))
	}
}

// Spec: §4.2, §4.3.4, §4.4, §6.7, §13.11.3 — a filesystem-source sync of a
// directory of plain skills to claude-code materializes each skill at
// .claude/skills/<name>/, keeps the nested SKILL.md as a resource of greet
// rather than a skill of its own, writes no ARTIFACT.md, and records
// 0.0.0-auto.0 for the version-less greet and the metadata.version 1.0.0 for
// summarize.
func TestPlainSkills_FilesystemSourceSync(t *testing.T) {
	t.Parallel()
	skills := filepath.Join(plainSkillsFixture(t), "skills")
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", skills, "--target", target, "--harness", "claude-code")
	cliWantExit(t, res, 0, "filesystem-source sync")

	skillsDir := filepath.Join(target, ".claude", "skills")
	assertGreetSkillDir(t, skills, filepath.Join(skillsDir, "greet"))
	mustExist(t, filepath.Join(skillsDir, "summarize", "SKILL.md"))
	mustNotExist(t, filepath.Join(skillsDir, "nested"))
	assertNoArtifactManifest(t, target)
	assertLockVersions(t, target, map[string]string{"greet": "0.0.0-auto.0", "summarize": "1.0.0"})
}

// Spec: §4.2, §13.11.3 — a sync rooted at the repository finds the skills
// under skills/ by their SKILL.md alone, and the .github directory and the
// top-level README.md, which hold no package, materialize nothing.
func TestPlainSkills_RepositoryRootSync(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", plainSkillsFixture(t), "--target", target, "--harness", "claude-code")
	cliWantExit(t, res, 0, "repository-root sync")

	assertLockVersions(t, target, map[string]string{"skills/greet": "0.0.0-auto.0", "skills/summarize": "1.0.0"})
	for _, rel := range relFiles(t, target) {
		if strings.Contains(rel, ".github") || strings.Contains(rel, "ci.yml") || filepath.Base(rel) == "README.md" {
			t.Errorf("materialized %s from outside the skill packages", rel)
		}
	}
	mustExist(t, filepath.Join(target, ".claude", "skills", "greet", "SKILL.md"))
	mustExist(t, filepath.Join(target, ".claude", "skills", "summarize", "SKILL.md"))
}

// Spec: §4.3.4, §4.7.10, §6.7, §13.11.3 — a standalone server that signs at
// ingest serves a plain skill's synthesized ARTIFACT.md with the exact §4.3.4
// bytes in the load_artifact frontmatter member. The bridge verifies the
// delivery signature under always and materializes the skill with no
// ARTIFACT.md, and a server-source sync records the served versions and also
// writes no ARTIFACT.md.
func TestPlainSkills_SignedServerDelivery(t *testing.T) {
	t.Parallel()
	skills := filepath.Join(plainSkillsFixture(t), "skills")
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--sign", "registry-key", "--layer-path", skills)

	t.Run("load_artifact serves the synthesized document", func(t *testing.T) {
		var raw map[string]any
		getJSON(t, srv.BaseURL+"/v1/load_artifact?id=greet", &raw)
		if got, _ := raw["frontmatter"].(string); got != plainSkillGreetDocument {
			t.Errorf("frontmatter = %q, want %q", got, plainSkillGreetDocument)
		}
		if got, _ := raw["version"].(string); got != "0.0.0-auto.0" {
			t.Errorf("version = %q, want 0.0.0-auto.0", got)
		}
	})
	t.Run("bridge load verifies and omits ARTIFACT.md", func(t *testing.T) {
		kf, err := sign.ReadKeyFile(homeKeyFile(srv.Home))
		if err != nil {
			t.Fatalf("read the registry key: %v", err)
		}
		root := t.TempDir()
		errStr, res := bridgeLoad(t, srv.BaseURL, "greet",
			"PODIUM_SIGNATURE_PROVIDER=registry-managed",
			"PODIUM_SIGNATURE_VERIFY_KEY="+base64.StdEncoding.EncodeToString(kf.Public),
			"PODIUM_VERIFY_SIGNATURES=always",
			"PODIUM_MATERIALIZE_ROOT="+root)
		if errStr != "" {
			t.Fatalf("load of greet = %q, want success\nstderr: %s", errStr, res.Stderr)
		}
		assertGreetSkillDir(t, skills, materializedSkillDir(t, root))
		assertNoArtifactManifest(t, root)
	})
	t.Run("server-source sync records the served versions", func(t *testing.T) {
		target := t.TempDir()
		res := runPodium(t, "", srvSyncEnv(srv), "sync", "--registry", srv.BaseURL, "--target", target, "--harness", "none")
		cliWantExit(t, res, 0, "server-source sync")
		assertGreetSkillDir(t, skills, filepath.Join(target, "greet"))
		assertNoArtifactManifest(t, target)
		assertLockVersions(t, target, map[string]string{"greet": "0.0.0-auto.0", "summarize": "1.0.0"})
	})
}

// materializedSkillDir returns the directory under root that holds the
// materialized greet SKILL.md at its top level, so the assertion does not
// depend on how the bridge nests a load under its materialize root.
func materializedSkillDir(t *testing.T, root string) string {
	t.Helper()
	want := readFile(t, filepath.Join(plainSkillsFixture(t), "skills", "greet", "SKILL.md"))
	for _, rel := range relFiles(t, root) {
		if filepath.Base(rel) != "SKILL.md" {
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		if b, err := os.ReadFile(p); err == nil && string(b) == want {
			return filepath.Dir(p)
		}
	}
	t.Fatalf("no materialized greet SKILL.md under %s: %v", root, relFiles(t, root))
	return ""
}
