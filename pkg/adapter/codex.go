package adapter

import (
	"context"
	"path"
)

// Codex is the adapter for OpenAI Codex (§6.7). Skills live under .agents/
// (not .codex/), and a user-invoked skill carries agents/openai.yaml with
// policy.allow_implicit_invocation: false. Subagents are TOML, rules inject
// into AGENTS.md, and hook and mcp-server merge into .codex/config.toml (the
// `hooks` and `mcp_servers` tables).
type Codex struct{}

// ID returns "codex".
func (Codex) ID() string { return "codex" }

// Adapt routes each type to its Codex-native location.
func (Codex) Adapt(ctx context.Context, src Source) ([]File, error) {
	name := lastSeg(src.ArtifactID)
	switch frontmatterType(src.ArtifactBytes) {
	case "skill":
		return skillOut(path.Join(".agents", "skills", name), src, controlOpenAIYAML), nil
	case "agent":
		return singleFileOut(path.Join(".codex", "agents", name+".toml"), codexAgentTOML(src, name), src), nil
	case "rule":
		return []File{injectRule("AGENTS.md", src)}, nil
	case "hook":
		return codexHookOut(src), nil
	case "mcp-server":
		return []File{{Path: ".codex/config.toml", Op: OpInject, Key: src.ArtifactID, Content: codexMCPTOML(src)}}, nil
	}
	return nil, nil
}
