package main

import (
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// hiddenFromBridge reports whether the bridge keeps an artifact of type typ
// out of its results. A host that consumes Podium through the MCP bridge fixes
// its MCP server list at startup and cannot connect to an mcp-server artifact
// mid-session, so the bridge neither lists one nor loads one. The SDK and
// podium sync still serve them.
//
// Spec: §5
func hiddenFromBridge(typ string) bool {
	return typ == string(manifest.TypeMCPServer)
}

// bridgeNotFound is the load_artifact refusal for an artifact the bridge keeps
// out of its results. The bridge answers as the registry answers an ID that
// is not in the caller's view, so a hidden artifact and an absent one read the
// same to the host.
//
// Spec: §5, §6.10
func bridgeNotFound(id string) map[string]any {
	return errorResult("registry.not_found: " + id)
}

// bridgeRecords returns the workspace-overlay records the bridge lists,
// leaving out the types hiddenFromBridge names. A record whose manifest did
// not parse carries no type and is kept.
//
// Spec: §5
func bridgeRecords(records []filesystem.ArtifactRecord) []filesystem.ArtifactRecord {
	out := make([]filesystem.ArtifactRecord, 0, len(records))
	for _, r := range records {
		if r.Artifact != nil && hiddenFromBridge(string(r.Artifact.Type)) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// dropHiddenDescriptors removes the descriptors in the list under key of the
// decoded JSON object v whose "type" hiddenFromBridge names, and returns how
// many it removed. A value that is not an object, or a key that holds no list,
// is left unchanged.
//
// Spec: §5
func dropHiddenDescriptors(v any, key string) int {
	m, ok := v.(map[string]any)
	if !ok {
		return 0
	}
	list, ok := m[key].([]any)
	if !ok {
		return 0
	}
	kept := make([]any, 0, len(list))
	for _, el := range list {
		if d, ok := el.(map[string]any); ok {
			if typ, _ := d["type"].(string); hiddenFromBridge(typ) {
				continue
			}
		}
		kept = append(kept, el)
	}
	m[key] = kept
	return len(list) - len(kept)
}
