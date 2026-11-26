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
// out of its results. Its message has the format of the registry's answer for
// an ID that is not in the caller's view ("registry.not_found: artifact <id>",
// pkg/registry/core), so a hidden artifact and an absent one read the same to
// the host.
//
// Spec: §5, §6.10
func bridgeNotFound(id string) map[string]any {
	return errorResult("registry.not_found: artifact " + id)
}

// registryMaxTopK is the largest top_k the registry accepts for
// search_artifacts. The bridge requests this many results so that dropping
// the mcp-server results still fills the caller's page.
//
// Spec: §5
const registryMaxTopK = 50

// searchWindow returns the arguments the bridge sends to the registry's
// search_artifacts and the page size it returns to the caller. A caller top_k
// of zero or less is the registry default of 10. A top_k within the registry
// maximum is raised to the maximum, so the registry returns the most results
// it can and the bridge trims them to the page after dropping the mcp-server
// results. A top_k above the maximum passes through unchanged, and the
// registry refuses it.
//
// Spec: §5
func searchWindow(args map[string]any) (map[string]any, int) {
	page := topKArg(args)
	if page <= 0 {
		page = 10
	}
	if page > registryMaxTopK {
		return args, page
	}
	wide := make(map[string]any, len(args)+1)
	for k, v := range args {
		wide[k] = v
	}
	wide["top_k"] = registryMaxTopK
	return wide, page
}

// trimDescriptors cuts the list under key of the decoded JSON object v to at
// most n elements. A value that is not an object, or a key that holds no list,
// is left unchanged.
func trimDescriptors(v any, key string, n int) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	if list, ok := m[key].([]any); ok && len(list) > n {
		m[key] = list[:n]
	}
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
