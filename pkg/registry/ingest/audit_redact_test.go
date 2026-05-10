package ingest_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

const auditRedactArtifact = `---
type: skill
version: 1.0.0
description: skill that bundles sensitive metadata
sensitivity: low
audit_redact:
  - content_hash
---

`
const auditRedactSkillBody = `---
name: redact-me
description: redact-me
---
body
`

// Spec: §8.2 — manifest-declared `audit_redact` causes the
// registry to replace the named field's value with "[redacted]"
// in audit context for events that reference this artifact.
// Phase: 8
func TestIngest_AuditRedactReplacesContext(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	files := fstest.MapFS{
		"redact-me/ARTIFACT.md": &fstest.MapFile{Data: []byte(auditRedactArtifact)},
		"redact-me/SKILL.md":    &fstest.MapFile{Data: []byte(auditRedactSkillBody)},
	}
	type emitted struct {
		event string
		ctx   map[string]string
	}
	var emits []emitted
	auditEmit := func(eventType, _ string, ctx map[string]string) {
		c := map[string]string{}
		for k, v := range ctx {
			c[k] = v
		}
		emits = append(emits, emitted{eventType, c})
	}

	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID:  "t",
		LayerID:   "team",
		Linter:    &lint.Linter{},
		Files:     files,
		AuditEmit: auditEmit,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("Accepted = %d, want 1; rejects=%+v lint=%+v", res.Accepted, res.Rejected, res.LintFailures)
	}
	publish := emitted{}
	for _, e := range emits {
		if e.event == "artifact.published" {
			publish = e
		}
	}
	if publish.event == "" {
		t.Fatalf("artifact.published not emitted; got %+v", emits)
	}
	if got := publish.ctx["content_hash"]; got != "[redacted]" {
		t.Errorf("content_hash = %q, want [redacted] (manifest declared audit_redact)", got)
	}
	// Other fields pass through unchanged.
	if got := publish.ctx["layer"]; got != "team" {
		t.Errorf("layer = %q, want team", got)
	}
	if got := publish.ctx["version"]; got != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", got)
	}
}

// Spec: §8.2 — when audit_redact is empty, every context field
// passes through (no false redactions).
// Phase: 8
func TestIngest_NoAuditRedactPassesThrough(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	files := fstest.MapFS{
		"plain/ARTIFACT.md": &fstest.MapFile{Data: []byte(`---
type: skill
version: 1.0.0
description: plain
sensitivity: low
---

`)},
		"plain/SKILL.md": &fstest.MapFile{Data: []byte("---\nname: plain\ndescription: plain\n---\nbody\n")},
	}
	var captured map[string]string
	auditEmit := func(eventType, _ string, ctx map[string]string) {
		if eventType == "artifact.published" {
			captured = map[string]string{}
			for k, v := range ctx {
				captured[k] = v
			}
		}
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "team",
		Linter:    &lint.Linter{},
		Files:     files,
		AuditEmit: auditEmit,
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if captured == nil {
		t.Fatal("artifact.published not emitted")
	}
	for _, k := range []string{"version", "content_hash", "layer"} {
		if captured[k] == "[redacted]" {
			t.Errorf("%s = [redacted] without manifest opt-in", k)
		}
	}
}
