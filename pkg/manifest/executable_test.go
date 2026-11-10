package manifest_test

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
)

// parentPin is the reference every single-parent case's child declares.
const parentPin = "shared/parent@1.x"

// basePin is the reference an intermediate chain member declares to its root.
const basePin = "core/base@1.x"

// execCase is one input to InheritedExecutableFields and the field paths it
// must return. chain holds the parent's authored blocks, root first and the
// parent last, and resolveChain folds the chain-resolved parent from it.
type execCase struct {
	name string
	// id is the child's canonical ID; empty means childID.
	id    string
	chain []manifest.MergedBlock
	child string
	want  []string
}

// doc builds an ARTIFACT.md with the given type, extends reference, and extra
// frontmatter lines.
func doc(typ, extends, extra string) string {
	s := "---\ntype: " + typ + "\nversion: 1.0.0\n"
	if extends != "" {
		s += "extends: " + extends + "\n"
	}
	return s + extra + "---\n\nbody\n"
}

// root is a chain member that extends nothing.
func root(typ, extra string) manifest.MergedBlock {
	return block(doc(typ, "", extra), "")
}

// overBase is a chain member that extends the root through basePin.
func overBase(typ, extra string) manifest.MergedBlock {
	return block(doc(typ, basePin, extra), basePin)
}

// childOf is a child ARTIFACT.md that pins the single parent.
func childOf(typ, extra string) string {
	return doc(typ, parentPin, extra)
}

// resolveChain folds the chain's authored blocks through MergeExtends, root
// first, which is the chain-resolved parent the read path serves. An empty
// block contributes nothing.
func resolveChain(t *testing.T, chain []manifest.MergedBlock) manifest.Artifact {
	t.Helper()
	var out manifest.Artifact
	started := false
	for _, b := range chain {
		if len(b.Frontmatter) == 0 {
			continue
		}
		a, err := manifest.ParseArtifact(b.Frontmatter)
		if err != nil {
			t.Fatalf("ParseArtifact(chain): %v", err)
		}
		if !started {
			out, started = *a, true
			continue
		}
		out = manifest.MergeExtends(out, *a)
	}
	return out
}

// jiraParent sets every member of the jira entry.
const jiraParent = "mcpServers:\n  - name: jira\n    transport: stdio\n    command: jira-mcp\n" +
	"    args: [--stdio]\n    env:\n      TOKEN_FILE: x\n"

// execCases is the table the expected-path test and the property test share.
func execCases() []execCase {
	var cases []execCase
	cases = append(cases, scalarCases()...)
	cases = append(cases, serverCases()...)
	cases = append(cases, undeclaredCases()...)
	cases = append(cases, excludedCases()...)
	return append(cases, orderingCases()...)
}

// scalarCases covers hook_action and server_identifier.
//
// Spec: §4.6 executable fields across layers. A parent's value is restated
// only by a non-empty child value, which may equal the parent's or differ from
// it.
func scalarCases() []execCase {
	var cases []execCase
	for _, f := range []struct{ typ, key string }{
		{"hook", "hook_action"},
		{"mcp-server", "server_identifier"},
	} {
		parent := []manifest.MergedBlock{root(f.typ, f.key+": parent-run\n")}
		cases = append(cases,
			execCase{name: f.key + " omitted", chain: parent, child: childOf(f.typ, ""), want: []string{f.key}},
			execCase{name: f.key + " empty scalar", chain: parent, child: childOf(f.typ, f.key+": \"\"\n"), want: []string{f.key}},
			execCase{name: f.key + " restated same", chain: parent, child: childOf(f.typ, f.key+": parent-run\n")},
			execCase{name: f.key + " restated different", chain: parent, child: childOf(f.typ, f.key+": child-run\n")},
		)
	}
	return cases
}

// serverCases covers mcpServers.
//
// Spec: §4.6 executable fields across layers. Each entry the parent sets is
// restated member by member, and each env key counts on its own. A decoded
// `mcpServers: []` cannot be told apart from an absent key and the merge keeps
// every parent entry, so each entry is still reported; an undeclared key set
// to `[]` beside it is a value other than an empty scalar and counts as
// restated.
func serverCases() []execCase {
	jira := []manifest.MergedBlock{root("agent", jiraParent)}
	return []execCase{
		{name: "mcpServers entry omitted", chain: jira, child: childOf("agent", ""),
			want: []string{"mcpServers[jira]"}},
		{name: "mcpServers entry name only", chain: jira,
			child: childOf("agent", "mcpServers:\n  - name: jira\n"),
			want: []string{
				"mcpServers[jira].args", "mcpServers[jira].command",
				"mcpServers[jira].env.TOKEN_FILE", "mcpServers[jira].transport",
			}},
		{name: "mcpServers entry with another env key", chain: jira,
			child: childOf("agent", "mcpServers:\n  - name: jira\n    transport: sse\n    command: other\n"+
				"    args: [--sse]\n    env:\n      OTHER: y\n"),
			want: []string{"mcpServers[jira].env.TOKEN_FILE"}},
		{name: "mcpServers entry fully restated", chain: jira,
			child: childOf("agent", "mcpServers:\n  - name: jira\n    transport: sse\n    command: other\n"+
				"    args: [--sse]\n    env:\n      TOKEN_FILE: z\n")},
		{name: "mcpServers parent sets transport alone",
			chain: []manifest.MergedBlock{root("agent", "mcpServers:\n  - name: jira\n    transport: stdio\n")},
			child: childOf("agent", "mcpServers:\n  - name: jira\n"),
			want:  []string{"mcpServers[jira].transport"}},
		{name: "mcpServers parent sets name alone",
			chain: []manifest.MergedBlock{root("agent", "mcpServers:\n  - name: jira\n")},
			child: childOf("agent", "mcpServers:\n  - name: jira\n")},
		{name: "mcpServers [] beside undeclared []",
			chain: []manifest.MergedBlock{root("agent", jiraParent+
				"  - name: wiki\n    command: wiki-mcp\nacme_tools: [lint]\n")},
			child: childOf("agent", "mcpServers: []\nacme_tools: []\n"),
			want:  []string{"mcpServers[jira]", "mcpServers[wiki]"}},
	}
}

// undeclaredCases covers the undeclared keys, including a value the parent
// inherited from its own chain.
//
// Spec: §4.6 executable fields across layers. An undeclared key is restated by
// any value other than an empty scalar, and what the parent sets is its
// chain-resolved value, so a grandparent's key reaches the child through an
// intermediate that omits it or sets it to an empty scalar. A parent value
// that is an empty scalar sets nothing (§4.6 omitted fields).
func undeclaredCases() []execCase {
	hooks := []manifest.MergedBlock{root("agent", "acme_hooks:\n  pre: x\n")}
	cases := []execCase{
		{name: "undeclared key omitted", chain: hooks, child: childOf("agent", ""),
			want: []string{"acme_hooks"}},
		{name: "undeclared key empty scalar", chain: hooks, child: childOf("agent", "acme_hooks:\n"),
			want: []string{"acme_hooks"}},
		{name: "undeclared key null", chain: hooks, child: childOf("agent", "acme_hooks: ~\n"),
			want: []string{"acme_hooks"}},
		{name: "undeclared key restated", chain: hooks, child: childOf("agent", "acme_hooks:\n  pre: y\n")},
		{name: "empty chain block contributes nothing",
			chain: []manifest.MergedBlock{block("", ""), overBase("agent", "acme_hooks: x\n")},
			child: childOf("agent", ""),
			want:  []string{"acme_hooks"}},
		{name: "parent chain sets an empty scalar alone",
			chain: []manifest.MergedBlock{root("agent", "acme_hooks:\n"), overBase("agent", "acme_hooks: ~\n")},
			child: childOf("agent", "")},
	}
	for _, between := range []struct{ name, extra string }{
		{"undeclared key from the grandparent", ""},
		{"undeclared key from the grandparent, empty in between", "acme_hooks:\n"},
	} {
		cases = append(cases, execCase{
			name:  between.name,
			chain: []manifest.MergedBlock{hooks[0], overBase("agent", between.extra)},
			child: childOf("agent", ""),
			want:  []string{"acme_hooks"},
		})
	}
	return cases
}

// excludedCases covers the §4.6 fields outside the executable set, which give
// nothing when the parent sets them and the child omits them, and the §4.3.4
// keys whose standing in ARTIFACT.md D-1 of proposal 0057 settles.
//
// Spec: §4.6 executable fields across layers
func excludedCases() []execCase {
	var cases []execCase
	for _, f := range []struct{ typ, extra string }{
		{"hook", "hook_event: PreToolUse\n"},
		{"agent", "runtime_requirements:\n  python: \">=3.11\"\n"},
		{"agent", "sandbox_profile: network-isolated\n"},
		{"agent", "external_resources:\n  - path: data/x.bin\n    url: https://acme.com/x.bin\n"},
		{"agent", "audit_redact: [query]\n"},
		{"agent", "lint_suppress: [lint.skill_ref_validate]\n"},
		{"agent", "source: imported\n"},
	} {
		key, _, _ := strings.Cut(f.extra, ":")
		cases = append(cases, execCase{
			name:  "excluded " + key,
			chain: []manifest.MergedBlock{root(f.typ, f.extra)},
			child: childOf(f.typ, ""),
		})
	}

	// D-1 (proposal 0057): a key §4.3 defines for SKILL.md alone is an
	// undeclared key when it appears in ARTIFACT.md, so a command parent's
	// allowed-tools, which pre-approves tools in the materialized command, is
	// executable.
	tools := []manifest.MergedBlock{root("command", "allowed-tools: [Bash]\n")}
	cases = append(cases,
		execCase{name: "allowed-tools omitted", chain: tools, child: childOf("command", ""),
			want: []string{"allowed-tools"}},
		execCase{name: "allowed-tools restated", chain: tools, child: childOf("command", "allowed-tools: [Bash]\n")},
	)

	// D-1 (proposal 0057): name, description, and license are universal
	// ARTIFACT.md fields in a skill's ARTIFACT.md too, so they are never
	// undeclared keys.
	return append(cases, execCase{
		name:  "skill name, description, and license",
		chain: []manifest.MergedBlock{root("skill", "name: parent-skill\ndescription: d\nlicense: MIT\n")},
		child: childOf("skill", ""),
	})
}

// orderingCases covers the sorted, de-duplicated result and the same-ID
// overlay's own ID.
//
// Spec: §4.6 executable fields across layers. Paths are sorted byte-wise and
// de-duplicated: an undeclared key spelled as an mcpServers field path
// collapses into the path the missing entry produces. A same-ID overlay's own
// ID is not a chain parent (§4.6 collisions), so an undeclared key named with
// it is reported by name.
func orderingCases() []execCase {
	return []execCase{
		{name: "sorted and de-duplicated",
			chain: []manifest.MergedBlock{root("mcp-server", "server_identifier: npx run\n"+
				"mcpServers:\n  - name: wiki\n  - name: jira\n    command: run\n  - name: jira\n    args: [-v]\n"+
				"acme_hooks: x\nZeta: y\n\"mcpServers[jira]\": z\n")},
			child: childOf("mcp-server", ""),
			want: []string{
				"Zeta", "acme_hooks", "mcpServers[jira]", "mcpServers[wiki]", "server_identifier",
			}},
		{name: "same-ID overlay key", id: "shared/a",
			chain: []manifest.MergedBlock{root("agent", "shared/a: x\n")},
			child: doc("agent", "shared/a@1.x", ""),
			want:  []string{"shared/a"}},
	}
}

// Each table case gives exactly its field paths.
// Spec: §4.6 executable fields across layers
func TestInheritedExecutableFields_Table(t *testing.T) {
	t.Parallel()
	for _, tc := range execCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := inherited(t, tc)
			if err != nil {
				t.Fatalf("InheritedExecutableFields: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("fields = %q, want %q", got, tc.want)
			}
		})
	}
}

// Over the table's inputs, the result is empty exactly when the executable
// values of MergeExtends(parent, child) and of the restored undeclared keys
// equal the values the child authored.
// Spec: §4.6 executable fields across layers
func TestInheritedExecutableFields_EmptyExactlyWhenMergeKeepsChildValues(t *testing.T) {
	t.Parallel()
	for _, tc := range execCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := inherited(t, tc)
			if err != nil {
				t.Fatalf("InheritedExecutableFields: %v", err)
			}
			if kept := mergeKeepsChildValues(t, tc); kept != (len(got) == 0) {
				t.Errorf("merge keeps child values = %v, but fields = %q", kept, got)
			}
		})
	}
}

// inherited runs the comparison for one table case.
func inherited(t *testing.T, tc execCase) ([]string, error) {
	t.Helper()
	return manifest.InheritedExecutableFields(caseID(tc), resolveChain(t, tc.chain), tc.chain, []byte(tc.child))
}

// caseID is the child's canonical ID for a table case.
func caseID(tc execCase) string {
	if tc.id != "" {
		return tc.id
	}
	return childID
}

// mergeKeepsChildValues is the property's oracle. It reads the public merge
// and serialization surface rather than the comparison's helpers: the typed
// executable fields of the merged artifact, and the undeclared keys the served
// block restores, against the child's authored values.
func mergeKeepsChildValues(t *testing.T, tc execCase) bool {
	t.Helper()
	child, err := manifest.ParseArtifact([]byte(tc.child))
	if err != nil {
		t.Fatalf("ParseArtifact(child): %v", err)
	}
	merged := manifest.MergeExtends(resolveChain(t, tc.chain), *child)
	if merged.HookAction != child.HookAction || merged.ServerIdentifier != child.ServerIdentifier {
		return false
	}
	own := map[string]manifest.MCPServerRef{}
	for _, c := range manifest.MergeExtends(manifest.Artifact{}, *child).MCPServers {
		own[c.Name] = c
	}
	for _, m := range merged.MCPServers {
		c, ok := own[m.Name]
		if !ok || m.Transport != c.Transport || m.Command != c.Command ||
			!slices.Equal(m.Args, c.Args) || !maps.Equal(m.Env, c.Env) {
			return false
		}
	}
	return restoredKeysAreChildAuthored(t, tc, merged, child.Extends)
}

// restoredKeysAreChildAuthored reports whether every key the served block
// restores beyond the typed serialization carries the child's authored value.
// An absent key and an empty scalar compare equal, because §4.6 treats both as
// an omitted field.
func restoredKeysAreChildAuthored(t *testing.T, tc execCase, merged manifest.Artifact, extends string) bool {
	t.Helper()
	chain := append(slices.Clone(tc.chain), block(tc.child, extends))
	served, err := manifest.SerializeMerged(&merged, caseID(tc), chain...)
	if err != nil {
		t.Fatalf("SerializeMerged: %v", err)
	}
	merged.Extends, merged.Body = "", ""
	typed, err := manifest.SerializeArtifact(&merged)
	if err != nil {
		t.Fatalf("SerializeArtifact: %v", err)
	}
	childFM, _, err := manifest.SplitFrontmatter([]byte(tc.child))
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v", err)
	}
	declared, authored := decodeMapping(t, typed), decodeMapping(t, childFM)
	for key, value := range decodeMapping(t, served) {
		if _, ok := declared[key]; ok {
			continue
		}
		own := authored[key]
		if (isEmptyScalar(own) && isEmptyScalar(value)) || reflect.DeepEqual(own, value) {
			continue
		}
		return false
	}
	return true
}

// isEmptyScalar reports whether a decoded value is an empty scalar or absent.
func isEmptyScalar(v any) bool {
	return v == nil || v == ""
}

// A field path whose name component stands as a reference to a chain parent is
// withheld: the comparison fails with ErrUnreadableChain, names no field, and
// its message names neither parent.
// Spec: §4.6 executable fields across layers
func TestInheritedExecutableFields_DisclosingPathIsUnreadable(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ base, parent, child string }{
		"undeclared key named for the root": {
			base: "\"core/base\": x\n",
		},
		"mcpServers entry named for the root": {
			base: "mcpServers:\n  - name: core/base@1.x\n    command: run\n",
		},
		"env key named for the parent": {
			parent: "mcpServers:\n  - name: jira\n    env:\n      shared/a: v\n",
			child:  "mcpServers:\n  - name: jira\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			chain := []manifest.MergedBlock{root("agent", tc.base), overBase("agent", tc.parent)}
			got, err := manifest.InheritedExecutableFields("team/b", resolveChain(t, chain), chain,
				[]byte(doc("agent", "shared/a@1.x", tc.child)))
			assertUnreadable(t, err)
			if got != nil {
				t.Errorf("fields = %q, want none", got)
			}
			for _, id := range []string{"core/base", "shared/a"} {
				if strings.Contains(err.Error(), id) {
					t.Errorf("error %q names %s", err, id)
				}
			}
		})
	}
}

// A child or chain block that does not decode fails closed with
// ErrUnreadableChain rather than reporting no fields.
// Spec: §4.6 executable fields across layers
func TestInheritedExecutableFields_UndecodableBlocks(t *testing.T) {
	t.Parallel()
	good := childOf("agent", "")
	cases := map[string]struct {
		chain []manifest.MergedBlock
		child string
	}{
		"child without frontmatter":   {child: "no frontmatter\n"},
		"child not YAML":              {child: "---\ntype: [agent\n---\n"},
		"child header not a mapping":  {child: "---\n- a\n---\n"},
		"child header only a comment": {child: "---\n# nothing\n---\n"},
		"chain block not YAML": {
			chain: []manifest.MergedBlock{block("---\ntype: [agent\n---\n", "")},
			child: good,
		},
		"chain header not a mapping": {
			chain: []manifest.MergedBlock{block("---\n- a\n---\n", "")},
			child: good,
		},
		"chain header only a comment": {
			chain: []manifest.MergedBlock{block("---\n# nothing\n---\n", "")},
			child: good,
		},
		"chain executable field mistyped": {
			chain: []manifest.MergedBlock{block("---\ntype: hook\nversion: 1.0.0\nhook_action: [a]\n---\n", "")},
			child: good,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := manifest.InheritedExecutableFields(childID, manifest.Artifact{}, tc.chain, []byte(tc.child))
			assertUnreadable(t, err)
			if got != nil {
				t.Errorf("fields = %q, want none", got)
			}
		})
	}
}

// assertUnreadable checks the error contract callers rely on: the error matches
// ErrUnreadableChain, carries the package's operation prefix, and never reads
// as the read path's hidden-parent failure.
func assertUnreadable(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, manifest.ErrUnreadableChain) {
		t.Fatalf("err = %v, want ErrUnreadableChain", err)
	}
	if !strings.HasPrefix(err.Error(), "manifest: executable fields:") {
		t.Errorf("err = %q, want prefix %q", err, "manifest: executable fields:")
	}
	if errors.Is(err, manifest.ErrUnhidableParent) {
		t.Errorf("err wraps ErrUnhidableParent: %v", err)
	}
}
