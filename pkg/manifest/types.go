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
// are skill, agent, rule, and hook, and mcp-server is the built-in extension
// type (per §4.1). The TypeContext and TypeCommand constants name the types
// §4.1 removed, which ingest refuses.
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
	EffortLow     EffortHint = "low"
	EffortMedium  EffortHint = "medium"
	EffortHigh    EffortHint = "high"
	EffortMaximum EffortHint = "max"
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

// Invocation records who starts a type: skill artifact (spec §4.3
// type-specific fields, §4.3.4 Invocation). The resolved value keeps the raw
// authored string, so an invalid value survives resolution; every reader
// decides through Artifact.UserInvoked, which treats only InvocationUser as
// user-only.
type Invocation string

// Invocation values per §4.3.4. InvocationAny is the default.
const (
	InvocationAny  Invocation = "any"
	InvocationUser Invocation = "user"
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
	// AuditRedact lists field names whose values must be replaced
	// with "[redacted]" in audit log entries that reference this
	// artifact (§8.2 manifest-declared redaction surface).
	AuditRedact []string `yaml:"audit_redact,omitempty"`
	// LintSuppress lists lint rule codes to suppress for this artifact.
	// The rules that honor the list are the agentskills.io `skills-ref
	// validate` check (lint.skill_ref_validate), for cases where the
	// standard's validator is overly strict, and the §7.3.1 catalog
	// advisories (lint.thin_description, lint.colliding_descriptions, and
	// lint.near_duplicate_content). Every other rule ignores the list. For
	// the pairwise advisories the suppression is one-sided: the other
	// artifact of the pair still names this one. A skill with no
	// ARTIFACT.md lists no code.
	//
	// Spec: §4.3.4, §7.3.1
	LintSuppress []string `yaml:"lint_suppress,omitempty"`
	// Source declares the document-level provenance of the manifest's
	// prose (§4.4.2). The documented value is `authored`; a non-authored
	// value (for example `imported`) marks the body's default trust region
	// as untrusted, so adapters that support trust regions wrap the prose.
	// Inline `<!-- begin imported ... -->` blocks override this per region.
	Source string `yaml:"source,omitempty"`

	// Caller-interpreted (§4.3).
	MCPServers          []MCPServerRef        `yaml:"mcpServers,omitempty"`
	RequiresApproval    []ApprovalRequirement `yaml:"requiresApproval,omitempty"`
	RuntimeRequirements *RuntimeRequirements  `yaml:"runtime_requirements,omitempty"`
	SandboxProfile      SandboxProfile        `yaml:"sandbox_profile,omitempty"`
	EffortHint          EffortHint            `yaml:"effort_hint,omitempty"`
	ModelClassHint      ModelClassHint        `yaml:"model_class_hint,omitempty"`
	SBOM                *SBOMRef              `yaml:"sbom,omitempty"`

	// Type-specific (§4.3).
	//
	// Invocation (type: skill) states who starts the skill. Read it
	// through UserInvoked rather than comparing the raw value.
	Invocation Invocation `yaml:"invocation,omitempty"`

	// Input and Output (type: agent) reference external JSON Schema
	// documents. The documented YAML form is a mapping with a $ref key
	// (input: { $ref: ./schemas/input.json }); SchemaRef.UnmarshalYAML
	// also accepts a bare scalar path. Nil when the field is absent.
	Input            *SchemaRef `yaml:"input,omitempty"`
	Output           *SchemaRef `yaml:"output,omitempty"`
	DelegatesTo      []string   `yaml:"delegates_to,omitempty"`
	RuleMode         RuleMode   `yaml:"rule_mode,omitempty"`
	RuleGlobs        string     `yaml:"rule_globs,omitempty"`
	RuleDescription  string     `yaml:"rule_description,omitempty"`
	HookEvent        string     `yaml:"hook_event,omitempty"`
	HookAction       string     `yaml:"hook_action,omitempty"`
	ServerIdentifier string     `yaml:"server_identifier,omitempty"`

	// Targeting (§4.3).
	TargetHarnesses []string `yaml:"target_harnesses,omitempty"`

	// External resources (§4.3 external resources).
	ExternalResources []ExternalResource `yaml:"external_resources,omitempty"`

	// Body is the markdown prose below the frontmatter. Empty for
	// type: skill (the body lives in SKILL.md).
	Body string `yaml:"-"`
}

// UserInvoked reports whether a is a skill meant for a human to invoke. Only
// the exact resolved value "user" on a type: skill artifact is user-only. An
// absent value, "any", and every other value, including "User" and an unknown
// word, resolve to any, whether or not lint ran. A nil a reports false.
//
// Spec: §4.3, §4.3.4
func (a *Artifact) UserInvoked() bool {
	return a != nil && a.Type == TypeSkill && a.Invocation == InvocationUser
}

// SchemaRef references an external JSON Schema document. It backs the
// type: agent input/output fields (§4.3 type-specific fields), whose
// documented YAML form is a mapping with a $ref key
// (input: { $ref: ./schemas/input.json }). The custom UnmarshalYAML in
// parse.go also decodes a bare scalar path into Ref so both forms parse.
type SchemaRef struct {
	Ref string `yaml:"$ref,omitempty"`
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
