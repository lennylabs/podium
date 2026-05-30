package manifest

import (
	"reflect"
	"testing"
)

// Spec: §4.6 field-semantics table — each row exercised once. parent is
// the lower-precedence artifact; child is the higher-precedence overlay.
func TestMergeExtends_FieldSemanticsTable(t *testing.T) {
	t.Parallel()
	parent := Artifact{
		Type:             TypeAgent,
		Name:             "parent-name",
		Version:          "1.0.0",
		Description:      "parent desc",
		ReleaseNotes:     "parent notes",
		License:          "Apache-2.0",
		Tags:             []string{"a", "shared"},
		WhenToUse:        []string{"parent case"},
		Sensitivity:      SensitivityMedium,
		SandboxProfile:   SandboxReadOnlyFS,
		SearchVisibility: SearchVisibilityIndexed,
		DelegatesTo:      []string{"finance/x"},
		RequiresApproval: []ApprovalRequirement{{Tool: "deploy"}},
		ExternalResources: []ExternalResource{
			{Path: "data/p.bin", URL: "https://e/p"},
		},
		MCPServers: []MCPServerRef{
			{Name: "github", Command: "gh-old", Transport: "stdio"},
			{Name: "jira", Command: "jira"},
		},
		RuntimeRequirements: &RuntimeRequirements{Python: "3.10", SystemPackages: []string{"git"}},
		EffortHint:          EffortLow, // unlisted field; inherited when child omits
	}
	child := Artifact{
		Type:             TypeAgent,
		Version:          "2.0.0",
		Description:      "child desc",
		License:          "MIT",
		Tags:             []string{"shared", "b"},
		WhenToUse:        []string{"child case"},
		Sensitivity:      SensitivityHigh,
		SandboxProfile:   SandboxSeccompStrict,
		SearchVisibility: SearchVisibilityDirectOnly,
		DelegatesTo:      []string{"finance/y"},
		RequiresApproval: []ApprovalRequirement{{Tool: "publish"}},
		ExternalResources: []ExternalResource{
			{Path: "data/c.bin", URL: "https://e/c"},
		},
		MCPServers: []MCPServerRef{
			{Name: "github", Command: "gh-new"}, // overrides parent's by name
			{Name: "slack", Command: "slack"},   // child-only, appended
		},
		RuntimeRequirements: &RuntimeRequirements{Python: "3.12", SystemPackages: []string{"curl"}},
		Extends:             "shared/parent@1.x",
	}

	got := MergeExtends(parent, child)

	// Scalars; child wins.
	if got.Description != "child desc" {
		t.Errorf("description = %q, want child desc", got.Description)
	}
	if got.License != "MIT" {
		t.Errorf("license = %q, want MIT (child wins)", got.License)
	}
	// name / release_notes: child omits → parent inherited.
	if got.Name != "parent-name" {
		t.Errorf("name = %q, want parent-name (inherited)", got.Name)
	}
	if got.ReleaseNotes != "parent notes" {
		t.Errorf("release_notes = %q, want inherited", got.ReleaseNotes)
	}
	// version: child's own.
	if got.Version != "2.0.0" {
		t.Errorf("version = %q, want child's 2.0.0", got.Version)
	}
	// tags: append unique.
	if !reflect.DeepEqual(got.Tags, []string{"a", "shared", "b"}) {
		t.Errorf("tags = %v, want [a shared b]", got.Tags)
	}
	// when_to_use: append (not unique).
	if !reflect.DeepEqual(got.WhenToUse, []string{"parent case", "child case"}) {
		t.Errorf("when_to_use = %v", got.WhenToUse)
	}
	// sensitivity / sandbox_profile: most-restrictive.
	if got.Sensitivity != SensitivityHigh {
		t.Errorf("sensitivity = %q, want high", got.Sensitivity)
	}
	if got.SandboxProfile != SandboxSeccompStrict {
		t.Errorf("sandbox_profile = %q, want seccomp-strict", got.SandboxProfile)
	}
	// search_visibility: most-restrictive (direct-only > indexed).
	if got.SearchVisibility != SearchVisibilityDirectOnly {
		t.Errorf("search_visibility = %q, want direct-only", got.SearchVisibility)
	}
	// delegates_to / requiresApproval / external_resources: append.
	if !reflect.DeepEqual(got.DelegatesTo, []string{"finance/x", "finance/y"}) {
		t.Errorf("delegates_to = %v", got.DelegatesTo)
	}
	if len(got.RequiresApproval) != 2 {
		t.Errorf("requiresApproval = %v, want 2 entries", got.RequiresApproval)
	}
	if len(got.ExternalResources) != 2 {
		t.Errorf("external_resources = %v, want 2 entries", got.ExternalResources)
	}
	// mcpServers: deep-merge by name. github overridden, jira inherited, slack added.
	byName := map[string]MCPServerRef{}
	for _, s := range got.MCPServers {
		byName[s.Name] = s
	}
	if len(got.MCPServers) != 3 {
		t.Errorf("mcpServers = %v, want 3 (github, jira, slack)", got.MCPServers)
	}
	if byName["github"].Command != "gh-new" {
		t.Errorf("github command = %q, want gh-new (child overrides)", byName["github"].Command)
	}
	if byName["github"].Transport != "stdio" {
		t.Errorf("github transport = %q, want stdio inherited from parent", byName["github"].Transport)
	}
	if byName["jira"].Command != "jira" {
		t.Errorf("jira not inherited: %v", byName["jira"])
	}
	if byName["slack"].Command != "slack" {
		t.Errorf("slack child-only not added: %v", byName["slack"])
	}
	// runtime_requirements: deep-merge, child python wins, system_packages unioned.
	if got.RuntimeRequirements.Python != "3.12" {
		t.Errorf("runtime python = %q, want 3.12", got.RuntimeRequirements.Python)
	}
	if !reflect.DeepEqual(got.RuntimeRequirements.SystemPackages, []string{"git", "curl"}) {
		t.Errorf("system_packages = %v, want [git curl]", got.RuntimeRequirements.SystemPackages)
	}
	// unlisted field: child omits effort_hint → parent inherited.
	if got.EffortHint != EffortLow {
		t.Errorf("effort_hint = %q, want low (inherited)", got.EffortHint)
	}
}

// Spec: §4.6 — the child cannot relax the parent's sensitivity or sandbox.
func TestMergeExtends_MostRestrictiveCannotRelax(t *testing.T) {
	t.Parallel()
	parent := Artifact{Sensitivity: SensitivityHigh, SandboxProfile: SandboxSeccompStrict}
	child := Artifact{Sensitivity: SensitivityLow, SandboxProfile: SandboxUnrestricted}
	got := MergeExtends(parent, child)
	if got.Sensitivity != SensitivityHigh {
		t.Errorf("sensitivity = %q, want high (child cannot relax)", got.Sensitivity)
	}
	if got.SandboxProfile != SandboxSeccompStrict {
		t.Errorf("sandbox = %q, want seccomp-strict (child cannot widen)", got.SandboxProfile)
	}
}

// Spec: §4.6 "Default for unlisted fields" — a child can override an
// inherited field by setting it.
func TestMergeExtends_UnlistedChildOverrides(t *testing.T) {
	t.Parallel()
	parent := Artifact{Deprecated: false, ReplacedBy: "", HookEvent: "PreToolUse"}
	child := Artifact{Deprecated: true, ReplacedBy: "finance/new", HookEvent: "PostToolUse"}
	got := MergeExtends(parent, child)
	if !got.Deprecated {
		t.Errorf("deprecated = false, want child's true")
	}
	if got.ReplacedBy != "finance/new" {
		t.Errorf("replaced_by = %q, want child's", got.ReplacedBy)
	}
	if got.HookEvent != "PostToolUse" {
		t.Errorf("hook_event = %q, want child's", got.HookEvent)
	}
}

// SerializeArtifact is the inverse of ParseArtifact for structured fields:
// a merged artifact round-trips through serialize → parse unchanged.
func TestSerializeArtifact_RoundTrip(t *testing.T) {
	t.Parallel()
	a := &Artifact{
		Type:        TypeAgent,
		Name:        "thing",
		Version:     "2.0.0",
		Description: "round trip",
		Tags:        []string{"x", "y"},
		WhenToUse:   []string{"use it"},
		Sensitivity: SensitivityHigh,
		MCPServers:  []MCPServerRef{{Name: "github", Command: "gh"}},
		Body:        "the body\n",
	}
	src, err := SerializeArtifact(a)
	if err != nil {
		t.Fatalf("SerializeArtifact: %v", err)
	}
	round, err := ParseArtifact(src)
	if err != nil {
		t.Fatalf("ParseArtifact(serialized): %v\nsource:\n%s", err, src)
	}
	if round.Name != "thing" || round.Version != "2.0.0" || round.Description != "round trip" {
		t.Errorf("scalar round-trip mismatch: %+v", round)
	}
	if !reflect.DeepEqual(round.Tags, []string{"x", "y"}) {
		t.Errorf("tags round-trip = %v", round.Tags)
	}
	if len(round.MCPServers) != 1 || round.MCPServers[0].Name != "github" {
		t.Errorf("mcpServers round-trip = %v", round.MCPServers)
	}
	if round.Body != "the body\n" {
		t.Errorf("body round-trip = %q, want %q", round.Body, "the body\n")
	}
}

// MergeExtends is defensive against empty inputs (no parent fields, no
// child fields) and returns zero-ish output without panicking.
func TestMergeExtends_Empty(t *testing.T) {
	t.Parallel()
	got := MergeExtends(Artifact{}, Artifact{})
	if got.Tags != nil || got.WhenToUse != nil || got.MCPServers != nil {
		t.Errorf("empty merge should yield nil slices, got %+v", got)
	}
}
