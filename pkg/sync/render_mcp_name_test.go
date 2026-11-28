package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/spi"
)

// Spec: §4.1 (MCP server names), §7.8 — a render fails its target on an
// mcp-server whose declared name breaks the rule, such as a stored row a
// registry ingested before the rule, with materialize.untranslatable, and
// renders a valid name.
func TestRenderEmitter_RefusesMCPServerName(t *testing.T) {
	t.Parallel()
	doc := func(name string) []byte {
		return []byte("---\ntype: mcp-server\nname: \"" + name + "\"\nversion: 1.0.0\ndescription: d\nserver_identifier: npx:@acme/db\n---\n\nbody\n")
	}
	record := func(name string) []assigned {
		art, err := manifest.ParseArtifact(doc(name))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		return []assigned{{record: Record{ID: "ops/db", Artifact: art, ArtifactBytes: doc(name)}, plugin: PluginFilter{Name: "ops"}}}
	}
	_, err := renderEmitter(context.Background(), adapter.CodexMarketplace{}, record(`db\n[x]`), "acme")
	var se *spi.Error
	if !errors.As(err, &se) || se.Code != "materialize.untranslatable" {
		t.Fatalf("render err = %v, want materialize.untranslatable", err)
	}
	files, err := renderEmitter(context.Background(), adapter.CodexMarketplace{}, record("db"), "acme")
	if err != nil || len(files) == 0 {
		t.Fatalf("render of a valid name = %v, %v", files, err)
	}
}
