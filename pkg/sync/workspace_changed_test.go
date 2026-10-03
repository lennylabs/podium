package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/adapter"
)

// switchableAdapter is a stub HarnessAdapter whose output format is selected
// by a test-controlled field, so a test can change the bytes an adapter emits
// while the registry and the lock stay unchanged.
type switchableAdapter struct {
	format *string
}

func (switchableAdapter) ID() string { return "switchable" }

func (a switchableAdapter) Adapt(_ context.Context, src adapter.Source) ([]adapter.File, error) {
	return []adapter.File{{
		Path:    filepath.ToSlash(filepath.Join("out", src.ArtifactID+".txt")),
		Content: []byte(*a.format + ":" + src.ArtifactID + "\n"),
	}}, nil
}

// syncChanged runs one sync with opts and returns Result.Changed, failing the
// test on a Run error.
func syncChanged(t *testing.T, opts Options) bool {
	t.Helper()
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res.Changed
}

// wantChanged asserts the Result.Changed of one sync of opts.
func wantChanged(t *testing.T, opts Options, want bool, why string) {
	t.Helper()
	if got := syncChanged(t, opts); got != want {
		t.Errorf("%s: Changed = %v, want %v", why, got, want)
	}
}

// contextRegistry writes a registry of context artifacts, one per ID, and
// returns its root.
func contextRegistry(t *testing.T, ids ...string) string {
	t.Helper()
	registry := t.TempDir()
	opts := make([]testharness.WriteTreeOption, 0, len(ids))
	for _, id := range ids {
		opts = append(opts, testharness.WriteTreeOption{Path: id + "/ARTIFACT.md", Content: contextArtifactSrc})
	}
	testharness.WriteTree(t, registry, opts...)
	return registry
}

// readTargetFile returns the bytes of a materialized file under target.
func readTargetFile(t *testing.T, target, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("ReadFile %s: %v", rel, err)
	}
	return data
}

// Spec: §7.5.2 — a change to the bytes an adapter emits alters the bytes on
// disk although the registry and the prior lock are unchanged, so the re-sync
// reports Changed true, and the following unchanged re-sync reports false.
func TestRun_ChangedOnAdapterOutputChange(t *testing.T) {
	t.Parallel()
	format := "v1"
	reg := adapter.NewRegistry()
	if err := reg.Register(switchableAdapter{format: &format}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	opts := Options{
		RegistryPath:    contextRegistry(t, "company-glossary"),
		Target:          t.TempDir(),
		AdapterID:       "switchable",
		AdapterRegistry: reg,
	}
	wantChanged(t, opts, true, "first sync")
	format = "v2"
	wantChanged(t, opts, true, "re-sync after the adapter output changed")
	wantChanged(t, opts, false, "unchanged re-sync")
}

// Spec: §7.5.2, §6.7 — swapping the inject blocks of AGENTS.md by hand alters
// the merged file; the re-sync restores ascending-ID block order and reports
// Changed true although the lock is unchanged.
func TestRun_ChangedRestoresInjectFoldOrder(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	rule := "---\ntype: rule\nversion: 1.0.0\nrule_mode: always\n---\n\n"
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: "a-rules/policy/ARTIFACT.md", Content: rule + "Policy rule.\n"},
		testharness.WriteTreeOption{Path: "z-rules/style/ARTIFACT.md", Content: rule + "Style rule.\n"},
	)
	opts := Options{RegistryPath: registry, Target: target, AdapterID: "codex"}
	syncChanged(t, opts)
	original := readTargetFile(t, target, "AGENTS.md")

	swapped := swapInjectBlocks(t, string(original), "a-rules/policy", "z-rules/style")
	writeFile(t, filepath.Join(target, "AGENTS.md"), swapped)

	wantChanged(t, opts, true, "re-sync after the blocks were swapped")
	got := string(readTargetFile(t, target, "AGENTS.md"))
	if a, z := strings.Index(got, "podium:begin:a-rules/policy"), strings.Index(got, "podium:begin:z-rules/style"); a < 0 || z < 0 || a > z {
		t.Errorf("AGENTS.md blocks not in ascending-ID order after re-sync:\n%s", got)
	}
}

// swapInjectBlocks returns s with the inject blocks keyed by first and second
// exchanged. Both blocks must be present.
func swapInjectBlocks(t *testing.T, s, first, second string) string {
	t.Helper()
	block := func(key string) string {
		begin := "<!-- podium:begin:" + key + " -->"
		end := "<!-- podium:end:" + key + " -->"
		bi := strings.Index(s, begin)
		ei := strings.Index(s, end)
		if bi < 0 || ei < bi {
			t.Fatalf("inject block %q not found in:\n%s", key, s)
		}
		return s[bi : ei+len(end)]
	}
	a, z := block(first), block(second)
	const placeholder = "\x00swap\x00"
	s = strings.Replace(s, a, placeholder, 1)
	s = strings.Replace(s, z, a, 1)
	return strings.Replace(s, placeholder, z, 1)
}

// Spec: §7.5.2 — a hand edit or a hand deletion of a materialized file is
// restored by the re-sync, which alters the bytes on disk and reports Changed
// true. The edit takes the differing-digest branch of the comparison and the
// deletion the newly-present branch.
func TestRun_ChangedRestoresHandEdits(t *testing.T) {
	t.Parallel()
	const rel = "company-glossary/ARTIFACT.md"

	t.Run("hand edit", func(t *testing.T) {
		t.Parallel()
		opts := Options{RegistryPath: contextRegistry(t, "company-glossary"), Target: t.TempDir(), AdapterID: "none"}
		syncChanged(t, opts)
		original := readTargetFile(t, opts.Target, rel)
		writeFile(t, filepath.Join(opts.Target, rel), "edited by hand\n")
		wantChanged(t, opts, true, "re-sync after a hand edit")
		if got := readTargetFile(t, opts.Target, rel); !bytes.Equal(got, original) {
			t.Errorf("hand edit not restored: got %q, want %q", got, original)
		}
	})

	t.Run("hand deletion", func(t *testing.T) {
		t.Parallel()
		opts := Options{RegistryPath: contextRegistry(t, "company-glossary"), Target: t.TempDir(), AdapterID: "none"}
		syncChanged(t, opts)
		if err := os.Remove(filepath.Join(opts.Target, rel)); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		wantChanged(t, opts, true, "re-sync after a hand deletion")
		readTargetFile(t, opts.Target, rel)
	})
}

// Spec: §7.5.2, §7.5.3 — the lock file is never a compared path: an unchanged
// re-sync rewrites the lock and reports Changed false, and a re-sync after the
// lock was deleted finds the tree already current and reports false.
func TestRun_ChangedIgnoresLockFile(t *testing.T) {
	t.Parallel()

	t.Run("lock-only rewrite", func(t *testing.T) {
		t.Parallel()
		opts := Options{RegistryPath: contextRegistry(t, "company-glossary"), Target: t.TempDir(), AdapterID: "none"}
		syncChanged(t, opts)
		wantChanged(t, opts, false, "unchanged re-sync")
	})

	t.Run("missing prior lock", func(t *testing.T) {
		t.Parallel()
		opts := Options{RegistryPath: contextRegistry(t, "company-glossary"), Target: t.TempDir(), AdapterID: "none"}
		syncChanged(t, opts)
		if err := os.Remove(LockFilePath(opts.Target)); err != nil {
			t.Fatalf("Remove lock: %v", err)
		}
		wantChanged(t, opts, false, "re-sync without a prior lock over a current tree")
	})
}

// Spec: §7.5.2, §7.5.3 — removing an artifact deletes its prior-lock path,
// which alters the bytes on disk; the following re-sync no longer compares the
// path and reports Changed false.
func TestRun_ChangedOnStaleRemoval(t *testing.T) {
	t.Parallel()
	registry := contextRegistry(t, "company-glossary", "style-guide")
	opts := Options{RegistryPath: registry, Target: t.TempDir(), AdapterID: "none"}
	syncChanged(t, opts)
	if err := os.RemoveAll(filepath.Join(registry, "style-guide")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	wantChanged(t, opts, true, "re-sync after an artifact was removed")
	if _, err := os.Stat(filepath.Join(opts.Target, "style-guide", "ARTIFACT.md")); !os.IsNotExist(err) {
		t.Errorf("stale file survived: stat err = %v", err)
	}
	wantChanged(t, opts, false, "re-sync after the stale removal")
}

// Spec: §7.5.2, §6.7 — a config-merge path is compared as the whole merged
// file. An operator entry written in the form the merge serializes leaves the
// file byte-identical across a re-sync; the same entry in another formatting is
// rewritten, which alters the bytes.
func TestRun_ChangedComparesMergedFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		marshal func(v any) ([]byte, error)
		want    bool
	}{
		{"canonical formatting", func(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }, false},
		{"non-canonical formatting", json.Marshal, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			registry := t.TempDir()
			testharness.WriteTree(t, registry, testharness.WriteTreeOption{
				Path: "a-alpha/server/ARTIFACT.md", Content: mcpServerSrc("alpha", "Alpha server."),
			})
			opts := Options{RegistryPath: registry, Target: t.TempDir(), AdapterID: "claude-code"}
			syncChanged(t, opts)
			addOperatorServer(t, filepath.Join(opts.Target, ".mcp.json"), tc.marshal)

			wantChanged(t, opts, tc.want, "re-sync after the operator entry was added")
			var merged map[string]map[string]any
			if err := json.Unmarshal(readTargetFile(t, opts.Target, ".mcp.json"), &merged); err != nil {
				t.Fatalf("Unmarshal .mcp.json: %v", err)
			}
			if _, ok := merged["mcpServers"]["operator"]; !ok {
				t.Errorf("operator entry not preserved: %v", merged)
			}
		})
	}
}

// addOperatorServer adds an untagged mcpServers entry to the JSON file at path
// and writes it back with marshal plus a trailing newline.
func addOperatorServer(t *testing.T, path string, marshal func(v any) ([]byte, error)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	servers, ok := v["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing from %s", data)
	}
	servers["operator"] = map[string]any{"command": "operator-server"}
	out, err := marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writeFile(t, path, string(out)+"\n")
}

// Spec: §7.5.2 — a read error before the write does not fail the sync, so the
// materialize write alone decides the outcome: a non-empty directory at a
// standalone path fails the write with its own error and leaves no lock.
func TestRun_ChangedUnreadablePathWriteFails(t *testing.T) {
	t.Parallel()
	opts := Options{RegistryPath: contextRegistry(t, "company-glossary"), Target: t.TempDir(), AdapterID: "none"}
	writeFile(t, filepath.Join(opts.Target, "company-glossary", "ARTIFACT.md", "keep"), "operator\n")

	_, err := Run(opts)
	if err == nil {
		t.Fatal("Run over a non-empty directory at a materialized path succeeded")
	}
	if strings.Contains(err.Error(), "sync: read target") {
		t.Errorf("Run failed on the comparison read instead of the write: %v", err)
	}
	if lock, lerr := ReadLock(opts.Target); lerr != nil || lock != nil {
		t.Errorf("ReadLock = (%v, %v), want no lock", lock, lerr)
	}
}

// Spec: §7.5.2 — an unreadable standalone materialized file does not fail the
// sync. The write replaces it, the unobservable starting state counts as
// changed, and the following re-sync reports Changed false.
func TestRun_ChangedUnreadableStandaloneFile(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file, so the read error cannot occur")
	}
	const rel = "company-glossary/ARTIFACT.md"
	opts := Options{RegistryPath: contextRegistry(t, "company-glossary"), Target: t.TempDir(), AdapterID: "none"}
	syncChanged(t, opts)
	original := readTargetFile(t, opts.Target, rel)
	if err := os.Chmod(filepath.Join(opts.Target, rel), 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	wantChanged(t, opts, true, "re-sync over an unreadable materialized file")
	if got := readTargetFile(t, opts.Target, rel); !bytes.Equal(got, original) {
		t.Errorf("file after re-sync = %q, want %q", got, original)
	}
	wantChanged(t, opts, false, "re-sync after the file was replaced")
}

// Spec: §7.5.2, §7.5.3 — an unreadable prior-only path does not fail the
// sync: the best-effort stale-file cleanup logs that it could not remove the
// path, the run reports Changed true, and the following re-sync, whose prior
// lock no longer names the path, reports false.
//
// Not parallel: the test swaps the process-wide os.Stderr to capture the
// cleanup log line.
func TestRun_ChangedUnreadablePriorOnlyPath(t *testing.T) {
	registry := contextRegistry(t, "company-glossary", "style-guide")
	opts := Options{RegistryPath: registry, Target: t.TempDir(), AdapterID: "none"}
	syncChanged(t, opts)
	if err := os.RemoveAll(filepath.Join(registry, "style-guide")); err != nil {
		t.Fatalf("RemoveAll registry: %v", err)
	}
	stale := filepath.Join(opts.Target, "style-guide", "ARTIFACT.md")
	if err := os.Remove(stale); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	writeFile(t, filepath.Join(stale, "keep"), "operator\n")

	var changed bool
	stderr := captureStderr(t, func() { changed = syncChanged(t, opts) })
	if !changed {
		t.Errorf("re-sync over an unreadable prior-only path: Changed = false, want true")
	}
	if !strings.Contains(stderr, "sync: stale-file cleanup:") {
		t.Errorf("stderr = %q, want the stale-file cleanup log line", stderr)
	}
	wantChanged(t, opts, false, "re-sync after the path left the prior lock")
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what
// fn wrote to it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	defer func() {
		os.Stderr = orig
	}()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("Close pipe: %v", err)
	}
	return <-done
}

// Spec: §7.5.2, §7.5.3 — an edit to a field the adapter does not emit moves
// the lock's content_hash but leaves the materialized bytes unchanged, so the
// re-sync reports Changed false.
func TestRun_ChangedIgnoresUnemittedMetadata(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	artifact := func(tags string) string {
		return "---\ntype: skill\nversion: 1.0.0\ntags: [" + tags + "]\n---\n\n"
	}
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: "greetings/hello/ARTIFACT.md", Content: artifact("greeting")},
		testharness.WriteTreeOption{Path: "greetings/hello/SKILL.md", Content: skillBodySrc},
	)
	opts := Options{RegistryPath: registry, Target: t.TempDir(), AdapterID: "claude-code"}
	syncChanged(t, opts)
	before := lockedContentHash(t, opts.Target, "greetings/hello")

	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: "greetings/hello/ARTIFACT.md", Content: artifact("greeting, onboarding")},
	)
	wantChanged(t, opts, false, "re-sync after a tags-only edit")
	if after := lockedContentHash(t, opts.Target, "greetings/hello"); after == before {
		t.Errorf("lock content_hash for greetings/hello did not move: %q", after)
	}
}

// lockedContentHash returns the content_hash the lock at target records for id.
func lockedContentHash(t *testing.T, target, id string) string {
	t.Helper()
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = (%v, %v)", lock, err)
	}
	for _, a := range lock.Artifacts {
		if a.ID == id {
			return a.ContentHash
		}
	}
	t.Fatalf("lock has no entry for %s", id)
	return ""
}

// Spec: §7.5.2 — concurrent syncs into distinct targets compare their own
// paths and report independent Changed values. Run with -race.
func TestRun_ChangedIndependentAcrossTargets(t *testing.T) {
	t.Parallel()
	registry := contextRegistry(t, "company-glossary")
	current := Options{RegistryPath: registry, Target: t.TempDir(), AdapterID: "none"}
	syncChanged(t, current)
	fresh := Options{RegistryPath: registry, Target: t.TempDir(), AdapterID: "none"}

	t.Run("fresh target", func(t *testing.T) {
		t.Parallel()
		wantChanged(t, fresh, true, "first sync into an empty target")
	})
	t.Run("current target", func(t *testing.T) {
		t.Parallel()
		wantChanged(t, current, false, "re-sync of a current target")
	})
}
