package main

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/hook"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// ----- test hooks (in-process; the SPI owns the wire-serializable loading) --

// tagHook appends "|<tag>" to each file and records run order.
type tagHook struct {
	tag   string
	order *[]string
}

func (h tagHook) ID() string { return "tag-" + h.tag }
func (h tagHook) Apply(_ context.Context, _ map[string]any, f hook.File) (hook.Result, error) {
	*h.order = append(*h.order, h.tag)
	f.Content = append(append([]byte{}, f.Content...), []byte("|"+h.tag)...)
	return hook.Result{File: f}, nil
}

// dropHook drops files whose path contains match.
type dropHook struct{ match string }

func (dropHook) ID() string { return "drop" }
func (h dropHook) Apply(_ context.Context, _ map[string]any, f hook.File) (hook.Result, error) {
	if strings.Contains(f.Path, h.match) {
		f.Drop = true
	}
	return hook.Result{File: f}, nil
}

// errHook always aborts the chain.
type errHook struct{}

func (errHook) ID() string { return "err" }
func (errHook) Apply(_ context.Context, _ map[string]any, _ hook.File) (hook.Result, error) {
	return hook.Result{}, errors.New("boom")
}

// warnHook emits a warning and passes the file through unchanged.
type warnHook struct{ msg string }

func (warnHook) ID() string { return "warn" }
func (h warnHook) Apply(_ context.Context, _ map[string]any, f hook.File) (hook.Result, error) {
	return hook.Result{File: f, Warnings: []string{h.msg}}, nil
}

// ctxHook captures the manifest context handed to the chain.
type ctxHook struct{ seen *map[string]any }

func (ctxHook) ID() string { return "ctx" }
func (h ctxHook) Apply(_ context.Context, m map[string]any, f hook.File) (hook.Result, error) {
	*h.seen = m
	return hook.Result{File: f}, nil
}

// fixtureResp builds a response whose content_hash matches its frontmatter so
// the §6.6 step 2 check passes by default.
func fixtureResp(id, frontmatter string) loadArtifactResponse {
	return loadArtifactResponse{
		ID:          id,
		Type:        "context",
		Version:     "1.0.0",
		Frontmatter: frontmatter,
		ContentHash: "sha256:" + version.ContentHash([]byte(frontmatter)),
	}
}

// Spec: §6.6 step 4 — the MaterializationHook chain runs in declared order
// over the adapter output; each hook receives the previous hook's output.
func TestDeliver_HookChainRunsInOrder(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	var order []string
	s.hooks = []hook.Hook{
		tagHook{tag: "A", order: &order},
		tagHook{tag: "B", order: &order},
	}
	fm := "---\ntype: context\n---\nbody"
	out := s.deliverLoadArtifact(fixtureResp("team/x", fm), deliverOpts{harness: "none", destination: dest})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("deliver returned %T: %v", out, out)
	}
	if len(order) != 2 || order[0] != "A" || order[1] != "B" {
		t.Errorf("hook order = %v, want [A B]", order)
	}
	got, err := os.ReadFile(filepath.Join(dest, "team/x", "ARTIFACT.md"))
	if err != nil {
		t.Fatalf("read materialized: %v (result=%v)", err, m)
	}
	if want := fm + "|A|B"; string(got) != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

// Spec: §6.6 step 4 — a hook that drops a file prevents that file's write.
func TestDeliver_HookDropsFile(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	s.hooks = []hook.Hook{dropHook{match: "ARTIFACT.md"}}
	fm := "---\ntype: context\n---\nbody"
	out := s.deliverLoadArtifact(fixtureResp("team/x", fm), deliverOpts{harness: "none", destination: dest})
	m := out.(map[string]any)
	paths, _ := m["materialized_at"].([]string)
	if len(paths) != 0 {
		t.Errorf("materialized_at = %v, want empty (file dropped)", paths)
	}
	if _, err := os.Stat(filepath.Join(dest, "team/x", "ARTIFACT.md")); !os.IsNotExist(err) {
		t.Errorf("dropped file was written")
	}
}

// Spec: §6.6 step 4 — a hook error aborts the chain; no files are written.
func TestDeliver_HookErrorAbortsWrite(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	s.hooks = []hook.Hook{errHook{}}
	fm := "---\ntype: context\n---\nbody"
	out := s.deliverLoadArtifact(fixtureResp("team/x", fm), deliverOpts{harness: "none", destination: dest})
	m := out.(map[string]any)
	if code, _ := m["code"].(string); code != "materialize.hook_failed" {
		t.Errorf("code = %v, want materialize.hook_failed (result=%v)", m["code"], m)
	}
	if _, err := os.Stat(filepath.Join(dest, "team/x", "ARTIFACT.md")); !os.IsNotExist(err) {
		t.Errorf("file written despite hook error")
	}
}

// Spec: §6.6 step 4 — "No-op when no hooks are configured." The adapter output
// materializes unchanged.
func TestDeliver_NoHooksIsNoOp(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	fm := "---\ntype: context\n---\nbody"
	s.deliverLoadArtifact(fixtureResp("team/x", fm), deliverOpts{harness: "none", destination: dest})
	got, err := os.ReadFile(filepath.Join(dest, "team/x", "ARTIFACT.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != fm {
		t.Errorf("content = %q, want unchanged %q", got, fm)
	}
}

// Spec: §6.6 step 4 — hook warnings surface in the response alongside the
// materialized paths.
func TestDeliver_HookWarningsSurface(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	s.hooks = []hook.Hook{warnHook{msg: "heads up"}}
	fm := "---\ntype: context\n---\nbody"
	out := s.deliverLoadArtifact(fixtureResp("team/x", fm), deliverOpts{harness: "none", destination: dest})
	m := out.(map[string]any)
	warns, _ := m["warnings"].([]string)
	if len(warns) != 1 || warns[0] != "heads up" {
		t.Errorf("warnings = %v, want [heads up]", m["warnings"])
	}
}

// Spec: §6.6 step 4 — the hook receives the manifest for context.
func TestDeliver_HookReceivesManifestContext(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	var seen map[string]any
	s.hooks = []hook.Hook{ctxHook{seen: &seen}}
	fm := "---\ntype: context\ndescription: hi\n---\nbody"
	s.deliverLoadArtifact(fixtureResp("team/x", fm), deliverOpts{harness: "none", destination: dest})
	if seen["type"] != "context" || seen["description"] != "hi" {
		t.Errorf("manifest context = %v, want type/description populated", seen)
	}
}

// Spec: §6.6 step 4 — the hook chain runs whether or not an adapter is
// configured; harness: none still produces the canonical layout, then hooks
// run over it.
func TestDeliver_HookRunsForHarnessNone(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	var order []string
	s.hooks = []hook.Hook{tagHook{tag: "H", order: &order}}
	fm := "---\ntype: context\n---\nbody"
	s.deliverLoadArtifact(fixtureResp("team/x", fm), deliverOpts{harness: "none", destination: dest})
	if len(order) != 1 {
		t.Errorf("hook did not run for harness none: order=%v", order)
	}
}

// ----- §6.6 step 2 content-hash verification ------------------------------

// Spec: §6.6 step 2 / §4.7.6 — a manifest whose bytes do not reproduce the
// served content_hash is rejected before materialization.
func TestDeliver_ContentHashMismatchRejected(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	resp := fixtureResp("team/x", "---\ntype: context\n---\nbody")
	resp.ContentHash = "sha256:" + strings.Repeat("0", 64) // tampered
	out := s.deliverLoadArtifact(resp, deliverOpts{harness: "none", destination: dest})
	m := out.(map[string]any)
	if code, _ := m["code"].(string); code != "materialize.content_hash_mismatch" {
		t.Errorf("code = %v, want materialize.content_hash_mismatch", m["code"])
	}
	if _, err := os.Stat(filepath.Join(dest, "team/x", "ARTIFACT.md")); !os.IsNotExist(err) {
		t.Errorf("tampered artifact was materialized")
	}
}

// Spec: §6.6 step 2 — a tampered inline resource (consistent content_hash kept
// but resource bytes changed) is rejected, closing the sub-threshold gap.
func TestDeliver_TamperedInlineResourceRejected(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	fm := "---\ntype: context\n---\nbody"
	resp := loadArtifactResponse{
		ID: "team/x", Type: "context", Version: "1.0.0", Frontmatter: fm,
		Resources: map[string]string{"data/a.txt": "original"},
	}
	resp.ContentHash = "sha256:" + version.ContentHash([]byte(fm), nil, []byte("data/a.txt"), []byte("original"))
	// Tamper the resource after the hash was fixed.
	resp.Resources["data/a.txt"] = "tampered"
	out := s.deliverLoadArtifact(resp, deliverOpts{harness: "none", destination: dest})
	m := out.(map[string]any)
	if code, _ := m["code"].(string); code != "materialize.content_hash_mismatch" {
		t.Errorf("code = %v, want materialize.content_hash_mismatch", m["code"])
	}
}

// Spec: §6.6 step 2 — a matching manifest with a bundled resource passes and
// materializes.
func TestDeliver_ContentHashMatchAccepts(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	fm := "---\ntype: context\n---\nbody"
	resp := loadArtifactResponse{
		ID: "team/x", Type: "context", Version: "1.0.0", Frontmatter: fm,
		Resources: map[string]string{"data/a.txt": "hello"},
	}
	resp.ContentHash = "sha256:" + version.ContentHash([]byte(fm), nil, []byte("data/a.txt"), []byte("hello"))
	out := s.deliverLoadArtifact(resp, deliverOpts{harness: "none", destination: dest})
	m := out.(map[string]any)
	if _, isErr := m["error"]; isErr {
		t.Fatalf("matching hash rejected: %v", m)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "team/x", "data/a.txt")); string(b) != "hello" {
		t.Errorf("resource = %q, want hello", b)
	}
}

// Spec: §6.6 step 2 / §4.7.6 — a skill's content_hash covers the verbatim
// SKILL.md the registry ships in skill_raw, so the bridge reproduces the hash
// over (ARTIFACT.md, SKILL.md, resources) and a matching skill passes the
// check instead of skipping it.
func TestDeliver_SkillVerifiesContentHash(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	fm := "---\ntype: skill\n---\n"
	skillRaw := "---\nname: demo\ndescription: a demo skill\n---\nskill prose"
	resp := loadArtifactResponse{
		ID: "team/sk", Type: "skill", Version: "1.0.0",
		Frontmatter:  fm,
		SkillRaw:     skillRaw,
		ManifestBody: "skill prose",
		ContentHash:  "sha256:" + version.ContentHash([]byte(fm), []byte(skillRaw)),
	}
	out := s.deliverLoadArtifact(resp, deliverOpts{harness: "none", destination: dest})
	m := out.(map[string]any)
	if _, isErr := m["error"]; isErr {
		t.Fatalf("valid skill rejected by content-hash check: %v", m)
	}
}

// Spec: §6.6 step 2 — a skill whose served SKILL.md bytes were altered while the
// content_hash field was kept consistent is rejected before materialization,
// closing the gap where a skill skipped the check entirely under a permissive
// signature policy.
func TestDeliver_SkillTamperedSkillRawRejected(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	fm := "---\ntype: skill\n---\n"
	skillRaw := "---\nname: demo\ndescription: a demo skill\n---\nskill prose"
	resp := loadArtifactResponse{
		ID: "team/sk", Type: "skill", Version: "1.0.0",
		Frontmatter:  fm,
		SkillRaw:     skillRaw,
		ManifestBody: "skill prose",
		ContentHash:  "sha256:" + version.ContentHash([]byte(fm), []byte(skillRaw)),
	}
	// Tamper the SKILL.md after the hash was fixed.
	resp.SkillRaw = skillRaw + "\ninjected"
	out := s.deliverLoadArtifact(resp, deliverOpts{harness: "none", destination: dest})
	m := out.(map[string]any)
	if code, _ := m["code"].(string); code != "materialize.content_hash_mismatch" {
		t.Errorf("code = %v, want materialize.content_hash_mismatch", m["code"])
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Errorf("tampered skill left files in destination: %v", entries)
	}
}

// Spec: §6.6 step 2 / §4.6 — a merged manifest's served frontmatter is a
// re-serialization with the hidden parent stripped, so the bridge reproduces
// the hash from the leaf child's pre-merge raw_frontmatter and a matching
// merged manifest passes.
func TestDeliver_MergedManifestVerifiesContentHash(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	raw := "---\ntype: context\nextends: shared/parent@1.x\n---\nbody"
	resp := loadArtifactResponse{
		ID: "team/x", Type: "context", Version: "1.0.0",
		Frontmatter:    "---\ntype: context\n---\nbody", // re-serialized, parent stripped
		RawFrontmatter: raw,
		ManifestMerged: true,
		ContentHash:    "sha256:" + version.ContentHash([]byte(raw)),
	}
	out := s.deliverLoadArtifact(resp, deliverOpts{harness: "none", destination: dest})
	m := out.(map[string]any)
	if _, isErr := m["error"]; isErr {
		t.Fatalf("valid merged manifest rejected by content-hash check: %v", m)
	}
}

// Spec: §6.6 step 2 — a merged manifest whose pre-merge bytes do not reproduce
// the served content_hash (tampered raw_frontmatter) is rejected, so the merged
// path no longer passes step 2 without any integrity check.
func TestDeliver_MergedManifestTamperedRejected(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	raw := "---\ntype: context\nextends: shared/parent@1.x\n---\nbody"
	resp := loadArtifactResponse{
		ID: "team/x", Type: "context", Version: "1.0.0",
		Frontmatter:    "---\ntype: context\n---\nbody",
		RawFrontmatter: raw,
		ManifestMerged: true,
		ContentHash:    "sha256:" + version.ContentHash([]byte(raw)),
	}
	// Tamper the pre-merge bytes after the hash was fixed.
	resp.RawFrontmatter = raw + "\ntampered"
	out := s.deliverLoadArtifact(resp, deliverOpts{harness: "none", destination: dest})
	m := out.(map[string]any)
	if code, _ := m["code"].(string); code != "materialize.content_hash_mismatch" {
		t.Errorf("code = %v, want materialize.content_hash_mismatch", m["code"])
	}
}

// ----- §5.1 materialized_at absolute paths --------------------------------

// Spec: §5.1 — "The returned materialized_at paths are absolute and ready to
// use." Even when the destination is supplied relative (per-call argument or
// PODIUM_MATERIALIZE_ROOT), every returned path is absolute so a host that
// resolves them against its own working directory lands on the correct location.
func TestDeliver_MaterializedAtAbsoluteForRelativeDest(t *testing.T) {
	t.Chdir(t.TempDir()) // relative dest resolves against this dir; not parallel-safe
	s := newTestServer(t, &config{harness: "none", verifyPolicy: sign.PolicyNever})
	resp := fixtureResp("team/x", "---\ntype: context\n---\nbody")
	out := s.deliverLoadArtifact(resp, deliverOpts{harness: "none", destination: "out/dir"})
	m := out.(map[string]any)
	paths, _ := m["materialized_at"].([]string)
	if len(paths) == 0 {
		t.Fatalf("no materialized_at paths returned: %v", m)
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			t.Errorf("materialized_at entry %q is not absolute", p)
		}
	}
}

// Spec: §5.1 — absMaterializeRoot returns an absolute path; an empty root
// (materialization disabled) passes through, and an already-absolute root is
// returned unchanged.
func TestAbsMaterializeRoot(t *testing.T) {
	t.Parallel()
	if got := absMaterializeRoot(""); got != "" {
		t.Errorf(`absMaterializeRoot("") = %q, want ""`, got)
	}
	if got := absMaterializeRoot("rel/sub"); !filepath.IsAbs(got) {
		t.Errorf("absMaterializeRoot(rel/sub) = %q, want absolute", got)
	}
	abs := filepath.Join(t.TempDir(), "x")
	if got := absMaterializeRoot(abs); got != abs {
		t.Errorf("absMaterializeRoot(%q) = %q, want unchanged", abs, got)
	}
}

// ----- resources_base64 -------------------------------------------

// Spec: §6.6 — when the registry flags inline resources base64, the
// MCP decodes them to raw bytes before the hash check and materialization.
func TestDeliver_Base64InlineResourceDecoded(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	raw := []byte{0x00, 0x01, 0x02, 0xff} // binary payload
	enc := base64.StdEncoding.EncodeToString(raw)
	fm := "---\ntype: context\n---\nbody"
	resp := loadArtifactResponse{
		ID: "team/x", Type: "context", Version: "1.0.0", Frontmatter: fm,
		Resources:    map[string]string{"bin/blob": enc},
		ResourcesB64: true,
		// content_hash is over the DECODED bytes, matching the registry.
		ContentHash: "sha256:" + version.ContentHash([]byte(fm), nil, []byte("bin/blob"), raw),
	}
	out := s.deliverLoadArtifact(resp, deliverOpts{harness: "none", destination: dest})
	m := out.(map[string]any)
	if _, isErr := m["error"]; isErr {
		t.Fatalf("base64 delivery failed: %v", m)
	}
	got, err := os.ReadFile(filepath.Join(dest, "team/x", "bin/blob"))
	if err != nil {
		t.Fatalf("read decoded resource: %v", err)
	}
	if string(got) != string(raw) {
		t.Errorf("decoded resource = %v, want %v", got, raw)
	}
}

// Spec: §6.6 — an inline value that does not base64-decode fails the
// call rather than writing the base64 text to disk.
func TestDeliver_InvalidBase64Rejected(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
	resp := loadArtifactResponse{
		ID: "team/x", Type: "context", Version: "1.0.0",
		Frontmatter:  "---\ntype: context\n---\n",
		Resources:    map[string]string{"bin/blob": "not!base64!"},
		ResourcesB64: true,
		ContentHash:  "sha256:" + strings.Repeat("0", 64),
	}
	out := s.deliverLoadArtifact(resp, deliverOpts{harness: "none", destination: dest})
	m := out.(map[string]any)
	if code, _ := m["code"].(string); code != "materialize.invalid_base64" {
		t.Errorf("code = %v, want materialize.invalid_base64", m["code"])
	}
}
