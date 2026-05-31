package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
)

// promptsListResult is the §5.2 promise: every `type: command`
// artifact in the caller's view that opted in via
// `expose_as_mcp_prompt: true` becomes one entry.
type promptsListResult struct {
	Prompts []promptDescriptor `json:"prompts"`
}

type promptDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// handlePromptsList answers the MCP `prompts/list` request. The
// MCP server walks the registry's command-type artifacts and
// keeps the ones whose frontmatter declares
// `expose_as_mcp_prompt: true`. Empty result when the deployment
// has no opt-ins.
func (s *mcpServer) handlePromptsList() any {
	descriptors, err := s.collectPromptDescriptors()
	if err != nil {
		return errorResult(err.Error())
	}
	return promptsListResult{Prompts: descriptors}
}

// handlePromptsGet answers MCP `prompts/get` by loading the named
// command artifact, confirming it opted into prompt projection,
// and returning the body as the prompt's user-message content.
func (s *mcpServer) handlePromptsGet(raw json.RawMessage) any {
	var args struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult(err.Error())
	}
	if args.Name == "" {
		return errorResult("prompts.invalid_argument: name is required")
	}
	body, err := s.fetchJSON("/v1/load_artifact", map[string]any{"id": args.Name})
	if err != nil {
		return errorResultFrom(err)
	}
	var resp loadArtifactResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("decode load_artifact: " + err.Error())
	}
	if resp.ID == "" {
		return errorResult("prompts.not_found: " + args.Name)
	}
	art, err := manifest.ParseArtifact([]byte(resp.Frontmatter))
	if err != nil {
		return errorResult("prompts.parse_failed: " + err.Error())
	}
	if art.Type != manifest.TypeCommand {
		return errorResult("prompts.not_a_command: " + args.Name)
	}
	if !art.ExposeAsMCPPrompt {
		return errorResult("prompts.not_exposed: " + args.Name)
	}
	description := art.Description
	if description == "" {
		description = art.Name
	}
	// MCP prompts/get response shape per the protocol: a
	// description plus an array of messages with role + content.
	return map[string]any{
		"description": description,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": map[string]any{
					"type": "text",
					"text": resp.ManifestBody,
				},
			},
		},
	}
}

// collectPromptDescriptors walks every `type: command` artifact in the
// caller's effective view and keeps the ones whose manifest declares
// `expose_as_mcp_prompt: true`. The MCP server consults the filtered
// list when answering `prompts/list`.
//
// Enumeration goes through /v1/sync/manifest (the registry's full
// effective view, which carries no top-K cap) rather than
// search_artifacts: search_artifacts rejects top_k > 50 (§5) and offers
// no pagination, so a deployment with more than 50 command artifacts
// would silently drop every opt-in whose ID sorts past the 50th. The
// effective view enumerates the whole catalog in one pass, so the
// projected prompt list is complete regardless of catalog size
// (F-5.2.1).
func (s *mcpServer) collectPromptDescriptors() ([]promptDescriptor, error) {
	body, err := s.fetchJSON("/v1/sync/manifest", nil)
	if err != nil {
		return nil, err
	}
	var view struct {
		Artifacts []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		return nil, fmt.Errorf("decode sync/manifest: %w", err)
	}
	out := []promptDescriptor{}
	for _, a := range view.Artifacts {
		if a.ID == "" || a.Type != string(manifest.TypeCommand) {
			continue
		}
		body, err := s.fetchJSON("/v1/load_artifact", map[string]any{"id": a.ID})
		if err != nil {
			continue
		}
		var resp loadArtifactResponse
		if err := json.Unmarshal(body, &resp); err != nil || resp.ID == "" {
			continue
		}
		art, err := manifest.ParseArtifact([]byte(resp.Frontmatter))
		if err != nil || !art.ExposeAsMCPPrompt {
			continue
		}
		out = append(out, promptDescriptor{Name: a.ID, Description: strings.TrimSpace(art.Description)})
	}
	return out, nil
}

// promptsCapabilityActive reports whether the bridge should announce the
// `prompts` MCP capability. §5 advertises `prompts` conditional on the
// presence of at least one `type: command` artifact that opted into
// projection, so the check enumerates the effective view and reports
// whether any opt-in exists. A registry-fetch failure fails open
// (returns true) so a transient error never hides a present capability;
// the client can still call prompts/list to enumerate (F-5.2.2).
func (s *mcpServer) promptsCapabilityActive() bool {
	descriptors, err := s.collectPromptDescriptors()
	if err != nil {
		return true
	}
	return len(descriptors) > 0
}

// errorResultWithStatus is reserved for callers that want to
// surface non-200 HTTP status alongside the error message.
func errorResultWithStatus(msg string, status int) any {
	if status == http.StatusOK {
		return errorResult(msg)
	}
	return errorResult(fmt.Sprintf("%s (HTTP %d)", msg, status))
}
