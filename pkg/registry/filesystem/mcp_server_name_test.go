package filesystem

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
)

// Spec: §4.1 (MCP server names), §13.11.3 — the walk drops an mcp-server
// whose declared name breaks the rule with an mcp_server_name drop, after the
// removed-type rule, and keeps a valid name and an undeclared one. With
// OnDrop nil the walk fails with the rule's reason.
func TestWalk_MCPServerNameDropped(t *testing.T) {
	t.Parallel()
	mcp := func(name string) string {
		return "---\ntype: mcp-server\n" + name + "version: 1.0.0\ndescription: d\nserver_identifier: npx:@acme/db\n---\n\nBody.\n"
	}
	files := map[string]string{
		"l1/bad/ARTIFACT.md":  mcp("name: \"a]\\n[mcp_servers.db.env]\"\n"),
		"l1/good/ARTIFACT.md": mcp("name: db\n"),
		"l1/bare/ARTIFACT.md": mcp(""),
	}
	reg := openLayeredTree(t, []string{"l1"}, files)
	got, drops, err := walkCollecting(reg, CollisionPolicyDrop, true)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []layer.Drop{layer.RemovedFeatureDrop("bad", layer.MCPServerNameConstraint, layer.MCPServerNameReason)}
	if !reflect.DeepEqual(drops, want) {
		t.Errorf("OnDrop calls = %+v, want %+v", drops, want)
	}
	if ids := idsOf(got); !reflect.DeepEqual(ids, []string{"bare", "good"}) {
		t.Errorf("kept = %v, want bare and good", ids)
	}

	single := openLayeredTree(t, []string{"l1"}, map[string]string{"l1/bad/ARTIFACT.md": files["l1/bad/ARTIFACT.md"]})
	if _, _, err := walkCollecting(single, CollisionPolicyDefault, false); err == nil ||
		!strings.Contains(err.Error(), layer.MCPServerNameReason) {
		t.Errorf("Walk without OnDrop err = %v, want the name rule's reason", err)
	}
}
