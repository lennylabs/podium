package core

import (
	"context"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/store"
)

// bridgeView returns visible without its mcp-server manifests when the
// request names the MCP bridge as its client (§7.2.2 name=mcp), and the IDs of
// the artifacts it left out. A host that consumes Podium through the bridge
// fixes its MCP server list at startup and cannot connect to an mcp-server
// artifact mid-session, so the registry leaves them out of the bridge's
// search_artifacts, load_domain, and load_artifact before it ranks, renders,
// resolves, audits, or counts anything. Every other caller gets visible
// unchanged.
//
// The filter removes planning noise and is not access control. The client
// name is self-reported, and a caller that sends name=mcp only loses the
// mcp-server artifacts from its own results; every one stays reachable
// through any other client name.
//
// Spec: §5, §7.2.2
func bridgeView(ctx context.Context, visible []store.ManifestRecord) ([]store.ManifestRecord, map[string]bool) {
	if name, _, _ := usageClientFrom(ctx); name != audit.ClientNameMCP {
		return visible, nil
	}
	var hidden map[string]bool
	out := make([]store.ManifestRecord, 0, len(visible))
	for _, m := range visible {
		if m.Type == string(manifest.TypeMCPServer) {
			if hidden == nil {
				hidden = map[string]bool{}
			}
			hidden[m.ArtifactID] = true
			continue
		}
		out = append(out, m)
	}
	return out, hidden
}
