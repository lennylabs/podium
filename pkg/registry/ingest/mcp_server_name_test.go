package ingest_test

import (
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
)

// mcpDoc is the ARTIFACT.md of an mcp-server named name.
func mcpDoc(name string) string {
	return "---\ntype: mcp-server\nname: " + name + "\nversion: 1.0.0\ndescription: d\nserver_identifier: npx:@acme/db\n---\n\nbody\n"
}

// Spec: §4.1 (MCP server names), §7.3.1 — ingest refuses an mcp-server whose
// declared name holds a character outside ASCII letters, digits, '_', and
// '-', or is longer than 64 characters, with ingest.invalid_artifact and
// exactly the mcp_server_name constraint, and stores a valid one and an
// mcp-server that declares no name.
func TestIngest_MCPServerNameRule(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	long := ""
	for len(long) < 65 {
		long += "a"
	}
	res := erIngest(t, st, "team", map[string]string{
		"ops/quoted/ARTIFACT.md": mcpDoc(`'"db"'`),
		"ops/dotted/ARTIFACT.md": mcpDoc("db.env"),
		"ops/long/ARTIFACT.md":   mcpDoc(long),
		"ops/ok/ARTIFACT.md":     mcpDoc("finance-db2"),
		"ops/bare/ARTIFACT.md":   "---\ntype: mcp-server\nversion: 1.0.0\ndescription: d\nserver_identifier: npx:@acme/db\n---\n\nbody\n",
	})
	if res.Accepted != 2 || len(res.Rejected) != 3 {
		t.Fatalf("result = %+v, want two accepted and three refusals", res)
	}
	want := map[string]any{"constraint": layer.MCPServerNameConstraint}
	for _, id := range []string{"ops/quoted", "ops/dotted", "ops/long"} {
		rj := erRejection(t, res, id)
		if rj.Code != layer.InvalidArtifactCode || rj.Reason != layer.MCPServerNameReason || !reflect.DeepEqual(rj.Details, want) {
			t.Errorf("rejection of %s = %+v", id, rj)
		}
		erAssertNotStored(t, st, id, "1.0.0")
	}
}
