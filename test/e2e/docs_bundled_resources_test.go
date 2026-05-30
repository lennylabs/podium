package e2e

// End-to-end tests for docs/authoring/bundled-resources.md (D-bundled-resources).
// Covers the implicit bundling model (every file in the artifact directory
// ships, with no resources: list in frontmatter), the prose-reference existence
// check that fires on Markdown links in the SKILL.md body, per-file and
// per-package size caps, external_resources metadata, runtime_requirements and
// sandbox_profile handling through the MCP bridge, content-provenance rewriting,
// the SKILL.md manifest-size caps, the documented authoring patterns, atomic
// (.tmp + rename) materialization, podium import, and the /objects/ route
// contract. Tests drive the podium CLI, the standalone server, and the
// podium-mcp bridge. Behaviors blocked by a known BUILD-GAPS finding are
// recorded as skips; doc claims the implementation does not honor (with no
// finding filed) are asserted against actual behavior with a note.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// brSkillArtifact is a minimal skill ARTIFACT.md (Podium frontmatter only).
const brSkillArtifact = "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"

// brSkillArtifactPy is the skill ARTIFACT.md with a python runtime requirement.
const brSkillArtifactPy = "---\ntype: skill\nversion: 1.0.0\nruntime_requirements:\n  python: \">=3.10\"\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"

// brSkillMD returns a SKILL.md whose name matches the leaf directory, with the
// given body appended after the frontmatter.
func brSkillMD(name, desc, body string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body
}

// brVarianceDesc is the description used by the run-variance-analysis fixtures.
const brVarianceDesc = "Flag unusual variance vs. forecast after month-end close."

// brExternalArtifact is a context ARTIFACT.md declaring an external resource
// whose bytes are not bundled locally; size is parameterized.
func brExternalArtifact(size string) string {
	return "---\ntype: context\nversion: 1.0.0\ndescription: Model context.\n" +
		"external_resources:\n" +
		"  - path: ./model.onnx\n" +
		"    url: s3://company-models/variance/v1/model.onnx\n" +
		"    sha256: 9f2caabbccddeeff00112233445566778899aabbccddeeff0011223344556677\n" +
		"    size: " + size + "\n" +
		"    signature: \"sigstore:abc123\"\n" +
		"---\n\nbody\n"
}

// brNoTmp asserts no key in a materialized tree ends in a .tmp suffix.
func brNoTmp(t *testing.T, tree map[string]string) {
	t.Helper()
	for k := range tree {
		if strings.HasSuffix(k, ".tmp") {
			t.Errorf("unexpected .tmp file remained: %q", k)
		}
	}
}

// ---- Implicit bundling: directory layout materializes -----------------------

// T-D-bundled-resources-1 — a skill with scripts/, references/, and assets/
// subfolders materializes every bundled file under the none-adapter
// <artifact-id>/ layout, with no resources: list in frontmatter.
func TestBundled_NoneAdapterFullLayout(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":                      brSkillArtifactPy,
		id + "/SKILL.md":                         brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/scripts/variance.py":              "print('variance')\n",
		id + "/scripts/helpers.py":               "def helper():\n    return 1\n",
		id + "/references/variance-explained.md": "# Variance explained\n",
		id + "/assets/variance-report.md.j2":     "{{ period }}\n",
		id + "/assets/output-schema.json":        "{}\n",
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	for _, rel := range []string{
		"ARTIFACT.md", "SKILL.md",
		"scripts/variance.py", "scripts/helpers.py",
		"references/variance-explained.md",
		"assets/variance-report.md.j2", "assets/output-schema.json",
	} {
		mustExist(t, filepath.Join(tgt, id, rel))
	}
	brNoTmp(t, readTreeAll(t, tgt))
}

// T-D-bundled-resources-2 — a skill's bundled resources materialize under the
// claude-code adapter at .claude/skills/<name>/, preserving subdirectories,
// with ARTIFACT.md omitted.
func TestBundled_ClaudeCodeSkillLayout(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":               brSkillArtifact,
		id + "/SKILL.md":                  brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/scripts/variance.py":       "print('variance')\n",
		id + "/assets/output-schema.json": "{}\n",
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	base := filepath.Join(tgt, ".claude/skills/run-variance-analysis")
	mustExist(t, filepath.Join(base, "SKILL.md"))
	mustExist(t, filepath.Join(base, "scripts/variance.py"))
	mustExist(t, filepath.Join(base, "assets/output-schema.json"))
	if _, err := os.Stat(filepath.Join(base, "ARTIFACT.md")); err == nil {
		t.Errorf("ARTIFACT.md must be omitted for skills under .claude/skills/")
	}
}

// T-D-bundled-resources-3 — a context artifact ships a bundled file with no
// resources: key; lint emits no missing-resources error and sync places the
// file alongside ARTIFACT.md.
func TestBundled_NoResourcesKeyRequired(t *testing.T) {
	t.Parallel()
	id := "finance/glossary"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":  contextArtifact("Glossary of finance terms."),
		id + "/glossary.csv": "term,definition\nebitda,earnings\n",
	})
	lint := runPodium(t, "", nil, "lint", "--registry", reg)
	if lint.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", lint.Exit, lint.Stdout)
	}
	if strings.Contains(lint.Stdout, "resources") {
		t.Errorf("lint should not mention a resources key:\n%s", lint.Stdout)
	}
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, id, "glossary.csv"))
}

// ---- Prose-reference existence check (Markdown links only) ------------------

// T-D-bundled-resources-4 — a Markdown-link prose reference to an existing
// bundled file passes lint with no lint.prose_reference diagnostic.
func TestBundled_ProseReferenceResolves(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":         brSkillArtifact,
		id + "/SKILL.md":            brSkillMD("run-variance-analysis", brVarianceDesc, "Run [scripts/variance.py](scripts/variance.py) against the closed period.\n"),
		id + "/scripts/variance.py": "print('variance')\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.prose_reference") {
		t.Errorf("resolving reference should not warn:\n%s", res.Stdout)
	}
}

// T-D-bundled-resources-5 — a Markdown-link prose reference to a missing bundled
// file errors with lint.prose_reference naming the missing path.
func TestBundled_ProseReferenceMissing(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, "Run [scripts/missing.py](scripts/missing.py).\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.prose_reference") || !strings.Contains(res.Stdout, "[error]") {
		t.Errorf("missing prose_reference error:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "scripts/missing.py") {
		t.Errorf("diagnostic should name scripts/missing.py:\n%s", res.Stdout)
	}
}

// T-D-bundled-resources-5b — a prose reference to a URL that returns 404 to a
// HEAD is an ingest error (§4.4, F-4.4.2). The real `podium lint` enables the
// URL HEAD check by default; the probe targets a localhost test server.
func TestBundled_ProseURLReferenceValidated(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/missing") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, "Live [a]("+ts.URL+"/ok). Dead [b]("+ts.URL+"/missing).\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.prose_reference") || !strings.Contains(res.Stdout, "/missing") {
		t.Errorf("expected a prose_reference error naming the dead URL:\n%s", res.Stdout)
	}
}

// T-D-bundled-resources-5c — --offline skips the §4.4 URL HEAD check, so a dead
// URL no longer blocks lint (F-4.4.2 offline opt-out).
func TestBundled_ProseURLOfflineSkips(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()

	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, "Dead [b]("+ts.URL+"/missing).\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg, "--offline")
	if res.Exit != 0 {
		t.Fatalf("offline lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.prose_reference") {
		t.Errorf("offline lint should not run the URL check:\n%s", res.Stdout)
	}
}

// T-D-bundled-resources-6 — a Markdown-link prose reference that escapes the
// artifact package (../) errors with lint.prose_reference.
func TestBundled_ProseReferenceEscapes(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, "See [creds](../../etc/passwd).\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.prose_reference") || !strings.Contains(res.Stdout, "escapes the artifact package") {
		t.Errorf("missing escape diagnostic:\n%s", res.Stdout)
	}
}

// T-D-bundled-resources-7 — a bundled resource below the 256 KB inline cutoff is
// returned inline in the load_artifact response. spec: §7.2.
func TestBundled_InlineResourceBelowCutoff(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	script := "print('variance')\n"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":         brSkillArtifact,
		id + "/SKILL.md":            brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/scripts/variance.py": script,
	}))
	var resp struct {
		Resources      map[string]string `json:"resources"`
		LargeResources map[string]any    `json:"large_resources"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+id, &resp)
	if resp.Resources["scripts/variance.py"] != script {
		t.Errorf("inline resource = %q, want %q (resources=%v)",
			resp.Resources["scripts/variance.py"], script, resp.Resources)
	}
	if len(resp.LargeResources) != 0 {
		t.Errorf("a sub-cutoff resource must not appear in large_resources: %v", resp.LargeResources)
	}
}

// T-D-bundled-resources-8 — a bundled resource above the 256 KB inline cutoff is
// returned as a presigned URL in the load_artifact response, and the URL serves
// the bytes from the data plane. spec: §7.2.
func TestBundled_LargeResourceAboveCutoff(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	large := strings.Repeat("A", 256*1024+1024) // above the 256 KB inline cutoff
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":  brSkillArtifact,
		id + "/SKILL.md":     brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/data/big.bin": large,
	}))
	var resp struct {
		Resources      map[string]string `json:"resources"`
		LargeResources map[string]struct {
			PresignedURL string `json:"presigned_url"`
			ContentHash  string `json:"content_hash"`
			Size         int64  `json:"size"`
		} `json:"large_resources"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+id, &resp)
	if _, inline := resp.Resources["data/big.bin"]; inline {
		t.Error("a resource above the cutoff must not be returned inline")
	}
	link, ok := resp.LargeResources["data/big.bin"]
	if !ok {
		t.Fatalf("large resource missing from large_resources: %v", resp.LargeResources)
	}
	if link.PresignedURL == "" {
		t.Error("presigned_url is empty")
	}
	if link.Size != int64(len(large)) {
		t.Errorf("size = %d, want %d", link.Size, len(large))
	}
	// The presigned URL resolves to the data-plane route and returns the bytes.
	st, body := getRaw(t, link.PresignedURL)
	if st != 200 {
		t.Fatalf("presigned fetch = HTTP %d", st)
	}
	if string(body) != large {
		t.Errorf("fetched %d bytes, want %d", len(body), len(large))
	}
}

// ---- Size caps --------------------------------------------------------------

// T-D-bundled-resources-9 — a bundled file over the 1 MB per-file soft cap
// warns at ingest (exit 0).
func TestBundled_PerFileSoftCapWarns(t *testing.T) {
	t.Parallel()
	id := "finance/glossary"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": contextArtifact("Big-data context."),
		id + "/data.bin":    strings.Repeat("a", 1024*1024+1),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout(head)=%.200s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.bundled_resource_size") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing per-file soft-cap warning:\n%.400s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "per-file") {
		t.Errorf("diagnostic should mention per-file:\n%.400s", res.Stdout)
	}
}

// T-D-bundled-resources-10 — bundled files totaling over the 10 MB per-package
// cap error at ingest (exit 1).
func TestBundled_PerPackageCapErrors(t *testing.T) {
	t.Parallel()
	id := "finance/glossary"
	chunk := strings.Repeat("b", 6*1024*1024)
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": contextArtifact("Oversized context."),
		id + "/a.bin":       chunk,
		id + "/b.bin":       chunk,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout(head)=%.200s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.bundled_resource_size") || !strings.Contains(res.Stdout, "per-package") {
		t.Errorf("missing per-package error:\n%.400s", res.Stdout)
	}
}

// ---- External resources -----------------------------------------------------

// T-D-bundled-resources-11 — an external_resources block parses without error
// when the referenced file is absent from disk.
func TestBundled_ExternalResourcesParse(t *testing.T) {
	t.Parallel()
	id := "data/model-ctx"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brExternalArtifact("145000000"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("external_resources block should not produce an error:\n%s", res.Stdout)
	}
}

// T-D-bundled-resources-12 — declared external resource size does not trigger a
// bundled-resource size cap.
func TestBundled_ExternalResourcesSizeIgnored(t *testing.T) {
	t.Parallel()
	id := "data/model-ctx"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brExternalArtifact("145000000"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.bundled_resource_size") {
		t.Errorf("external resource size should not hit a bundled-size cap:\n%s", res.Stdout)
	}
}

// T-D-bundled-resources-13 — external resource bytes need not be present
// locally; the linter does not treat external_resources.path as a prose
// reference or a missing bundled file.
func TestBundled_ExternalResourcesBytesAbsent(t *testing.T) {
	t.Parallel()
	id := "data/model-ctx"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brExternalArtifact("145000000"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.prose_reference") {
		t.Errorf("external_resources.path must not be a prose reference:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.bundled_resource_size") && strings.Contains(res.Stdout, "model.onnx") {
		t.Errorf("absent external bytes must not hit a size cap:\n%s", res.Stdout)
	}
}

// ---- runtime_requirements ---------------------------------------------------

// T-D-bundled-resources-14 — runtime_requirements with python, node, and
// system_packages parses cleanly for a skill.
func TestBundled_RuntimeRequirementsParse(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	art := "---\ntype: skill\nversion: 1.0.0\nruntime_requirements:\n  python: \">=3.10\"\n  node: \">=20\"\n  system_packages: [\"jq\", \"curl\"]\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": art,
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("runtime_requirements should parse without error:\n%s", res.Stdout)
	}
}

// T-D-bundled-resources-15 — a host that advertises no capabilities does not
// gate on runtime_requirements: it surfaces the requirement to the caller and
// materializes (§4.4.1 "Adapters surface these requirements to the host where
// supported"). The runtime gate (F-4.4.1) activates only once the host
// advertises a capability; see tests 16 and 17 for the refusal path.
func TestBundled_RuntimeRequirementsSurfacedNotGated(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifactPy,
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	if e, ok := result["error"]; ok && e != nil {
		t.Fatalf("unconfigured host should not gate; load should not refuse: %v", e)
	}
	if paths, _ := result["materialized_at"].([]any); len(paths) == 0 {
		t.Errorf("expected materialized_at paths: %v", result)
	}
}

// T-D-bundled-resources-15b — once the host advertises a satisfying capability,
// the artifact materializes (§4.4.1). The positive complement to tests 16/17.
func TestBundled_RuntimeSatisfiedMaterializes(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifactPy, // requires python >=3.10
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL),
		"PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT="+mat, "PODIUM_HOST_PYTHON=3.11.4"),
		toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	if e, ok := result["error"]; ok && e != nil {
		t.Fatalf("host python 3.11.4 satisfies >=3.10; load should not refuse: %v", e)
	}
	if paths, _ := result["materialized_at"].([]any); len(paths) == 0 {
		t.Errorf("expected materialized_at paths: %v", result)
	}
}

// T-D-bundled-resources-16 — a host that does not satisfy a python requirement
// refuses with materialize.runtime_unavailable (§4.4.1, F-4.4.1).
func TestBundled_RuntimeUnavailablePython(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifactPy, // requires python >=3.10
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL),
		"PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT="+mat, "PODIUM_HOST_PYTHON=3.9.0"),
		toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	errStr, _ := result["error"].(string)
	if !strings.Contains(errStr, "materialize.runtime_unavailable") || !strings.Contains(errStr, "python") {
		t.Errorf("expected runtime_unavailable naming python, got result=%v", result)
	}
	// A refused artifact must not have been written to disk.
	if paths, _ := result["materialized_at"].([]any); len(paths) != 0 {
		t.Errorf("refused artifact should not materialize: %v", paths)
	}
}

// T-D-bundled-resources-17 — a host missing a required system package refuses
// with materialize.runtime_unavailable naming the package (§4.4.1, F-4.4.1).
func TestBundled_RuntimeUnavailableSystemPackage(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	art := "---\ntype: skill\nversion: 1.0.0\nruntime_requirements:\n  system_packages: [\"jq\", \"curl\"]\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": art,
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
	}))
	mat := t.TempDir()
	// Host advertises jq but not curl.
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL),
		"PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT="+mat, "PODIUM_HOST_PACKAGES=jq"),
		toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	errStr, _ := result["error"].(string)
	if !strings.Contains(errStr, "materialize.runtime_unavailable") || !strings.Contains(errStr, "curl") {
		t.Errorf("expected runtime_unavailable naming curl, got result=%v", result)
	}
}

// ---- sandbox_profile --------------------------------------------------------

// T-D-bundled-resources-18 — a sandbox_profile: unrestricted artifact
// materializes through MCP with no refusal.
func TestBundled_SandboxUnrestrictedMaterializes(t *testing.T) {
	t.Parallel()
	id := "finance/unrestricted-ctx"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Unrestricted context.\nsandbox_profile: unrestricted\n---\n\nbody\n",
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	if e, ok := result["error"]; ok && e != nil {
		t.Fatalf("unrestricted profile should not refuse: %v", e)
	}
	if paths, _ := result["materialized_at"].([]any); len(paths) == 0 {
		t.Errorf("expected materialized_at paths: %v", result)
	}
}

// T-D-bundled-resources-19 — a non-unrestricted sandbox_profile is refused on a
// host without that capability. The refusal is returned in the result.error
// string (not as a JSON-RPC envelope error).
func TestBundled_SandboxRefusedWithoutCapability(t *testing.T) {
	t.Parallel()
	id := "finance/restricted-ctx"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Restricted context.\nsandbox_profile: read-only-fs\n---\n\nbody\n",
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	errStr, _ := result["error"].(string)
	if !strings.Contains(errStr, "materialize.sandbox_unsupported") || !strings.Contains(errStr, "read-only-fs") {
		t.Errorf("expected sandbox refusal naming read-only-fs, got result=%v", result)
	}
}

// T-D-bundled-resources-20 — PODIUM_IGNORE_SANDBOX=true overrides the sandbox
// refusal and materializes, emitting a loud warning to stderr.
func TestBundled_SandboxIgnoreOverride(t *testing.T) {
	t.Parallel()
	id := "finance/restricted-ctx"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Restricted context.\nsandbox_profile: read-only-fs\n---\n\nbody\n",
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT="+mat, "PODIUM_IGNORE_SANDBOX=true"),
		toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	if e, ok := result["error"]; ok && e != nil {
		t.Fatalf("PODIUM_IGNORE_SANDBOX should bypass refusal: %v", e)
	}
	if !strings.Contains(res.Stderr, "WARN") || !strings.Contains(res.Stderr, "sandbox") {
		t.Errorf("expected a loud sandbox warning on stderr:\n%s", res.Stderr)
	}
}

// T-D-bundled-resources-21 — an artifact with no sandbox_profile is treated as
// unrestricted and materializes without refusal.
func TestBundled_SandboxAbsentDefaultsUnrestricted(t *testing.T) {
	t.Parallel()
	id := "finance/plain-ctx"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": contextArtifact("Plain context with no sandbox profile."),
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	if e, ok := result["error"]; ok && e != nil {
		t.Fatalf("absent sandbox_profile should not refuse: %v", e)
	}
}

// T-D-bundled-resources-21b — when a host honors sandbox_profile: seccomp-strict,
// the materialization layer delivers the baseline syscall-allowlist profile
// Podium ships (§4.4.1, F-4.4.5) under .podium/seccomp-strict.json so the host
// can apply it.
func TestBundled_SeccompBaselineDelivered(t *testing.T) {
	t.Parallel()
	id := "finance/strict-ctx"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Strict context.\nsandbox_profile: seccomp-strict\n---\n\nbody\n",
	}))
	mat := t.TempDir()
	// Host declares it honors seccomp-strict so the sandbox gate passes.
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL),
		"PODIUM_HARNESS=none", "PODIUM_MATERIALIZE_ROOT="+mat, "PODIUM_HOST_SANDBOXES=unrestricted,seccomp-strict"),
		toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	if e, ok := result["error"]; ok && e != nil {
		t.Fatalf("host honoring seccomp-strict should not refuse: %v", e)
	}
	profile := filepath.Join(mat, ".podium", "seccomp-strict.json")
	mustExist(t, profile)
	got := readFile(t, profile)
	if !strings.Contains(got, "SCMP_ACT_ERRNO") || !strings.Contains(got, "SCMP_ACT_ALLOW") {
		t.Errorf("delivered seccomp profile is not a strict allowlist:\n%s", got)
	}
}

// ---- Content provenance -----------------------------------------------------

// brProvenanceBody is a SKILL.md body carrying one imported provenance block.
const brProvenanceBody = "Authored prose.\n\n<!-- begin imported source=\"https://wiki.example.com/policy/payments\" -->\nImported policy text.\n<!-- end imported -->\n"

// T-D-bundled-resources-22 — the none adapter passes imported provenance markers
// through verbatim.
func TestBundled_ProvenanceNonePassthrough(t *testing.T) {
	t.Parallel()
	id := "finance/policy/payments"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
		id + "/SKILL.md":    brSkillMD("payments", "Payments policy skill.", brProvenanceBody),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, id, "SKILL.md"))
	if !strings.Contains(got, "begin imported") || !strings.Contains(got, "end imported") {
		t.Errorf("none adapter should preserve provenance markers verbatim:\n%s", got)
	}
}

// T-D-bundled-resources-23 — the claude-code adapter rewrites an imported
// provenance block into an <untrusted-data source="..."> region and drops the
// begin/end markers.
func TestBundled_ProvenanceClaudeCodeRewrite(t *testing.T) {
	t.Parallel()
	id := "finance/policy/payments"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
		id + "/SKILL.md":    brSkillMD("payments", "Payments policy skill.", brProvenanceBody),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/skills/payments/SKILL.md"))
	if !strings.Contains(got, "<untrusted-data source=\"https://wiki.example.com/policy/payments\">") {
		t.Errorf("missing untrusted-data region:\n%s", got)
	}
	if strings.Contains(got, "begin imported") {
		t.Errorf("begin-imported marker should be rewritten:\n%s", got)
	}
}

// T-D-bundled-resources-24 — a plain SKILL.md with no provenance markers passes
// through the claude-code adapter without gaining untrusted-data tags.
func TestBundled_ProvenanceAuthoredOnlyPassthrough(t *testing.T) {
	t.Parallel()
	id := "finance/policy/payments"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
		id + "/SKILL.md":    brSkillMD("payments", "Payments policy skill.", "Plain authored prose with no imported blocks.\n"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/skills/payments/SKILL.md"))
	if strings.Contains(got, "untrusted-data") {
		t.Errorf("unmarked body should not gain untrusted-data tags:\n%s", got)
	}
}

// T-D-bundled-resources-24b — the claude-code adapter rewrites imported
// provenance in a non-skill (context) body too, not just skills (§4.4.2,
// F-4.4.3). The materialized ARTIFACT.md under .claude/podium/<id>/ carries
// the <untrusted-data> region.
func TestBundled_ProvenanceClaudeCodeNonSkill(t *testing.T) {
	t.Parallel()
	id := "finance/policy/payments-context"
	art := "---\ntype: context\nversion: 1.0.0\ndescription: Aggregated payments policy.\n---\n\nAuthored intro.\n\n" + brProvenanceBody
	reg := writeRegistry(t, map[string]string{id + "/ARTIFACT.md": art})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/podium", id, "ARTIFACT.md"))
	if !strings.Contains(got, "<untrusted-data source=\"https://wiki.example.com/policy/payments\">") {
		t.Errorf("non-skill body missing untrusted-data region:\n%s", got)
	}
	if strings.Contains(got, "begin imported") {
		t.Errorf("begin-imported marker should be rewritten in a non-skill body:\n%s", got)
	}
}

// T-D-bundled-resources-25 — two imported provenance blocks with distinct
// sources are each rewritten to a separate <untrusted-data> region.
func TestBundled_ProvenanceMultipleBlocks(t *testing.T) {
	t.Parallel()
	id := "finance/policy/payments"
	body := "Intro.\n\n" +
		"<!-- begin imported source=\"https://wiki.example.com/policy/a\" -->\nBlock A.\n<!-- end imported -->\n\n" +
		"Middle prose.\n\n" +
		"<!-- begin imported source=\"https://wiki.example.com/policy/b\" -->\nBlock B.\n<!-- end imported -->\n"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
		id + "/SKILL.md":    brSkillMD("payments", "Payments policy skill.", body),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/skills/payments/SKILL.md"))
	if strings.Count(got, "<untrusted-data") < 2 {
		t.Errorf("expected two untrusted-data regions:\n%s", got)
	}
	if !strings.Contains(got, "https://wiki.example.com/policy/a") || !strings.Contains(got, "https://wiki.example.com/policy/b") {
		t.Errorf("both sources should be present:\n%s", got)
	}
}

// ---- Manifest-size lint -----------------------------------------------------

// T-D-bundled-resources-26 — a SKILL.md body over the ~5K-token soft cap warns
// (exit 0).
func TestBundled_SkillBodyOverTokenSoftCapWarns(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	body := strings.Repeat("word ", 5200)
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, body),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout(head)=%.200s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.manifest_size") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing manifest_size token warning:\n%.400s", res.Stdout)
	}
}

// T-D-bundled-resources-27 — a SKILL.md body over the 500-line soft cap warns
// (exit 0).
func TestBundled_SkillBodyOverLineSoftCapWarns(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	body := strings.Repeat("line\n", 501)
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, body),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout(head)=%.200s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.manifest_size") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing manifest_size line warning:\n%.400s", res.Stdout)
	}
}

// T-D-bundled-resources-28 — a SKILL.md body over the ~20K-token hard cap errors
// (exit 1).
func TestBundled_ManifestOverHardCapErrors(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	body := strings.Repeat("word ", 21000)
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, body),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout(head)=%.200s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.manifest_size") {
		t.Errorf("missing manifest_size error:\n%.400s", res.Stdout)
	}
}

// ---- Documented patterns ----------------------------------------------------

// T-D-bundled-resources-29 — the "skill with a script" pattern (HTML-comment
// ARTIFACT.md body, SKILL.md with name/description/license, scripts/) lints
// clean and materializes its files.
func TestBundled_PatternSkillWithScript(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	skill := "---\nname: run-variance-analysis\ndescription: " + brVarianceDesc + "\nlicense: MIT\n---\n\nRun the analysis.\n"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":         "---\ntype: skill\nversion: 1.0.0\nruntime_requirements:\n  python: \">=3.10\"\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		id + "/SKILL.md":            skill,
		id + "/scripts/variance.py": "print('variance')\n",
	})
	if lint := runPodium(t, "", nil, "lint", "--registry", reg); lint.Exit != 0 {
		t.Fatalf("lint exit=%d\nstdout=%s", lint.Exit, lint.Stdout)
	}
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	for _, rel := range []string{"ARTIFACT.md", "SKILL.md", "scripts/variance.py"} {
		mustExist(t, filepath.Join(tgt, id, rel))
	}
}

// T-D-bundled-resources-30 — the "skill with a template" pattern materializes
// the assets/ template and the prose reference resolves.
func TestBundled_PatternSkillWithTemplate(t *testing.T) {
	t.Parallel()
	id := "finance/reports/monthly-summary"
	body := "Format the report using [assets/summary.md.j2](assets/summary.md.j2). Pass the metrics dict as `m` and the period string as `period`.\n"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":          brSkillArtifact,
		id + "/SKILL.md":             brSkillMD("monthly-summary", "Format a monthly summary report.", body),
		id + "/assets/summary.md.j2": "# {{ period }}\n{{ m }}\n",
	})
	if lint := runPodium(t, "", nil, "lint", "--registry", reg); lint.Exit != 0 {
		t.Fatalf("lint exit=%d\nstdout=%s", lint.Exit, lint.Stdout)
	}
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, id, "assets/summary.md.j2"))
}

// T-D-bundled-resources-31 — the "skill with a JSON schema" pattern materializes
// the assets/ schema and the prose reference resolves.
func TestBundled_PatternSkillWithJSONSchema(t *testing.T) {
	t.Parallel()
	id := "finance/procurement/vendor-form"
	body := "Validate the vendor record against [assets/vendor.json](assets/vendor.json) before submitting. The schema defines required fields and value ranges.\n"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":        brSkillArtifact,
		id + "/SKILL.md":           brSkillMD("vendor-form", "Validate a vendor record.", body),
		id + "/assets/vendor.json": "{}",
	})
	if lint := runPodium(t, "", nil, "lint", "--registry", reg); lint.Exit != 0 {
		t.Fatalf("lint exit=%d\nstdout=%s", lint.Exit, lint.Stdout)
	}
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, id, "assets/vendor.json"))
}

// brHookArtifact is the "hook with a bundled action script" pattern ARTIFACT.md.
const brHookArtifact = "---\ntype: hook\nversion: 1.0.0\nhook_event: stop\nhook_action: |\n  scripts/log.sh\nruntime_requirements:\n  system_packages: [jq]\n---\n\nHook body. Document the side effect the hook_action performs.\n"

// T-D-bundled-resources-32 — the "hook with a bundled action script" pattern
// lints clean and materializes ARTIFACT.md plus the action script via none.
func TestBundled_PatternHookNoneAdapter(t *testing.T) {
	t.Parallel()
	id := "finance/audit/log-session-end"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":    brHookArtifact,
		id + "/scripts/log.sh": "#!/bin/sh\necho done\n",
	})
	if lint := runPodium(t, "", nil, "lint", "--registry", reg); lint.Exit != 0 {
		t.Fatalf("lint exit=%d\nstdout=%s", lint.Exit, lint.Stdout)
	}
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, id, "ARTIFACT.md"))
	mustExist(t, filepath.Join(tgt, id, "scripts/log.sh"))
}

// T-D-bundled-resources-33 — the hook pattern materializes under claude-code at
// .claude/podium/<id>/ with its bundled script.
func TestBundled_PatternHookClaudeCode(t *testing.T) {
	t.Parallel()
	id := "finance/audit/log-session-end"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":    brHookArtifact,
		id + "/scripts/log.sh": "#!/bin/sh\necho done\n",
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/podium", id, "ARTIFACT.md"))
	mustExist(t, filepath.Join(tgt, ".claude/podium", id, "scripts/log.sh"))
}

// T-D-bundled-resources-34 — a skill missing SKILL.md is rejected before the
// lint rules run (registry walk), both lint and sync exit non-zero, and nothing
// is materialized.
func TestBundled_SkillMissingSkillMDRejected(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
	})
	lint := runPodium(t, "", nil, "lint", "--registry", reg)
	if lint.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s stderr=%s", lint.Exit, lint.Stdout, lint.Stderr)
	}
	if !strings.Contains(lint.Stdout+lint.Stderr, "missing SKILL.md") {
		t.Errorf("missing 'missing SKILL.md' diagnostic:\nstdout=%s\nstderr=%s", lint.Stdout, lint.Stderr)
	}
	tgt := t.TempDir()
	sync := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if sync.Exit == 0 {
		t.Fatalf("sync exit=0, want non-zero\nstdout=%s stderr=%s", sync.Stdout, sync.Stderr)
	}
	if _, err := os.Stat(filepath.Join(tgt, id, "ARTIFACT.md")); err == nil {
		t.Errorf("artifact must not be materialized when SKILL.md is missing")
	}
}

// T-D-bundled-resources-35 — a skill ARTIFACT.md body with non-comment prose
// warns with lint.skill_artifact_body (exit 0).
func TestBundled_SkillArtifactBodyProseWarns(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\nThis is unauthorized body prose.\n",
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.skill_artifact_body") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing skill_artifact_body warning:\n%s", res.Stdout)
	}
}

// T-D-bundled-resources-36 — a skill ARTIFACT.md body that is a single HTML
// comment passes lint with no skill_artifact_body warning.
func TestBundled_SkillArtifactBodyComment(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": brSkillArtifact,
		id + "/SKILL.md":    brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || strings.Contains(res.Stdout, "lint.skill_artifact_body") {
		t.Errorf("comment body should not warn: exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-bundled-resources-37 — a successful sync leaves no .tmp files; all final
// bundled resource files are present (atomic .tmp + rename materialization).
func TestBundled_AtomicNoTmpFiles(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":               brSkillArtifact,
		id + "/SKILL.md":                  brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/scripts/variance.py":       "print('variance')\n",
		id + "/assets/output-schema.json": "{}\n",
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	brNoTmp(t, readTreeAll(t, tgt))
	for _, rel := range []string{"ARTIFACT.md", "SKILL.md", "scripts/variance.py", "assets/output-schema.json"} {
		mustExist(t, filepath.Join(tgt, id, rel))
	}
}

// T-D-bundled-resources-38 — MCP load_artifact via the none adapter materializes
// the skill's bundled resources. spec: §7.2.
func TestBundled_MCPMaterializeNone(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":         brSkillArtifact,
		id + "/SKILL.md":            brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/scripts/variance.py": "print('variance')\n",
	}))
	mat := t.TempDir()
	res := mcpExec(t, brMatEnv(t, srv.BaseURL, mat),
		toolCall(1, "load_artifact", map[string]any{"id": id}))
	_ = rpcResult(t, res.Stdout, 1)
	mustExist(t, filepath.Join(mat, id, "SKILL.md"))
	mustExist(t, filepath.Join(mat, id, "scripts/variance.py"))
}

// T-D-bundled-resources-39 — MCP load_artifact via the claude-code adapter
// materializes the skill's bundled resources. spec: §7.2.
func TestBundled_MCPMaterializeClaudeCode(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":         brSkillArtifact,
		id + "/SKILL.md":            brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/scripts/variance.py": "print('variance')\n",
	}))
	mat := t.TempDir()
	res := mcpExec(t, brMatEnv(t, srv.BaseURL, mat, "PODIUM_HARNESS=claude-code"),
		toolCall(1, "load_artifact", map[string]any{"id": id, "harness": "claude-code"}))
	_ = rpcResult(t, res.Stdout, 1)
	base := filepath.Join(mat, ".claude/skills/run-variance-analysis")
	mustExist(t, filepath.Join(base, "SKILL.md"))
	mustExist(t, filepath.Join(base, "scripts/variance.py"))
}

// T-D-bundled-resources-40 — a references/ file is bundled and materialized, and
// its prose reference resolves.
func TestBundled_ReferencesSubdir(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	body := "Background is in [references/variance-explained.md](references/variance-explained.md).\n"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":                      brSkillArtifact,
		id + "/SKILL.md":                         brSkillMD("run-variance-analysis", brVarianceDesc, body),
		id + "/references/variance-explained.md": "# Variance explained\n",
	})
	if lint := runPodium(t, "", nil, "lint", "--registry", reg); lint.Exit != 0 {
		t.Fatalf("lint exit=%d\nstdout=%s", lint.Exit, lint.Stdout)
	}
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, id, "references/variance-explained.md"))
}

// T-D-bundled-resources-41 — a custom (non-standard) subdirectory is bundled and
// materialized with no lint error.
func TestBundled_CustomSubdir(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":          brSkillArtifact,
		id + "/SKILL.md":             brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/config/settings.yaml": "key: value\n",
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, id, "config/settings.yaml"))
	if lint := runPodium(t, "", nil, "lint", "--registry", reg); strings.Contains(lint.Stdout, "[error]") {
		t.Errorf("non-standard subdir should not produce a lint error:\n%s", lint.Stdout)
	}
}

// T-D-bundled-resources-42 — an SBOM bundled as an ordinary resource with an
// informational sbom: frontmatter field lints clean and materializes.
func TestBundled_SbomBundledResource(t *testing.T) {
	t.Parallel()
	id := "finance/bom-ctx"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Context with SBOM.\nsbom:\n  format: cyclonedx\n  ref: bom.json\n---\n\nbody\n",
		id + "/bom.json":    "{\"bomFormat\":\"CycloneDX\"}\n",
	})
	if lint := runPodium(t, "", nil, "lint", "--registry", reg); lint.Exit != 0 {
		t.Fatalf("lint exit=%d\nstdout=%s", lint.Exit, lint.Stdout)
	}
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, id, "bom.json"))
}

// T-D-bundled-resources-43 — an sbom: frontmatter ref without a bundled file
// does not cause a lint error (the YAML value is not a prose reference).
func TestBundled_SbomRefNoFile(t *testing.T) {
	t.Parallel()
	id := "finance/bom-ctx"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Context with SBOM.\nsbom:\n  format: cyclonedx\n  ref: bom.json\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.prose_reference") {
		t.Errorf("sbom ref is not a prose reference; no diagnostic expected:\n%s", res.Stdout)
	}
}

// T-D-bundled-resources-44 — a high-sensitivity skill with a bundled script is
// accepted by default (non-public-mode) ingest; sensitivity does not block.
func TestBundled_HighSensitivityAccepted(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":    "---\ntype: skill\nversion: 1.0.0\nsensitivity: high\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		id + "/SKILL.md":       brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/scripts/run.py": "print('run')\n",
	})
	if lint := runPodium(t, "", nil, "lint", "--registry", reg); lint.Exit != 0 {
		t.Fatalf("lint exit=%d\nstdout=%s", lint.Exit, lint.Stdout)
	}
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, id, "scripts/run.py"))
}

// T-D-bundled-resources-45 — public-mode ingest rejects a high-sensitivity
// artifact.
func TestBundled_PublicModeRejectsSensitive(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-13.2.2: the public-mode sensitivity ceiling is not wired into the running ingest path, so a high-sensitivity artifact is not rejected with ingest.public_mode_rejects_sensitive")
}

// ---- podium import ----------------------------------------------------------

// T-D-bundled-resources-46 — podium import wraps a SKILL.md tree into a Podium
// layer: it writes ARTIFACT.md (type + version) and copies SKILL.md plus
// bundled scripts.
func TestBundled_ImportCreatesLayer(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	sd := filepath.Join(src, "run-variance-analysis")
	if err := os.MkdirAll(filepath.Join(sd, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"),
		[]byte("---\nname: run-variance-analysis\ndescription: Variance.\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "scripts", "variance.py"), []byte("print('variance')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "import", "--source", src, "--target", tgt, "--type", "skill", "--version", "1.0.0")
	if res.Exit != 0 {
		t.Fatalf("import exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	art := readFile(t, filepath.Join(tgt, "run-variance-analysis", "ARTIFACT.md"))
	if !strings.Contains(art, "type: skill") || !strings.Contains(art, "version: 1.0.0") {
		t.Errorf("ARTIFACT.md missing type/version:\n%s", art)
	}
	mustExist(t, filepath.Join(tgt, "run-variance-analysis", "SKILL.md"))
	mustExist(t, filepath.Join(tgt, "run-variance-analysis", "scripts", "variance.py"))
}

// T-D-bundled-resources-47 — podium import --dry-run reports the plan ("would
// write") and writes no files.
func TestBundled_ImportDryRun(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	sd := filepath.Join(src, "run-variance-analysis")
	if err := os.MkdirAll(filepath.Join(sd, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"),
		[]byte("---\nname: run-variance-analysis\ndescription: Variance.\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "scripts", "variance.py"), []byte("print('variance')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "import", "--source", src, "--target", tgt, "--type", "skill", "--version", "1.0.0", "--dry-run")
	if res.Exit != 0 {
		t.Fatalf("import --dry-run exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "would write") {
		t.Errorf("dry-run stdout missing 'would write':\n%s", res.Stdout)
	}
	if tree := readTreeAll(t, tgt); len(tree) != 0 {
		t.Errorf("dry-run wrote files: %v", tree)
	}
}

// ---- /objects/ route --------------------------------------------------------

// T-D-bundled-resources-48 — the objects route returns 404 for an unknown
// content-hash key, leaking no stored bytes.
func TestBundled_ObjectsUnknownHash(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/real/ARTIFACT.md": contextArtifact("Real context."),
	}))
	if st := getStatus(t, srv.BaseURL+"/objects/"+strings.Repeat("0", 64)); st != 404 {
		t.Errorf("unknown hash = HTTP %d, want 404", st)
	}
}

// T-D-bundled-resources-49 — the objects route rejects a path-traversal key with
// 400.
func TestBundled_ObjectsPathTraversal(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/real/ARTIFACT.md": contextArtifact("Real context."),
	}))
	if st := getStatus(t, srv.BaseURL+"/objects/..%2Fescape"); st != 400 {
		t.Errorf("traversal key = HTTP %d, want 400", st)
	}
}

// T-D-bundled-resources-50 — an unknown object key returns 404 whether or not
// the object store route is registered.
func TestBundled_ObjectsUnknownReturns404(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/real/ARTIFACT.md": contextArtifact("Real context."),
	}))
	if st := getStatus(t, srv.BaseURL+"/objects/anything"); st != 404 {
		t.Errorf("unknown object = HTTP %d, want 404", st)
	}
}

// T-D-bundled-resources-51 — content-addressed deduplication stores identical
// files once. spec: §4.4, §7.2.
func TestBundled_ContentDedup(t *testing.T) {
	t.Parallel()
	shared := strings.Repeat("D", 256*1024+777) // large → routed to the object store
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/a/ARTIFACT.md":  contextArtifact("A"),
		"finance/a/data/big.bin": shared,
		"finance/b/ARTIFACT.md":  contextArtifact("B"),
		"finance/b/data/big.bin": shared,
	}))
	// Both artifacts reference the same content-addressed key.
	var ra, rb struct {
		LargeResources map[string]struct {
			ContentHash string `json:"content_hash"`
		} `json:"large_resources"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/a", &ra)
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/b", &rb)
	ha := ra.LargeResources["data/big.bin"].ContentHash
	hb := rb.LargeResources["data/big.bin"].ContentHash
	if ha == "" || ha != hb {
		t.Fatalf("identical bytes must share a content hash: a=%q b=%q", ha, hb)
	}
	// The object store keeps exactly one copy keyed by that hash.
	objRoot := filepath.Join(srv.Home, ".podium", "standalone", "objects")
	key := strings.TrimPrefix(ha, "sha256:")
	entries, err := os.ReadDir(objRoot)
	if err != nil {
		t.Fatalf("read object store dir: %v", err)
	}
	count := 0
	for _, e := range entries {
		if e.Name() == key {
			count++
		}
	}
	if count != 1 {
		t.Errorf("dedup: object %s stored %d times, want 1", key, count)
	}
}

// T-D-bundled-resources-52 — sync with an unknown harness fails with
// config.unknown_harness.
func TestBundled_SyncUnknownHarness(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/glossary/ARTIFACT.md": contextArtifact("Glossary context."),
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "not-a-real-harness")
	if res.Exit == 0 {
		t.Fatalf("sync exit=0, want non-zero\nstdout=%s stderr=%s", res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "config.unknown_harness") {
		t.Errorf("stderr missing config.unknown_harness:\n%s", res.Stderr)
	}
}
