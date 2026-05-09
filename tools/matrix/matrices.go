package main

// Matrix is one documented spec matrix the auditor knows about.
type Matrix struct {
	// ID is the section number (e.g., "§6.7.1").
	ID string
	// Title is a one-line description.
	Title string
	// Phase is the build phase under which cells of this matrix are
	// expected to be tested.
	Phase int
	// StubPrefix is the prefix used by `matrix scaffold` for generated
	// test function names.
	StubPrefix string
	// Axes is the list of axes the matrix spans. Cells = cartesian
	// product of every axis.
	Axes [][]string
}

// Cells returns the cartesian product across the matrix's axes.
func (m Matrix) Cells() [][]string {
	if len(m.Axes) == 0 {
		return nil
	}
	out := [][]string{{}}
	for _, axis := range m.Axes {
		next := make([][]string, 0, len(out)*len(axis))
		for _, prefix := range out {
			for _, value := range axis {
				cell := make([]string, len(prefix)+1)
				copy(cell, prefix)
				cell[len(prefix)] = value
				next = append(next, cell)
			}
		}
		out = next
	}
	return out
}

// KnownMatrices returns every spec matrix the auditor checks coverage
// for. Each entry is hardcoded against the spec's Markdown content;
// updates to the spec require updates here.
func KnownMatrices() []Matrix {
	return []Matrix{
		{
			ID:         "§6.7.1",
			Title:      "Capability matrix: per-(field, harness) adapter coverage",
			Phase:      13,
			StubPrefix: "Adapter_Capability",
			Axes: [][]string{
				// Axis 1: harness adapter values (§6.7).
				{
					"claude-code", "claude-desktop", "claude-cowork",
					"cursor", "codex", "opencode",
					"gemini", "pi", "hermes",
				},
				// Axis 2: matrix fields (§6.7.1 capability rows).
				{
					"description",
					"mcpServers",
					"delegates_to",
					"requiresApproval",
					"sandbox_profile",
					"expose_as_mcp_prompt",
					"rule_mode_always",
					"rule_mode_glob",
					"rule_mode_auto",
					"rule_mode_explicit",
					"hook_event",
				},
			},
		},
		{
			ID:         "§6.10",
			Title:      "Error codes: every namespaced code has an envelope test",
			Phase:      2,
			StubPrefix: "ErrorEnvelope",
			Axes: [][]string{
				{
					"auth.untrusted_runtime",
					"auth.token_expired",
					"auth.forbidden",
					"auth.device_code_pending",
					"config.invalid",
					"config.no_registry",
					"config.unknown_harness",
					"config.layer_path_ambiguous",
					"config.public_mode_with_idp",
					"config.read_only",
					"ingest.lint_failed",
					"ingest.immutable_violation",
					"ingest.frozen",
					"ingest.public_mode_rejects_sensitive",
					"ingest.webhook_invalid",
					"ingest.source_unreachable",
					"ingest.collision",
					"materialize.signature_invalid",
					"materialize.signature_missing",
					"materialize.runtime_unavailable",
					"materialize.sandbox_violation",
					"quota.storage_exceeded",
					"mcp.unsupported_version",
					"network.registry_unreachable",
					"registry.unavailable",
					"registry.invalid_argument",
					"registry.not_found",
					"registry.read_only",
					"domain.not_found",
				},
			},
		},
		{
			ID:         "§4.6",
			Title:      "Visibility unions: every subset of {public, organization, groups, users}",
			Phase:      7,
			StubPrefix: "Visibility_Union",
			Axes: [][]string{
				// 15 non-empty subsets enumerated explicitly so the
				// scaffolded names read clearly.
				{
					"public",
					"organization",
					"groups",
					"users",
					"public_organization",
					"public_groups",
					"public_users",
					"organization_groups",
					"organization_users",
					"groups_users",
					"public_organization_groups",
					"public_organization_users",
					"public_groups_users",
					"organization_groups_users",
					"public_organization_groups_users",
				},
			},
		},
		{
			ID:         "§4.3.5",
			Title:      "Canonical hook events × claude-code adapter (✓ cells only)",
			Phase:      13,
			StubPrefix: "Hook_Event_ClaudeCode",
			Axes: [][]string{
				{
					"session_start", "session_end",
					"user_prompt_submit",
					"pre_tool_use", "post_tool_use", "post_tool_use_failure",
					"pre_shell_execution", "post_shell_execution",
					"pre_mcp_execution", "post_mcp_execution",
					"pre_read_file", "post_file_edit",
					"permission_request", "permission_denied",
					"subagent_start", "subagent_stop",
					"stop", "pre_compact", "post_compact",
					"notification",
				},
			},
		},
		{
			ID:         "§4.3",
			Title:      "rule_mode × first-class harness adapter",
			Phase:      13,
			StubPrefix: "RuleMode",
			Axes: [][]string{
				{"always", "glob", "auto", "explicit"},
				{
					"claude-code", "claude-desktop", "claude-cowork",
					"cursor", "codex", "opencode",
					"gemini", "pi", "hermes",
				},
			},
		},
	}
}
