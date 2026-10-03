package main

import (
	"encoding/json"
	"strings"
)

// resourceURIPrefix is the scheme + path the bridge uses to address an
// artifact through MCP's resource protocol. The artifact's canonical ID
// (which may itself contain slashes, e.g. "finance/close/checklist")
// follows the prefix verbatim.
const resourceURIPrefix = "podium://artifact/"

// resourceMimeType labels every mirrored artifact body. Artifact
// manifests are Markdown with YAML frontmatter.
const resourceMimeType = "text/markdown"

// resourcesListResult is the §5.0 read-only mirror enumeration: one
// resource per artifact in the caller's effective view.
type resourcesListResult struct {
	Resources []resourceDescriptor `json:"resources"`
}

type resourceDescriptor struct {
	URI      string `json:"uri"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
}

// handleResourcesList answers MCP `resources/list`. It enumerates the
// caller's full effective view through the registry's no-top-K
// /v1/sync/manifest endpoint and maps each artifact to a resource URI.
// `mcp-server` artifacts are filtered out for the same reason they are
// filtered from the bridge's tool results (§5): a bridge host fixes its
// MCP server list at startup and cannot connect to one discovered
// mid-session. The mirror is read-only and never materializes.
func (s *mcpServer) handleResourcesList() any {
	body, err := s.fetchJSON("/v1/sync/manifest", nil)
	if err != nil {
		return errorResultFrom(err)
	}
	var view struct {
		Artifacts []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		return errorResult("decode sync/manifest: " + err.Error())
	}
	out := []resourceDescriptor{}
	for _, a := range view.Artifacts {
		if a.ID == "" || a.Type == "mcp-server" {
			continue
		}
		out = append(out, resourceDescriptor{
			URI:      resourceURIPrefix + a.ID,
			Name:     a.ID,
			MimeType: resourceMimeType,
		})
	}
	return resourcesListResult{Resources: out}
}

// handleResourcesRead answers MCP `resources/read`. It resolves the
// requested URI back to an artifact ID, loads the artifact body from the
// same /v1/load_artifact path the canonical `load_artifact` tool uses,
// and returns the manifest (frontmatter + body) as the resource content.
// This is the read-only half of the §5.0 mirror. It runs the same §6.6
// reconstitution and verification load_artifact runs, through
// verifyServedArtifact, and the §6.5 freshness check, and performs none of
// load_artifact's other effects: it emits no read event, caches nothing,
// advances no revision mark, runs neither §4.4.1 gate, and materializes
// nothing.
//
// Spec: §5.0, §6.6 step 2, §4.7.9, §6.5
func (s *mcpServer) handleResourcesRead(raw json.RawMessage) any {
	var args struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult(err.Error())
	}
	id, ok := artifactIDFromResourceURI(args.URI)
	if !ok {
		return errorResult("resources.invalid_argument: uri must be " + resourceURIPrefix + "<id>")
	}
	loadArgs := map[string]any{"id": id}
	body, err := s.fetchJSON("/v1/load_artifact", loadArgs)
	if err != nil {
		return errorResultFrom(err)
	}
	var resp loadArtifactResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("decode load_artifact: " + err.Error())
	}
	if resp.ID == "" {
		return errorResult("resources.not_found: " + id)
	}
	// §5.0: the mirror is a read-only mirror of load_artifact, so it returns
	// no text load_artifact would refuse. The refreshers are the ones the
	// live load_artifact path passes, built from the same arguments.
	if err := s.verifyServedArtifact(&resp, deliverOpts{
		refresh:         s.largeResourceRefresher(loadArgs),
		manifestRefresh: s.manifestBodyRefresher(loadArgs),
	}); err != nil {
		return errorResult(err.Error())
	}
	// §6.5: the mirror always requests `latest`, so its answer is compared
	// like a fresh load_artifact answer. A pass records the session reference
	// the registry's pin implies and never advances the revision mark.
	session := s.effectiveSessionID(loadArgs)
	rev, env := s.checkFreshness(resp, session)
	if env != nil {
		return env
	}
	s.resolutions.NoteSession(s.markKey(resp.ID), session, rev)
	return map[string]any{
		"contents": []map[string]any{
			{
				"uri":      args.URI,
				"mimeType": resourceMimeType,
				"text":     resp.Frontmatter + resp.ManifestBody,
			},
		},
	}
}

// artifactIDFromResourceURI strips the podium artifact-resource scheme
// and returns the canonical artifact ID. It reports false for any URI
// that does not carry the prefix or whose ID portion is empty.
func artifactIDFromResourceURI(uri string) (string, bool) {
	if !strings.HasPrefix(uri, resourceURIPrefix) {
		return "", false
	}
	id := strings.TrimPrefix(uri, resourceURIPrefix)
	if id == "" {
		return "", false
	}
	return id, true
}
