package layer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Spec: §4.6, §13.11.3 — the executable-field reason lists the field paths
// joined by ", " and names neither the parent nor any layer.
func TestExecutableInheritance_Reason(t *testing.T) {
	t.Parallel()
	e := &ExecutableInheritance{
		ArtifactID: "team/child",
		Fields:     []string{"mcpServers[jira]", "server_identifier"},
	}
	want := "extends: a parent in another layer sets executable fields this artifact does not restate: mcpServers[jira], server_identifier; restate each one in this artifact"
	if got := e.Reason(); got != want {
		t.Errorf("Reason() = %q, want %q", got, want)
	}
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	// The child's own ID is the only ID a same-ID overlay could carry, and
	// the different-ID reason does not name the child either.
	if strings.Contains(e.Reason(), e.ArtifactID) {
		t.Errorf("Reason() = %q, must not name %q", e.Reason(), e.ArtifactID)
	}
	single := &ExecutableInheritance{ArtifactID: "x", Fields: []string{"hook_action"}}
	if got, want := single.Reason(), "extends: a parent in another layer sets executable fields this artifact does not restate: hook_action; restate each one in this artifact"; got != want {
		t.Errorf("single-field Reason() = %q, want %q", got, want)
	}
}

// Spec: §4.6, §7.3.1 — the rejection details carry the constraint name and
// the field paths, and the type matches through errors.As.
func TestExecutableInheritance_DetailsAndErrorsAs(t *testing.T) {
	t.Parallel()
	fields := []string{"env[TOKEN]", "x_runner"}
	e := &ExecutableInheritance{ArtifactID: "team/child", Fields: fields}
	want := map[string]any{"constraint": "executable_inheritance", "fields": fields}
	if got := e.Details(); !reflect.DeepEqual(got, want) {
		t.Errorf("Details() = %#v, want %#v", got, want)
	}
	if ExecutableInheritanceConstraint != "executable_inheritance" {
		t.Errorf("ExecutableInheritanceConstraint = %q", ExecutableInheritanceConstraint)
	}
	var err error = e
	var got *ExecutableInheritance
	if !errors.As(err, &got) || got != e {
		t.Errorf("errors.As(%v) did not yield the ExecutableInheritance", err)
	}
}

// Spec: §4.6, §13.11.3 — the shared reasons and the code both modes report.
func TestExecutableSharedReasons(t *testing.T) {
	t.Parallel()
	if InvalidArtifactCode != "ingest.invalid_artifact" {
		t.Errorf("InvalidArtifactCode = %q, want ingest.invalid_artifact", InvalidArtifactCode)
	}
	if got, want := UnresolvedParentReason("team/base"), `extends: no parent artifact "team/base" ingested yet`; got != want {
		t.Errorf("UnresolvedParentReason = %q, want %q", got, want)
	}
	if UnreadableExecutableFieldsReason != "extends: executable fields could not be read" {
		t.Errorf("UnreadableExecutableFieldsReason = %q", UnreadableExecutableFieldsReason)
	}
}
