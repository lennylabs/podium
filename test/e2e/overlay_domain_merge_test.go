package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// End-to-end coverage for the §4.5.4 / §6.4 client-side overlay DOMAIN.md merge
// in load_domain, driven through the real podium-mcp bridge against a real
// standalone registry server. The bridge subprocess is
// bounded and torn down by mcpExec.

// writeOverlayFile writes one file into a workspace overlay tree.
func writeOverlayFile(t *testing.T, overlay, rel, content string) {
	t.Helper()
	p := filepath.Join(overlay, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// loadDomainResult drives load_domain through the bridge and returns the
// decoded tool result for path.
func loadDomainResult(t *testing.T, reg, overlay, path string) map[string]any {
	t.Helper()
	res := mcpExec(t, chMCPEnv(t, reg, "PODIUM_HARNESS=none", "PODIUM_OVERLAY_PATH="+overlay),
		toolCall(1, "load_domain", map[string]any{"path": path}))
	return rpcResult(t, res.Stdout, 1)
}

func notableHasID(result map[string]any, id string) bool {
	notable, _ := result["notable"].([]any)
	for _, n := range notable {
		if m, ok := n.(map[string]any); ok {
			if got, _ := m["id"].(string); got == id {
				return true
			}
		}
	}
	return false
}

func subdomainHasPath(result map[string]any, path string) bool {
	subs, _ := result["subdomains"].([]any)
	for _, s := range subs {
		if m, ok := s.(map[string]any); ok {
			if got, _ := m["path"].(string); got == path {
				return true
			}
		}
	}
	return false
}

// spec: §4.5.4 / §6.4 — a workspace-overlay DOMAIN.md
// description overrides the registry's for the requested domain, and an overlay
// artifact that is a direct child of the domain joins the notable list.
func TestOverlayDomainMerge_DescriptionAndDirectChild(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		// Establishes the finance and finance/ap domains registry-side.
		"finance/ap/pay/ARTIFACT.md": contextArtifact("pay"),
		"finance/DOMAIN.md":          "---\ndescription: Registry finance\n---\n\nRegistry finance body.\n",
	})
	overlay := t.TempDir()
	writeOverlayFile(t, overlay, "finance/DOMAIN.md",
		"---\ndescription: Local finance overlay\n---\n\nLocal working notes for finance.\n")
	writeOverlayFile(t, overlay, "finance/draft-helper/ARTIFACT.md",
		"---\ntype: context\nversion: 0.1.0\ndescription: in-progress finance helper\nsensitivity: low\n---\n\ndraft body\n")

	result := loadDomainResult(t, reg, overlay, "finance")

	if desc, _ := result["description"].(string); desc != "Local working notes for finance." {
		t.Errorf("description = %q, want the overlay body to win", desc)
	}
	if !notableHasID(result, "finance/draft-helper") {
		t.Errorf("overlay direct-child artifact missing from notable: %v", result["notable"])
	}
	// The registry subdomain is still enumerated.
	if !subdomainHasPath(result, "finance/ap") {
		t.Errorf("registry subdomain finance/ap dropped: %v", result["subdomains"])
	}
}

// spec: §4.5.3 / §4.5.5 — an overlay artifact below an
// immediate child introduces that child as a subdomain, and an overlay
// DOMAIN.md with unlisted: true removes a registry subdomain from enumeration.
func TestOverlayDomainMerge_NewSubdomainAndUnlisted(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/ap/pay/ARTIFACT.md":     contextArtifact("pay"),
		"finance/secret/key/ARTIFACT.md": contextArtifact("key"),
	})
	overlay := t.TempDir()
	// A draft below a brand-new child folder introduces finance/newteam.
	writeOverlayFile(t, overlay, "finance/newteam/draft/ARTIFACT.md",
		"---\ntype: context\nversion: 0.1.0\ndescription: new team draft\nsensitivity: low\n---\n\nbody\n")
	writeOverlayFile(t, overlay, "finance/newteam/DOMAIN.md",
		"---\ndescription: New team workspace\n---\n")
	// The overlay unlists the registry's finance/secret subtree.
	writeOverlayFile(t, overlay, "finance/secret/DOMAIN.md", "---\nunlisted: true\n---\n")

	result := loadDomainResult(t, reg, overlay, "finance")

	if !subdomainHasPath(result, "finance/newteam") {
		t.Errorf("overlay-introduced subdomain finance/newteam missing: %v", result["subdomains"])
	}
	if subdomainHasPath(result, "finance/secret") {
		t.Errorf("overlay-unlisted subdomain finance/secret not pruned: %v", result["subdomains"])
	}
	if !subdomainHasPath(result, "finance/ap") {
		t.Errorf("registry subdomain finance/ap dropped: %v", result["subdomains"])
	}
}
