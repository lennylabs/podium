package adapter

import (
	"context"
	"encoding/json"
	"testing"
)

// Spec: §7.8 / issue #58 — the Claude and Cursor marketplace schemas both
// require `name`, `owner`, and `plugins` at the root, with `owner.name` set;
// Claude Desktop refuses to import a marketplace manifest without them. The
// Codex format documents `name`, `interface`, and `plugins` at the root and no
// `owner`, so its fragment omits the key rather than adding one its schema does
// not describe.
func TestMarketplaceFragment_OwnerPerHarnessSchema(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		emitter   MarketplaceEmitter
		prefix    string
		manifest  string
		wantOwner bool
	}{
		{"claude", ClaudeMarketplace{}, "claude", ".claude-plugin/marketplace.json", true},
		{"cursor", CursorMarketplace{}, "cursor", ".cursor-plugin/marketplace.json", true},
		{"codex", CodexMarketplace{}, "codex", ".agents/plugins/marketplace.json", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := tc.emitter.Manifest(context.Background(), "acme-agents", finPlugin(tc.prefix))
			if err != nil {
				t.Fatalf("Manifest: %v", err)
			}
			frag := fileByPath(t, out, tc.manifest)

			var m map[string]any
			if err := json.Unmarshal(frag.Content, &m); err != nil {
				t.Fatalf("fragment is not valid JSON: %v\n%s", err, frag.Content)
			}
			for _, key := range []string{"name", "plugins"} {
				if _, ok := m[key]; !ok {
					t.Errorf("fragment missing required root key %q", key)
				}
			}

			owner, present := m["owner"]
			if !tc.wantOwner {
				if present {
					t.Errorf("%s fragment must not carry a root owner (undocumented in its format), got %v", tc.name, owner)
				}
				return
			}
			obj, ok := owner.(map[string]any)
			if !ok {
				t.Fatalf("owner is %T, want an object carrying the schema-required name", owner)
			}
			if name, _ := obj["name"].(string); name != "acme-agents" {
				t.Errorf("owner.name = %q, want the marketplace name acme-agents", name)
			}
		})
	}
}
