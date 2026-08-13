package adapter

import (
	"context"
	"encoding/json"
	"testing"
)

// Spec: §7.8 / issue #58 — the Claude Code marketplace schema requires
// `name`, `owner`, and `plugins` at the root, with `owner.name` set; Claude
// Desktop refuses to import a marketplace manifest without them. The
// fragment carries owner.name so every merged marketplace.json validates.
func TestMarketplaceFragment_CarriesRequiredOwner(t *testing.T) {
	t.Parallel()
	out, err := ClaudeMarketplace{}.Manifest(context.Background(), "acme-agents", finPlugin("claude"))
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	mkt := fileByPath(t, out, ".claude-plugin/marketplace.json")

	var m map[string]any
	if err := json.Unmarshal(mkt.Content, &m); err != nil {
		t.Fatalf("fragment is not valid JSON: %v\n%s", err, mkt.Content)
	}
	for _, key := range []string{"name", "owner", "plugins"} {
		if _, ok := m[key]; !ok {
			t.Errorf("fragment missing schema-required root key %q", key)
		}
	}
	owner, ok := m["owner"].(map[string]any)
	if !ok {
		t.Fatalf("owner is %T, want an object", m["owner"])
	}
	if name, _ := owner["name"].(string); name != "acme-agents" {
		t.Errorf("owner.name = %q, want the marketplace name", name)
	}
}
