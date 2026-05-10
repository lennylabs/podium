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
		return errorResult(err.Error())
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

// collectPromptDescriptors walks the registry's command-type
// artifacts and keeps the ones whose manifest declares
// `expose_as_mcp_prompt: true`. The MCP server consults the
// filtered list when answering `prompts/list`.
func (s *mcpServer) collectPromptDescriptors() ([]promptDescriptor, error) {
	body, err := s.fetchJSON("/v1/search_artifacts", map[string]any{
		"type":  "command",
		"top_k": 50,
	})
	if err != nil {
		return nil, err
	}
	var search struct {
		Results []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &search); err != nil {
		return nil, fmt.Errorf("decode search_artifacts: %w", err)
	}
	out := []promptDescriptor{}
	for _, r := range search.Results {
		body, err := s.fetchJSON("/v1/load_artifact", map[string]any{"id": r.ID})
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
		desc := strings.TrimSpace(r.Description)
		if desc == "" {
			desc = strings.TrimSpace(art.Description)
		}
		out = append(out, promptDescriptor{Name: r.ID, Description: desc})
	}
	return out, nil
}

// promptsCapabilityActive reports whether the deployment should
// announce the `prompts` MCP capability. Empty-listed deployments
// can still serve future opt-ins, so we always return true once
// the MCP server is configured (matching §5.2's "conditional on
// prompt artifacts" wording loosely — clients send prompts/list
// to enumerate at runtime regardless).
func (s *mcpServer) promptsCapabilityActive() bool { return true }

// errorResultWithStatus is reserved for callers that want to
// surface non-200 HTTP status alongside the error message.
func errorResultWithStatus(msg string, status int) any {
	if status == http.StatusOK {
		return errorResult(msg)
	}
	return errorResult(fmt.Sprintf("%s (HTTP %d)", msg, status))
}
