// Package manifest defines the canonical artifact and domain manifest types
// per spec §4.3 (Artifact Manifest Schema), §4.3.4 (SKILL.md compliance),
// and §4.5.1 (DOMAIN.md). It also exposes parsers that convert markdown +
// YAML-frontmatter source files into typed values.
//
// Stubs in this file define the surface that the rest of the shared
// library and the binaries consume. Tests in this package describe the
// behavior. Implementations land as the active phase advances.
package manifest

// ArtifactType is the canonical type discriminator. The first-class types
// are skill, agent, context, command, rule, hook, and mcp-server (per §4.1).
// Extension types register through the TypeProvider SPI; they appear in
// this enum as plain strings without a constant.
type ArtifactType string

// First-class artifact type identifiers (spec §4.1).
const (
	TypeSkill     ArtifactType = "skill"
	TypeAgent     ArtifactType = "agent"
	TypeContext   ArtifactType = "context"
	TypeCommand   ArtifactType = "command"
	TypeRule      ArtifactType = "rule"
	TypeHook      ArtifactType = "hook"
	TypeMCPServer ArtifactType = "mcp-server"
)

// Sensitivity is the informational classification field (spec §4.3 universal
// fields, §4.7.4).
type Sensitivity string

// Sensitivity values per §4.3 / §4.7.4.
const (
	SensitivityLow    Sensitivity = "low"
	SensitivityMedium Sensitivity = "medium"
	SensitivityHigh   Sensitivity = "high"
)

// SearchVisibility values (spec §4.3 universal fields).
type SearchVisibility string

// SearchVisibility values.
const (
	SearchVisibilityIndexed    SearchVisibility = "indexed"
	SearchVisibilityDirectOnly SearchVisibility = "direct-only"
)

// SandboxProfile values (spec §4.4.1).
type SandboxProfile string

// SandboxProfile values per §4.4.1.
const (
	SandboxUnrestricted    SandboxProfile = "unrestricted"
	SandboxReadOnlyFS      SandboxProfile = "read-only-fs"
	SandboxNetworkIsolated SandboxProfile = "network-isolated"
	SandboxSeccompStrict   SandboxProfile = "seccomp-strict"
)

// EffortHint values (spec §4.3 caller-interpreted fields).
type EffortHint string

// EffortHint values per §4.3.
const (
	EffortLow      EffortHint = "low"
	EffortMedium   EffortHint = "medium"
	EffortHigh     EffortHint = "high"
	EffortMaximum  EffortHint = "max"
)

// ModelClassHint values (spec §4.3 caller-interpreted fields).
type ModelClassHint string

// ModelClassHint values per §4.3.
const (
	ModelClassNano     ModelClassHint = "nano"
	ModelClassSmall    ModelClassHint = "small"
	ModelClassMedium   ModelClassHint = "medium"
	ModelClassLarge    ModelClassHint = "large"
	ModelClassFrontier ModelClassHint = "frontier"
)

// RuleMode controls when the harness loads a type: rule artifact (spec §4.3,
// §4.3 rule fields).
type RuleMode string

// RuleMode values per §4.3.
const (
	RuleModeAlways   RuleMode = "always"
	RuleModeGlob     RuleMode = "glob"
	RuleModeAuto     RuleMode = "auto"
	RuleModeExplicit RuleMode = "explicit"
)

// Artifact is the canonical decoded representation of an ARTIFACT.md file's
// frontmatter, per the universal fields (§4.3) plus all caller-interpreted
// and type-specific fields. The body field carries the prose body (empty
// for type: skill, where prose lives in SKILL.md).
type Artifact struct {
	// Universal (§4.3).
	Type             ArtifactType     `yaml:"type"`
	Name             string           `yaml:"name,omitempty"`
	Version          string           `yaml:"version"`
	Description      string           `yaml:"description,omitempty"`
	WhenToUse        []string         `yaml:"when_to_use,omitempty"`
	Tags             []string         `yaml:"tags,omitempty"`
	Sensitivity      Sensitivity      `yaml:"sensitivity,omitempty"`
	License          string           `yaml:"license,omitempty"`
	SearchVisibility SearchVisibility `yaml:"search_visibility,omitempty"`
	Deprecated       bool             `yaml:"deprecated,omitempty"`
	ReplacedBy       string           `yaml:"replaced_by,omitempty"`
	ReleaseNotes     string           `yaml:"release_notes,omitempty"`

	// Caller-interpreted (§4.3).
	MCPServers          []MCPServerRef        `yaml:"mcpServers,omitempty"`
	RequiresApproval    []ApprovalRequirement `yaml:"requiresApproval,omitempty"`
	RuntimeRequirements *RuntimeRequirements  `yaml:"runtime_requirements,omitempty"`
	SandboxProfile      SandboxProfile        `yaml:"sandbox_profile,omitempty"`
	EffortHint          EffortHint            `yaml:"effort_hint,omitempty"`
	ModelClassHint      ModelClassHint        `yaml:"model_class_hint,omitempty"`
	SBOM                *SBOMRef              `yaml:"sbom,omitempty"`

	// Type-specific (§4.3).
	Input              string   `yaml:"input,omitempty"`
	Output             string   `yaml:"output,omitempty"`
	DelegatesTo        []string `yaml:"delegates_to,omitempty"`
	ExposeAsMCPPrompt  bool     `yaml:"expose_as_mcp_prompt,omitempty"`
	RuleMode           RuleMode `yaml:"rule_mode,omitempty"`
	RuleGlobs          string   `yaml:"rule_globs,omitempty"`
	RuleDescription    string   `yaml:"rule_description,omitempty"`
	HookEvent          string   `yaml:"hook_event,omitempty"`
	HookAction         string   `yaml:"hook_action,omitempty"`
	ServerIdentifier   string   `yaml:"server_identifier,omitempty"`

	// Inheritance / targeting (§4.3, §4.6).
	Extends         string   `yaml:"extends,omitempty"`
	TargetHarnesses []string `yaml:"target_harnesses,omitempty"`

	// External resources (§4.3 external resources).
	ExternalResources []ExternalResource `yaml:"external_resources,omitempty"`

	// Body is the markdown prose below the frontmatter. Empty for
	// type: skill (the body lives in SKILL.md).
	Body string `yaml:"-"`
}

// MCPServerRef is a single entry in the mcpServers list.
type MCPServerRef struct {
	Name      string            `yaml:"name"`
	Transport string            `yaml:"transport,omitempty"`
	Command   string            `yaml:"command,omitempty"`
	Args      []string          `yaml:"args,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
}

// ApprovalRequirement is a single entry in requiresApproval.
type ApprovalRequirement struct {
	Tool   string `yaml:"tool"`
	Reason string `yaml:"reason,omitempty"`
}

// RuntimeRequirements lists what a host must satisfy to materialize the
// artifact (§4.4.1).
type RuntimeRequirements struct {
	Python         string   `yaml:"python,omitempty"`
	Node           string   `yaml:"node,omitempty"`
	SystemPackages []string `yaml:"system_packages,omitempty"`
}

// SBOMRef declares the artifact's SBOM (CycloneDX or SPDX) inline or by
// reference (§4.3 caller-interpreted fields).
type SBOMRef struct {
	Format string `yaml:"format,omitempty"`
	Ref    string `yaml:"ref,omitempty"`
}

// ExternalResource is a pre-uploaded large resource (§4.3 external
// resources).
type ExternalResource struct {
	Path      string `yaml:"path"`
	URL       string `yaml:"url"`
	SHA256    string `yaml:"sha256,omitempty"`
	Size      int64  `yaml:"size,omitempty"`
	Signature string `yaml:"signature,omitempty"`
}

// Skill is the agentskills.io-compliant manifest contained in SKILL.md, per
// §4.3.4. Skills carry their prose body here; the corresponding ARTIFACT.md
// has frontmatter only.
type Skill struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`
	AllowedTools  []string          `yaml:"allowed-tools,omitempty"`

	// Body is the agent-facing skill prose body.
	Body string `yaml:"-"`
}

// Domain is the parsed DOMAIN.md, per §4.5.1.
type Domain struct {
	Unlisted    bool             `yaml:"unlisted,omitempty"`
	Description string           `yaml:"description,omitempty"`
	Discovery   *DomainDiscovery `yaml:"discovery,omitempty"`
	Include     []string         `yaml:"include,omitempty"`
	Exclude     []string         `yaml:"exclude,omitempty"`

	// Body is the long-form prose body, returned only when the domain is
	// the requested path (§4.5.5 description rendering).
	Body string `yaml:"-"`
}

// DomainDiscovery is the per-domain rendering knobs (§4.5.5).
type DomainDiscovery struct {
	MaxDepth              int      `yaml:"max_depth,omitempty"`
	FoldBelowArtifacts    int      `yaml:"fold_below_artifacts,omitempty"`
	FoldPassthroughChains *bool    `yaml:"fold_passthrough_chains,omitempty"`
	NotableCount          int      `yaml:"notable_count,omitempty"`
	TargetResponseTokens  int      `yaml:"target_response_tokens,omitempty"`
	Featured              []string `yaml:"featured,omitempty"`
	Deprioritize          []string `yaml:"deprioritize,omitempty"`
	Keywords              []string `yaml:"keywords,omitempty"`
}
