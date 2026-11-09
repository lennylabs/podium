package manifest_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
)

// execParent is an mcp-server parent that sets every kind of executable
// field the comparison reports: server_identifier, an mcpServers entry with
// each member set, and an undeclared key.
const execParent = "---\ntype: mcp-server\nversion: 1.0.0\ndescription: parent\n" +
	"server_identifier: npx parent-server\n" +
	"mcpServers:\n  - name: tools\n    transport: stdio\n    command: run-tools\n" +
	"    args: [--fast]\n    env:\n      TOKEN: abc\n      REGION: eu\n" +
	"x_launcher: parent-launcher\nx_blank: \"\"\n---\n\nparent body\n"

// parentArtifact parses src as the chain-resolved parent a caller passes.
func parentArtifact(t *testing.T, src string) manifest.Artifact {
	t.Helper()
	a, err := manifest.ParseArtifact([]byte(src))
	if err != nil {
		t.Fatalf("ParseArtifact: %v", err)
	}
	return *a
}

// A child that omits or partly restates the parent's executable fields gets
// one path per unrestated member, sorted, and an empty parent scalar sets
// nothing.
// Spec: §4.6
func TestInheritedExecutableFields_ReportsUnrestatedFields(t *testing.T) {
	t.Parallel()
	child := "---\ntype: mcp-server\nversion: 2.0.0\nextends: shared/parent@1.x\n" +
		"mcpServers:\n  - name: tools\n    transport: stdio\n    env:\n      TOKEN: xyz\n" +
		"---\n\nchild body\n"

	got, err := manifest.InheritedExecutableFields(childID, parentArtifact(t, execParent),
		[]manifest.MergedBlock{block(execParent, "")}, []byte(child))
	if err != nil {
		t.Fatalf("InheritedExecutableFields: %v", err)
	}
	want := []string{
		"mcpServers[tools].args",
		"mcpServers[tools].command",
		"mcpServers[tools].env.REGION",
		"server_identifier",
		"x_launcher",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %q, want %q", got, want)
	}
}

// A child that restates every executable field, with its own values, leaves
// nothing to the parent.
// Spec: §4.6
func TestInheritedExecutableFields_FullRestateReportsNothing(t *testing.T) {
	t.Parallel()
	child := "---\ntype: mcp-server\nversion: 2.0.0\nextends: shared/parent@1.x\n" +
		"server_identifier: npx child-server\n" +
		"mcpServers:\n  - name: tools\n    transport: stdio\n    command: run-tools\n" +
		"    args: [--slow]\n    env:\n      TOKEN: xyz\n      REGION: us\n" +
		"x_launcher: child-launcher\n---\n\nchild body\n"

	got, err := manifest.InheritedExecutableFields(childID, parentArtifact(t, execParent),
		[]manifest.MergedBlock{block(execParent, "")}, []byte(child))
	if err != nil {
		t.Fatalf("InheritedExecutableFields: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("fields = %q, want none", got)
	}
}

// A parent entry the child's list does not carry is reported as the whole
// entry.
// Spec: §4.6
func TestInheritedExecutableFields_MissingServerEntry(t *testing.T) {
	t.Parallel()
	child := "---\ntype: mcp-server\nversion: 2.0.0\nextends: shared/parent@1.x\n" +
		"server_identifier: npx child-server\nx_launcher: child-launcher\n---\n"

	got, err := manifest.InheritedExecutableFields(childID, parentArtifact(t, execParent),
		[]manifest.MergedBlock{block(execParent, "")}, []byte(child))
	if err != nil {
		t.Fatalf("InheritedExecutableFields: %v", err)
	}
	if want := []string{"mcpServers[tools]"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %q, want %q", got, want)
	}
}

// A field path whose name component stands as a reference to a chain parent
// is withheld: the comparison fails with ErrUnreadableChain and names no
// field, and the error does not read as the hidden-parent read failure.
// Spec: §4.6
func TestInheritedExecutableFields_DisclosingPathIsUnreadable(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\n" +
		"mcpServers:\n  - name: shared/root\n    command: run\n---\n"
	child := "---\ntype: agent\nversion: 2.0.0\nextends: shared/parent@1.x\n---\n"

	got, err := manifest.InheritedExecutableFields(childID, parentArtifact(t, parent),
		[]manifest.MergedBlock{block(parent, "shared/root@1.x")}, []byte(child))
	if !errors.Is(err, manifest.ErrUnreadableChain) {
		t.Fatalf("err = %v, want ErrUnreadableChain", err)
	}
	if errors.Is(err, manifest.ErrUnhidableParent) {
		t.Errorf("err wraps ErrUnhidableParent: %v", err)
	}
	if got != nil {
		t.Errorf("fields = %q, want none", got)
	}
}

// A child or chain block that does not decode fails closed with
// ErrUnreadableChain rather than reporting no fields.
// Spec: §4.6
func TestInheritedExecutableFields_UndecodableBlocks(t *testing.T) {
	t.Parallel()
	good := "---\ntype: agent\nversion: 2.0.0\nextends: shared/parent@1.x\n---\n"
	cases := map[string]struct {
		chain []manifest.MergedBlock
		child string
	}{
		"child without frontmatter":   {child: "no frontmatter\n"},
		"child header not a mapping":  {child: "---\n- a\n---\n"},
		"child header only a comment": {child: "---\n# nothing\n---\n"},
		"chain header not a mapping": {
			chain: []manifest.MergedBlock{block("---\n- a\n---\n", "")},
			child: good,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := manifest.InheritedExecutableFields(childID, manifest.Artifact{}, tc.chain, []byte(tc.child))
			if !errors.Is(err, manifest.ErrUnreadableChain) {
				t.Fatalf("err = %v, want ErrUnreadableChain", err)
			}
			if errors.Is(err, manifest.ErrUnhidableParent) {
				t.Errorf("err wraps ErrUnhidableParent: %v", err)
			}
		})
	}
}
