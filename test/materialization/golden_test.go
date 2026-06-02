// Package materialization holds the §6.6/§6.7 golden materialization
// conformance suite: a fixed canonical artifact set is materialized through
// every built-in harness adapter and the exact on-disk tree is snapshotted to a
// golden file. Materializing artifacts into each harness's native layout and
// format is the core of the product, so these goldens pin the precise paths and
// file contents per harness; any drift in a harness's expected layout, file
// extension, frontmatter transform, rule-mode mapping, inject markers, or
// config-merge structure fails the suite.
//
// Regenerate after an intentional change with: UPDATE_GOLDEN=1 go test ./test/materialization/
package materialization

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/materialize"
)

// fixture is one canonical artifact: the ARTIFACT.md bytes, optional SKILL.md
// (type: skill only), and bundled resources keyed by registry-relative path.
type fixture struct {
	id        string
	artifact  string
	skill     string
	resources map[string]string
}

// canonicalArtifacts is the fixed input set: one artifact per first-class type,
// plus one rule per mode, exercising the fields the adapters translate
// (rule_mode/rule_globs/rule_description, hook_event/hook_action,
// server_identifier, description, bundled resources).
var canonicalArtifacts = []fixture{
	{
		id:        "team/hello",
		artifact:  "---\ntype: skill\nversion: 1.0.0\n---\n\nUse this skill to greet a teammate.\n",
		skill:     "---\nname: hello\ndescription: Greet a teammate warmly by name.\n---\n\nSay hello to the person and offer help.\n",
		resources: map[string]string{"scripts/run.py": "print('hello')\n"},
	},
	{
		id:       "team/reviewer",
		artifact: "---\ntype: agent\nversion: 1.0.0\ndescription: Reviews pull requests for bugs.\n---\n\nReview the diff and report defects.\n",
	},
	{
		id:       "team/glossary",
		artifact: "---\ntype: context\nversion: 1.0.0\ndescription: Company glossary.\n---\n\nARR means annual recurring revenue.\n",
	},
	{
		id:       "team/deploy",
		artifact: "---\ntype: command\nversion: 1.0.0\ndescription: Deploy the service.\n---\n\nRun the deploy pipeline for the named environment.\n",
	},
	{
		id:       "team/tabs-always",
		artifact: "---\ntype: rule\nversion: 1.0.0\nrule_mode: always\n---\n\nIndent with tabs, not spaces.\n",
	},
	{
		id:       "team/ts-glob",
		artifact: "---\ntype: rule\nversion: 1.0.0\nrule_mode: glob\nrule_globs: \"*.ts,*.tsx\"\n---\n\nEnable strict type-checking in TypeScript files.\n",
	},
	{
		id:       "team/ts-auto",
		artifact: "---\ntype: rule\nversion: 1.0.0\nrule_mode: auto\nrule_description: Apply when editing TypeScript.\n---\n\nPrefer interfaces over type aliases.\n",
	},
	{
		id:       "team/deep-review",
		artifact: "---\ntype: rule\nversion: 1.0.0\nrule_mode: explicit\n---\n\nPerform a deep architectural review when invoked.\n",
	},
	{
		id:       "team/notify",
		artifact: "---\ntype: hook\nversion: 1.0.0\nhook_event: stop\nhook_action: echo done\n---\n\nNotify when the agent stops.\n",
	},
	{
		id:       "team/guard",
		artifact: "---\ntype: hook\nversion: 1.0.0\nhook_event: pre_tool_use\nhook_action: ./scripts/guard.sh\n---\n\nGuard before each tool use.\n",
	},
	{
		id:       "team/warehouse",
		artifact: "---\ntype: mcp-server\nversion: 1.0.0\nserver_identifier: npx:@acme/warehouse-mcp\n---\n\nWarehouse MCP server.\n",
	},
}

// harnesses is the full built-in adapter set, in capability-matrix column order.
var harnesses = []string{
	"none", "claude-code", "claude-desktop", "claude-cowork",
	"cursor", "codex", "opencode", "gemini", "pi", "hermes",
}

// TestGolden_HarnessMaterialization materializes the canonical artifact set
// through each adapter and snapshots the resulting on-disk tree against a
// golden file. The end-to-end path (adapter.Adapt then materialize.Write) is
// exercised so config-merge folding and inject markers appear as they land on
// disk.
func TestGolden_HarnessMaterialization(t *testing.T) {
	for _, h := range harnesses {
		h := h
		t.Run(h, func(t *testing.T) {
			dir, files := materializeCanonical(t, h)
			snap := snapshotTree(t, dir)
			testharness.AssertGoldenFile(t, filepath.Join("testdata", "golden", h+".golden"), snap)

			// Idempotency: a second materialization into the same tree must
			// reconcile to the identical result. This guards the §6.7
			// config-merge and inject reconciliation against duplicating
			// entries (a doubled inject block, a repeated mcpServers entry, or a
			// marketplace plugin listed twice) on re-sync.
			if err := materialize.Write(dir, files); err != nil {
				t.Fatalf("%s materialize.Write (second pass): %v", h, err)
			}
			if again := snapshotTree(t, dir); !bytes.Equal(again, snap) {
				t.Errorf("%s materialization is not idempotent; second pass differs:\n%s",
					h, diffSnips(snap, again))
			}
		})
	}
}

// diffSnips returns a compact view of the first line at which two snapshots
// diverge, for the idempotency failure message.
func diffSnips(a, b []byte) string {
	al := bytes.Split(a, []byte("\n"))
	bl := bytes.Split(b, []byte("\n"))
	n := len(al)
	if len(bl) < n {
		n = len(bl)
	}
	for i := 0; i < n; i++ {
		if !bytes.Equal(al[i], bl[i]) {
			return fmt.Sprintf("first diff at line %d:\n  pass1: %s\n  pass2: %s", i+1, al[i], bl[i])
		}
	}
	return fmt.Sprintf("differ in length: pass1=%d lines, pass2=%d lines", len(al), len(bl))
}

// materializeCanonical runs the canonical artifact set through the named
// adapter and writes the result into a fresh temp directory, returning the
// directory and the adapter.File set (so callers can re-materialize for the
// idempotency check). Shared by the golden and validity suites.
func materializeCanonical(t *testing.T, harness string) (string, []adapter.File) {
	t.Helper()
	a, err := adapter.DefaultRegistry().Get(harness)
	if err != nil {
		t.Fatalf("Get(%q): %v", harness, err)
	}
	var files []adapter.File
	for _, fx := range canonicalArtifacts {
		src := adapter.Source{
			ArtifactID:    fx.id,
			ArtifactBytes: []byte(fx.artifact),
			SkillBytes:    []byte(fx.skill),
			Resources:     resourceBytes(fx.resources),
		}
		out, err := a.Adapt(context.Background(), src)
		if err != nil {
			t.Fatalf("%s.Adapt(%s): %v", harness, fx.id, err)
		}
		files = append(files, out...)
	}
	dir := t.TempDir()
	if err := materialize.Write(dir, files); err != nil {
		t.Fatalf("%s materialize.Write: %v", harness, err)
	}
	return dir, files
}

func resourceBytes(m map[string]string) map[string][]byte {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		out[k] = []byte(v)
	}
	return out
}

// snapshotTree renders the materialized tree as a deterministic text snapshot:
// files sorted by path, each as a "=== <relpath> ===" header followed by its
// contents. Sorting by path makes the snapshot independent of write order.
func snapshotTree(t *testing.T, root string) []byte {
	t.Helper()
	var rels []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(rels)
	var b bytes.Buffer
	if len(rels) == 0 {
		b.WriteString("(no project-level output)\n")
		return b.Bytes()
	}
	for _, rel := range rels {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		fmt.Fprintf(&b, "=== %s ===\n", rel)
		b.Write(content)
		if len(content) > 0 && content[len(content)-1] != '\n' {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return b.Bytes()
}
