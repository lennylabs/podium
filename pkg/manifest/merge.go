package manifest

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"
)

// MergeExtends folds a child artifact onto its parent and returns the
// merged artifact per the §4.6 field-semantics table. parent is the
// lower-precedence artifact (the one named by the child's extends:);
// child is the higher-precedence overlay.
//
// Table fields merge as the spec mandates: description / name /
// release_notes / license are scalar child-wins; tags are append-unique;
// when_to_use / requiresApproval / delegates_to / external_resources are
// append; sensitivity and sandbox_profile take the most-restrictive
// value; search_visibility takes the most-restrictive value
// (direct-only > indexed); mcpServers deep-merge by name; and
// runtime_requirements deep-merge with the child winning per key.
//
// Every other frontmatter field follows the §4.6 "Default for unlisted
// fields" rule: the child's value overrides when it is set, otherwise the
// parent's value is inherited unchanged. version is independent (the
// child keeps its own), and type is expected to match (ingest rejects a
// cross-type chain per §4.6).
func MergeExtends(parent, child Artifact) Artifact {
	out := parent

	// --- Table fields -----------------------------------------------------
	// description, name, release_notes, license: scalar; child wins.
	if child.Description != "" {
		out.Description = child.Description
	}
	if child.Name != "" {
		out.Name = child.Name
	}
	if child.ReleaseNotes != "" {
		out.ReleaseNotes = child.ReleaseNotes
	}
	if child.License != "" {
		out.License = child.License
	}
	// tags: list; append unique.
	out.Tags = appendUnique(parent.Tags, child.Tags)
	// when_to_use: list; append.
	out.WhenToUse = appendAll(parent.WhenToUse, child.WhenToUse)
	// requiresApproval: list; append.
	out.RequiresApproval = appendApprovals(parent.RequiresApproval, child.RequiresApproval)
	// delegates_to: list; append.
	out.DelegatesTo = appendAll(parent.DelegatesTo, child.DelegatesTo)
	// external_resources: list; append.
	out.ExternalResources = appendResources(parent.ExternalResources, child.ExternalResources)
	// sensitivity: scalar; most-restrictive (high > medium > low).
	out.Sensitivity = Sensitivity(mostRestrictiveSensitivity(string(parent.Sensitivity), string(child.Sensitivity)))
	// sandbox_profile: scalar; most-restrictive.
	out.SandboxProfile = SandboxProfile(mostRestrictiveSandbox(string(parent.SandboxProfile), string(child.SandboxProfile)))
	// search_visibility: scalar; most-restrictive (direct-only > indexed).
	out.SearchVisibility = mostRestrictiveSearchVisibility(parent.SearchVisibility, child.SearchVisibility)
	// mcpServers: list of objects; deep-merge by name.
	out.MCPServers = mergeMCPServers(parent.MCPServers, child.MCPServers)
	// runtime_requirements: map; deep-merge with child wins.
	out.RuntimeRequirements = mergeRuntimeRequirements(parent.RuntimeRequirements, child.RuntimeRequirements)

	// --- Default for unlisted fields: child overrides when set ------------
	// version is the child's own (independent of the parent per §4.7.6);
	// out already carries the parent's, so take the child's verbatim.
	out.Version = child.Version
	if child.Type != "" {
		out.Type = child.Type
	}
	// deprecated is a boolean: a child can only set it (true), so a true
	// child overrides; an unset child inherits the parent.
	if child.Deprecated {
		out.Deprecated = true
	}
	if child.ReplacedBy != "" {
		out.ReplacedBy = child.ReplacedBy
	}
	if child.EffortHint != "" {
		out.EffortHint = child.EffortHint
	}
	if child.ModelClassHint != "" {
		out.ModelClassHint = child.ModelClassHint
	}
	if child.SBOM != nil {
		out.SBOM = child.SBOM
	}
	if child.RuleMode != "" {
		out.RuleMode = child.RuleMode
	}
	if child.RuleGlobs != "" {
		out.RuleGlobs = child.RuleGlobs
	}
	if child.RuleDescription != "" {
		out.RuleDescription = child.RuleDescription
	}
	if child.HookEvent != "" {
		out.HookEvent = child.HookEvent
	}
	if child.HookAction != "" {
		out.HookAction = child.HookAction
	}
	if child.ServerIdentifier != "" {
		out.ServerIdentifier = child.ServerIdentifier
	}
	if child.Input != nil {
		out.Input = child.Input
	}
	if child.Output != nil {
		out.Output = child.Output
	}
	if len(child.TargetHarnesses) > 0 {
		out.TargetHarnesses = child.TargetHarnesses
	}
	if len(child.AuditRedact) > 0 {
		out.AuditRedact = child.AuditRedact
	}
	if len(child.LintSuppress) > 0 {
		out.LintSuppress = child.LintSuppress
	}
	// source: document-level provenance (§4.4.2). Not in the §4.6 table, so
	// it follows the default-for-unlisted rule: the child's value overrides
	// when set, since out already carries the parent's.
	if child.Source != "" {
		out.Source = child.Source
	}
	// The merged artifact represents the child; carry the child's extends
	// reference and body. Callers that serve a resolved manifest strip
	// extends to preserve §4.6 hidden-parent privacy.
	out.Extends = child.Extends
	out.Body = child.Body
	return out
}

// SerializeArtifact renders an Artifact back into ARTIFACT.md source:
// a YAML frontmatter block followed by the prose body. It is the inverse
// of ParseArtifact for the structured fields and is used to re-emit a
// resolved (extends-merged) manifest per §4.6.
func SerializeArtifact(a *Artifact) ([]byte, error) {
	fm, err := yaml.Marshal(a)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	if body := strings.TrimRight(a.Body, "\n"); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.Bytes(), nil
}

// appendUnique returns a ∪ b preserving a's order, then b's new entries.
func appendUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, s := range list {
			if seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// appendAll concatenates a then b without de-duplication.
func appendAll(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

func appendApprovals(a, b []ApprovalRequirement) []ApprovalRequirement {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make([]ApprovalRequirement, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

func appendResources(a, b []ExternalResource) []ExternalResource {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make([]ExternalResource, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

// sensitivityRank orders sensitivity values for most-restrictive merges;
// the empty string ranks lowest so an unset value never overrides a set one.
func sensitivityRank(s string) int {
	switch s {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

func mostRestrictiveSensitivity(a, b string) string {
	if sensitivityRank(b) > sensitivityRank(a) {
		return b
	}
	return a
}

// sandboxRank orders sandbox profiles from least to most restrictive
// (§4.4.1). The empty string ranks lowest.
func sandboxRank(s string) int {
	switch s {
	case string(SandboxSeccompStrict):
		return 4
	case string(SandboxNetworkIsolated):
		return 3
	case string(SandboxReadOnlyFS):
		return 2
	case string(SandboxUnrestricted):
		return 1
	}
	return 0
}

func mostRestrictiveSandbox(a, b string) string {
	if sandboxRank(b) > sandboxRank(a) {
		return b
	}
	return a
}

// mostRestrictiveSearchVisibility returns direct-only when either input is
// direct-only (§4.6: direct-only > indexed); otherwise it returns the
// child's value when set, else the parent's.
func mostRestrictiveSearchVisibility(parent, child SearchVisibility) SearchVisibility {
	if parent == SearchVisibilityDirectOnly || child == SearchVisibilityDirectOnly {
		return SearchVisibilityDirectOnly
	}
	if child != "" {
		return child
	}
	return parent
}

// mergeMCPServers deep-merges the parent and child mcpServers lists by
// name: a child entry with the same name overrides the parent's fields
// (child non-empty wins per field), a parent-only server is inherited, and
// a child-only server is appended. Parent order is preserved; new child
// names follow.
func mergeMCPServers(parent, child []MCPServerRef) []MCPServerRef {
	if len(parent) == 0 && len(child) == 0 {
		return nil
	}
	idx := make(map[string]int, len(parent))
	out := make([]MCPServerRef, 0, len(parent)+len(child))
	for _, p := range parent {
		idx[p.Name] = len(out)
		out = append(out, p)
	}
	for _, c := range child {
		if i, ok := idx[c.Name]; ok {
			out[i] = mergeMCPServer(out[i], c)
			continue
		}
		idx[c.Name] = len(out)
		out = append(out, c)
	}
	return out
}

// mergeMCPServer merges one child server entry onto the parent entry of
// the same name; the child's non-empty fields win.
func mergeMCPServer(parent, child MCPServerRef) MCPServerRef {
	out := parent
	if child.Transport != "" {
		out.Transport = child.Transport
	}
	if child.Command != "" {
		out.Command = child.Command
	}
	if len(child.Args) > 0 {
		out.Args = child.Args
	}
	if len(child.Env) > 0 {
		merged := make(map[string]string, len(parent.Env)+len(child.Env))
		for k, v := range parent.Env {
			merged[k] = v
		}
		for k, v := range child.Env {
			merged[k] = v
		}
		out.Env = merged
	}
	return out
}

// mergeRuntimeRequirements deep-merges the runtime_requirements map: the
// child's python / node win when set, and system_packages are unioned.
func mergeRuntimeRequirements(parent, child *RuntimeRequirements) *RuntimeRequirements {
	if parent == nil && child == nil {
		return nil
	}
	out := &RuntimeRequirements{}
	if parent != nil {
		*out = *parent
		out.SystemPackages = append([]string(nil), parent.SystemPackages...)
	}
	if child != nil {
		if child.Python != "" {
			out.Python = child.Python
		}
		if child.Node != "" {
			out.Node = child.Node
		}
		out.SystemPackages = appendUnique(out.SystemPackages, child.SystemPackages)
	}
	return out
}
