package integration

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/server"
	podiumsync "github.com/lennylabs/podium/pkg/sync"
)

// G-SCALE-3: concurrent consumers sync overlapping catalog slices from one
// booted server to distinct targets, and the shared artifacts materialize
// byte-identically across every target with no stray ".tmp" staging siblings.
//
// The existing sync_concurrent_target_test.go runs many writers to distinct
// targets but each emits its own content token, so it never asserts that a
// shared artifact lands byte-identically across targets through overlapping
// --include filters. This test closes that: it boots a real registry server,
// runs N concurrent podium-sync passes (each with a different --include slice
// that overlaps the others on a shared/** subtree), and after the race diffs
// the shared subtree across every target.
//
// Run under -race. The server source is read-only over HTTP, so the only shared
// mutable state is each consumer's own target directory; a data race in the
// materializer or a cross-target leak surfaces here.

// scaleConcurrentRegistry writes a registry with one shared subtree that every
// consumer includes plus per-consumer private subtrees. It returns the registry
// root. The shared artifacts span a skill (with a bundled resource), an agent,
// a command, and a context so the adapter emits a varied file set whose
// byte-identity across targets is a non-trivial property.
func scaleConcurrentRegistry(t *testing.T, consumers int) string {
	t.Helper()
	root := t.TempDir()
	opts := []testharness.WriteTreeOption{}
	add := func(path, content string) {
		opts = append(opts, testharness.WriteTreeOption{Path: path, Content: content})
	}

	// Shared subtree: included by every consumer. A spread of types and one
	// bundled resource so the materialized shared output is non-trivial.
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("svc-%02d", i)
		base := fmt.Sprintf("shared/svc/%s", name)
		add(base+"/ARTIFACT.md", "---\ntype: context\nversion: 1.0.0\ndescription: Shared service "+name+" reference.\nsensitivity: low\n---\n\nShared service "+name+" body.\n")
	}
	// A shared skill with a bundled resource. Per §4.3.4 the skill's name and
	// description live in SKILL.md; ARTIFACT.md carries Podium frontmatter only.
	add("shared/tools/greeter/ARTIFACT.md", "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- Skill body lives in SKILL.md. -->\n")
	add("shared/tools/greeter/SKILL.md", "---\nname: greeter\ndescription: The greeter skill. Use when greeting a user.\n---\n\nGreet warmly.\n")
	add("shared/tools/greeter/helper.py", "print('shared helper')\n")
	// A shared agent and command.
	add("shared/agents/router/ARTIFACT.md", "---\ntype: agent\nversion: 1.0.0\ndescription: Shared routing agent.\n---\n\nRoute requests.\n")
	add("shared/cmds/deploy/ARTIFACT.md", "---\ntype: command\nversion: 1.0.0\ndescription: Shared deploy command.\n---\n\n$ARGUMENTS\n")

	// Per-consumer private subtrees: only consumer k includes ck/**.
	for k := 0; k < consumers; k++ {
		for i := 0; i < 8; i++ {
			id := fmt.Sprintf("c%d/local/item-%02d", k, i)
			add(id+"/ARTIFACT.md", fmt.Sprintf("---\ntype: context\nversion: 1.0.0\ndescription: Consumer %d private item %d.\nsensitivity: low\n---\n\nprivate body.\n", k, i))
		}
	}
	testharness.WriteTree(t, root, opts...)
	return root
}

func TestSyncConcurrent_OverlappingIncludeByteIdenticalShared(t *testing.T) {
	t.Parallel()
	const consumers = 8

	registry := scaleConcurrentRegistry(t, consumers)
	srv, err := server.NewFromFilesystem(registry)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	// One booted server, many concurrent consumers reading from it.
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	parent := t.TempDir()
	targets := make([]string, consumers)
	for k := range targets {
		targets[k] = filepath.Join(parent, fmt.Sprintf("consumer-%d", k))
	}

	// Each consumer includes the shared subtree (the overlap) plus only its own
	// private subtree. The include slices overlap exactly on shared/**.
	var errs atomic.Int64
	ready := make(chan struct{})
	var wg sync.WaitGroup
	for k := 0; k < consumers; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			include := []string{"shared/**", fmt.Sprintf("c%d/**", k)}
			<-ready
			if _, err := podiumsync.Run(podiumsync.Options{
				RegistryPath: ts.URL,
				Target:       targets[k],
				AdapterID:    "claude-code",
				Scope:        podiumsync.ScopeFilter{Include: include},
			}); err != nil {
				errs.Add(1)
				t.Errorf("consumer %d sync: %v", k, err)
			}
		}(k)
	}
	close(ready)
	wg.Wait()

	if errs.Load() != 0 {
		t.Fatalf("%d concurrent sync errors; want 0", errs.Load())
	}

	// ---- byte-identical shared output across every target -----------------
	// The shared subtree materializes under the claude-code layout. Extract
	// each target's shared-artifact files and diff them against consumer 0's.
	// A target's private files (.podium/context/c<k>/...) are excluded from the
	// comparison because they legitimately differ per consumer.
	reference := sharedSubtree(t, targets[0])
	if len(reference) == 0 {
		t.Fatalf("consumer 0 materialized no shared output")
	}
	// The shared set must include the skill, its bundled resource, the agent,
	// the command, and the context bodies.
	mustHaveSharedShape(t, reference)

	for k := 1; k < consumers; k++ {
		got := sharedSubtree(t, targets[k])
		assertTreesEqual(t, reference, got)
	}

	// ---- no stray .tmp staging siblings anywhere --------------------------
	for k := 0; k < consumers; k++ {
		assertNoTmpSiblings(t, targets[k])
	}

	// ---- each target is complete for its own include set ------------------
	for k := 0; k < consumers; k++ {
		// Each consumer's private context items materialized.
		for i := 0; i < 8; i++ {
			p := filepath.Join(targets[k], ".podium", "context", fmt.Sprintf("c%d/local/item-%02d", k, i), "ARTIFACT.md")
			if _, err := os.Stat(p); err != nil {
				t.Errorf("consumer %d missing its private item %d: %v", k, i, err)
			}
		}
		// No other consumer's private subtree leaked into this target.
		for j := 0; j < consumers; j++ {
			if j == k {
				continue
			}
			leak := filepath.Join(targets[k], ".podium", "context", fmt.Sprintf("c%d", j))
			if _, err := os.Stat(leak); err == nil {
				t.Errorf("consumer %d target contains consumer %d's private subtree", k, j)
			}
		}
	}
}

// sharedSubtree returns the materialized files under a target that originate
// from the shared/** include slice, keyed by relative path. It reads the whole
// tree and keeps only the files the shared artifacts produce, dropping the lock
// file and each consumer's private context bucket.
func sharedSubtree(t testing.TB, target string) map[string]string {
	t.Helper()
	full := testharness.ReadTree(t, target)
	out := map[string]string{}
	for path, content := range full {
		if strings.HasPrefix(path, ".podium/sync.lock") {
			continue
		}
		// Drop any consumer-private context bucket (.podium/context/c<k>/...).
		if strings.HasPrefix(path, ".podium/context/c") {
			continue
		}
		out[path] = content
	}
	return out
}

// mustHaveSharedShape asserts the shared materialized set carries the expected
// claude-code output for the shared artifacts: the skill SKILL.md, its bundled
// resource, the agent, the command, and at least one shared context body.
func mustHaveSharedShape(t testing.TB, tree map[string]string) {
	t.Helper()
	want := []string{
		".claude/skills/greeter/SKILL.md",
		".claude/skills/greeter/helper.py",
		".claude/agents/router.md",
		".claude/commands/deploy.md",
		".podium/context/shared/svc/svc-00/ARTIFACT.md",
	}
	for _, p := range want {
		if _, ok := tree[p]; !ok {
			t.Errorf("shared output missing expected file %q; have %d files", p, len(tree))
		}
	}
}

// assertNoTmpSiblings walks a target tree and fails on any leftover ".tmp"
// staging file (the materializer stages to "<path>.tmp" then renames).
func assertNoTmpSiblings(t testing.TB, target string) {
	t.Helper()
	err := filepath.Walk(target, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(p, ".tmp") {
			t.Errorf("leftover staging temporary: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", target, err)
	}
}
