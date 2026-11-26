package core

import (
	"context"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/store"
)

// bridgeCaller reports whether the request names the MCP bridge as its
// client (§7.2.2 name=mcp). A host that consumes Podium through the bridge
// fixes its MCP server list at startup and cannot connect to an mcp-server
// artifact mid-session, so the registry keeps mcp-server artifacts out of the
// bridge's search_artifacts, load_domain, and load_artifact.
//
// The filter removes planning noise and is not access control. The client
// name is self-reported, and a caller that sends name=mcp only loses the
// mcp-server artifacts from its own results; every one stays reachable
// through any other client name.
//
// Spec: §5, §7.2.2
func bridgeCaller(ctx context.Context) bool {
	name, _, _ := usageClientFrom(ctx)
	return name == audit.ClientNameMCP
}

// bridgeRefuses reports whether the bridge's load resolved to rec, an
// mcp-server record, which load_artifact refuses for the bridge. The check
// runs on the resolved record, so an explicit version pin to an earlier
// version of another type still loads.
//
// Spec: §5
func bridgeRefuses(ctx context.Context, rec store.ManifestRecord) bool {
	return rec.Type == string(manifest.TypeMCPServer) && bridgeCaller(ctx)
}

// bridgeView returns visible for the bridge's search and domain map: every
// record of an artifact whose latest version is an mcp-server is left out,
// and every other record stays. The type is judged on the latest version,
// the one the search and the domain map describe, so an artifact whose
// latest version is an mcp-server does not surface through an older version
// of another type. Any caller other than the bridge gets visible unchanged.
//
// Spec: §5, §7.2.2
func bridgeView(ctx context.Context, visible []store.ManifestRecord) []store.ManifestRecord {
	if !bridgeCaller(ctx) {
		return visible
	}
	hidden := map[string]bool{}
	for id, m := range latestByID(visible) {
		if m.Type == string(manifest.TypeMCPServer) {
			hidden[id] = true
		}
	}
	if len(hidden) == 0 {
		return visible
	}
	out := make([]store.ManifestRecord, 0, len(visible))
	for _, m := range visible {
		if !hidden[m.ArtifactID] {
			out = append(out, m)
		}
	}
	return out
}
