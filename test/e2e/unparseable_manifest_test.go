package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
)

// nestedMetadataSkill is a SKILL.md whose metadata holds a mapping, the
// authoring pattern a validation run over published skills found most often.
// §4.3.4 allows only string metadata values, so it does not parse.
const nestedMetadataSkill = "---\nname: bad\ndescription: The bad skill for tests. Use when the user needs bad.\n" +
	"metadata:\n  author:\n    name: alice\n    url: https://acme.com\n---\n\nbad body.\n"

// unparseableRejected returns the standard-error line podium lint, podium
// layer reingest, and a filesystem-source podium sync print for the package
// id whose SKILL.md src does not parse.
func unparseableRejected(t *testing.T, id, src string) string {
	t.Helper()
	_, err := manifest.ParseSkill([]byte(src))
	if err == nil {
		t.Fatalf("fixture SKILL.md of %s parses", id)
	}
	return "rejected: " + id + " (" + layer.InvalidArtifactCode + "): " + layer.UnparseableReason(id+"/SKILL.md", err)
}

// unparseableRegistry writes a filesystem registry holding the skill
// tools/bad, whose SKILL.md does not parse, beside the valid skills
// tools/good and tools/other.
func unparseableRegistry(t *testing.T) string {
	t.Helper()
	return writeRegistry(t, map[string]string{
		"tools/bad/SKILL.md":   nestedMetadataSkill,
		"tools/good/SKILL.md":  skillBody("good"),
		"tools/other/SKILL.md": skillBody("other"),
	})
}

// podium lint refuses an artifact whose SKILL.md does not parse and lints
// every other artifact, so a lint failure beside the refusal still reports
// its diagnostic, and a refusal alone fails the run. Under --json the refused
// artifact moves from standard error to the envelope's rejected list with
// exactly the manifest_unparseable constraint, and the diagnostics of the
// other artifacts are still present.
//
// Spec: §4.1, §7.3.1
func TestLint_UnparseableManifestRefused(t *testing.T) {
	t.Parallel()
	bad := unparseableRejected(t, "tools/bad", nestedMetadataSkill)
	t.Run("alone", func(t *testing.T) {
		t.Parallel()
		reg := writeRegistry(t, map[string]string{"tools/bad/SKILL.md": nestedMetadataSkill})
		res := runPodium(t, "", []string{"HOME=" + t.TempDir()}, "lint", "--registry", reg)
		cliWantExit(t, res, 1, "lint over an unparseable SKILL.md alone")
		assertRejectedLines(t, res.Stderr, bad)
		cliNotContains(t, res.Stdout, "lint: no issues.", "the clean-run line")
	})
	t.Run("beside a lint failure", func(t *testing.T) {
		t.Parallel()
		reg := writeRegistry(t, map[string]string{
			"tools/bad/SKILL.md":  nestedMetadataSkill,
			"tools/good/SKILL.md": skillBody("good"),
			// Invalid: missing the required version field, which the linter rejects.
			"invalid/ARTIFACT.md": "---\ntype: agent\ndescription: missing version\n---\n\nbody.\n",
		})
		res := runPodium(t, "", []string{"HOME=" + t.TempDir()}, "lint", "--registry", reg)
		cliWantExit(t, res, 1, "lint beside a lint failure")
		assertRejectedLines(t, res.Stderr, bad)
		if !lintErrorNames(res.Stdout, "invalid") {
			t.Errorf("stdout missing an error diagnostic naming invalid:\n%s", res.Stdout)
		}
	})
	t.Run("json", func(t *testing.T) {
		t.Parallel()
		reg := writeRegistry(t, map[string]string{
			"tools/bad/SKILL.md":  nestedMetadataSkill,
			"tools/good/SKILL.md": skillBody("good"),
			"invalid/ARTIFACT.md": "---\ntype: agent\ndescription: missing version\n---\n\nbody.\n",
		})
		res := runPodium(t, "", []string{"HOME=" + t.TempDir()}, "lint", "--registry", reg, "--json")
		cliWantExit(t, res, 1, "lint --json over an unparseable SKILL.md")
		cliNotContains(t, res.Stderr, "rejected:", "--json keeps refused artifacts off stderr")
		env := decodeLintEnvelope(t, res.Stdout)
		assertRejectedConstraints(t, env, map[string]string{"tools/bad": layer.ManifestUnparseableConstraint})
		if want := strings.TrimPrefix(bad, "rejected: tools/bad (ingest.invalid_artifact): "); env.Rejected[0].Reason != want {
			t.Errorf("rejected reason = %q, want %q", env.Rejected[0].Reason, want)
		}
		var linted bool
		for _, d := range env.Diagnostics {
			if d.ArtifactID == "tools/bad" {
				t.Errorf("diagnostics carry the refused tools/bad: %+v", d)
			}
			if d.ArtifactID == "invalid" && d.Severity == "error" {
				linted = true
			}
		}
		if !linted {
			t.Errorf("diagnostics = %+v, want an error for invalid, so the other artifacts were linted", env.Diagnostics)
		}
	})
}

// A filesystem-source sync drops an artifact whose SKILL.md does not parse,
// names it on standard error with ingest.invalid_artifact and the reason,
// writes the other skills for claude-code, and exits 1 as a collision drop
// does. --dry-run reports the drop and writes nothing, a --config target
// fails the same way, and podium sync override reports the drop and exits 1.
//
// Spec: §4.1, §13.11.3
func TestFilesystemSync_UnparseableManifestDropsArtifact(t *testing.T) {
	t.Parallel()
	reg := unparseableRegistry(t)
	bad := unparseableRejected(t, "tools/bad", nestedMetadataSkill)

	t.Run("one-shot", func(t *testing.T) {
		t.Parallel()
		target := t.TempDir()
		res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "claude-code")
		cliWantExit(t, res, 1, "sync over an unparseable SKILL.md")
		assertRejectedLines(t, res.Stderr, bad)
		mustExist(t, filepath.Join(target, ".claude", "skills", "good", "SKILL.md"))
		mustExist(t, filepath.Join(target, ".claude", "skills", "other", "SKILL.md"))
		mustNotExist(t, filepath.Join(target, ".claude", "skills", "bad"))
		lock := readFile(t, filepath.Join(target, ".podium", "sync.lock"))
		if strings.Contains(lock, "tools/bad") || !strings.Contains(lock, "tools/good") {
			t.Errorf("lock must record tools/good and not tools/bad:\n%s", lock)
		}
	})

	t.Run("dry-run", func(t *testing.T) {
		t.Parallel()
		target := t.TempDir()
		res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "claude-code", "--dry-run")
		cliWantExit(t, res, 1, "sync --dry-run over an unparseable SKILL.md")
		assertRejectedLines(t, res.Stderr, bad)
		mustNotExist(t, filepath.Join(target, ".claude"))
	})

	t.Run("config workspace target", func(t *testing.T) {
		t.Parallel()
		ws := t.TempDir()
		target := filepath.Join(ws, "out")
		cfg := writeCollisionWorkspaceConfig(t, ws, reg, target)
		res := runPodium(t, "", nil, "sync", "--config", cfg)
		cliWantExit(t, res, 1, "sync --config over an unparseable SKILL.md")
		assertRejectedLines(t, res.Stderr, bad)
		cliContains(t, res.Stderr, "target collide-ws:", "the target failure line")
		mustExist(t, filepath.Join(target, "tools", "good", "SKILL.md"))
		mustNotExist(t, filepath.Join(target, "tools", "bad"))
	})

	t.Run("override add", func(t *testing.T) {
		t.Parallel()
		target := t.TempDir()
		first := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none", "--include", "tools/good")
		cliWantExit(t, first, 1, "first sync over an unparseable SKILL.md")
		res := runPodium(t, "", nil, "sync", "override", "--target", target, "--add", "tools/other", "--registry", reg, "--harness", "none")
		cliWantExit(t, res, 1, "override over an unparseable SKILL.md")
		assertRejectedLines(t, res.Stderr, bad)
		mustExist(t, filepath.Join(target, "tools", "other", "SKILL.md"))
		mustNotExist(t, filepath.Join(target, "tools", "bad"))
	})
}

// A standalone server boots over a layer that holds a skill whose SKILL.md
// does not parse, refuses that skill at ingest so search and load_artifact do
// not return it, and serves the rest. POST /v1/layers/reingest answers 200
// and reports the refusal with ingest.invalid_artifact and exactly the
// manifest_unparseable constraint, and podium layer reingest exits non-zero
// and names the refused skill on standard error.
//
// Spec: §4.1, §7.3.1
func TestStandaloneServer_UnparseableManifestRefused(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"ops/base/ARTIFACT.md": smallteamLowArtifact("quarterly base runbook"),
		"ops/bad/SKILL.md":     nestedMetadataSkill,
	})
	srv := startServer(t, reg)

	st, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=quarterly+runbook")
	if st != 200 || !strings.Contains(string(body), "ops/base") {
		t.Errorf("search = HTTP %d %s, want ops/base", st, body)
	}
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=ops/base"); st != 200 {
		t.Errorf("load ops/base = HTTP %d, want 200", st)
	}
	if st, load := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=ops/bad"); st != 404 || !strings.Contains(string(load), "registry.not_found") {
		t.Errorf("load the refused ops/bad = HTTP %d %s, want 404 registry.not_found", st, load)
	}

	layerID := smallteamLayerID(t, srv.BaseURL)
	st, body = postJSON(t, srv.BaseURL+"/v1/layers/reingest?id="+layerID, nil)
	if st != 200 {
		t.Fatalf("POST /v1/layers/reingest = HTTP %d %s, want 200", st, body)
	}
	var resp struct {
		Rejected []struct {
			ArtifactID string         `json:"artifact_id"`
			Code       string         `json:"code"`
			Details    map[string]any `json:"details"`
		} `json:"rejected"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode reingest response %s: %v", body, err)
	}
	if len(resp.Rejected) != 1 || resp.Rejected[0].ArtifactID != "ops/bad" ||
		resp.Rejected[0].Code != layer.InvalidArtifactCode || len(resp.Rejected[0].Details) != 1 ||
		resp.Rejected[0].Details["constraint"] != layer.ManifestUnparseableConstraint {
		t.Errorf("rejected = %+v, want ops/bad alone with exactly the manifest_unparseable constraint", resp.Rejected)
	}

	res := runPodium(t, "", []string{"HOME=" + t.TempDir()}, "layer", "reingest", "--registry", srv.BaseURL, layerID)
	cliWantNonZero(t, res, "layer reingest over an unparseable SKILL.md")
	assertRejectedLines(t, res.Stderr, unparseableRejected(t, "ops/bad", nestedMetadataSkill))
}
