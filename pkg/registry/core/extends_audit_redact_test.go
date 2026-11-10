package core_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// earChildID is the canonical ID of the child earLoad stores beside its parent
// shared/parent. It sorts byte-wise after the parent's ID, so the single ingest
// pass stores the parent before the child resolves its pin.
const earChildID = "shared/payroll-child"

// earLoad ingests a parent and a child into one layer, loads the child, and
// returns the audit events the load emitted. The tests that call it pin how
// audit_redact and the undeclared keys it names reach the read event when the
// child inherits them, and §4.6 refuses a child that leaves an undeclared key to
// a parent in another layer, so both rows share a layer.
func earLoad(t *testing.T, parent, child string) []core.AuditEvent {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L1",
		Files: fstest.MapFS{
			"shared/parent/ARTIFACT.md": &fstest.MapFile{Data: []byte(parent)},
			earChildID + "/ARTIFACT.md": &fstest.MapFile{Data: []byte(child)},
		},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if res.Accepted != 2 {
		t.Fatalf("ingest accepted %d of 2 artifacts: %+v", res.Accepted, res.Rejected)
	}
	rec := &recorder{}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L1", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	}).WithAudit(rec.emit)
	if _, err := reg.LoadArtifact(context.Background(), publicID, earChildID, core.LoadArtifactOptions{}); err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	return rec.snapshot()
}

// hasKey reports whether keys contains want.
func hasKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// loadEventFor returns the artifact.loaded event for target, or nil when the
// load emitted none.
func loadEventFor(events []core.AuditEvent, target string) *core.AuditEvent {
	for i, e := range events {
		if e.Target == target && e.Type == "artifact.loaded" {
			return &events[i]
		}
	}
	return nil
}

// redactKeysFor returns the RedactKeys of the load event for target.
func redactKeysFor(events []core.AuditEvent, target string) []string {
	e := loadEventFor(events, target)
	if e == nil {
		return nil
	}
	return e.RedactKeys
}

// Spec: §8.2 — the read event carries the manifest's audit_redact key set so
// the sink masks the named keys, and §4.6 makes audit_redact inheritable. The
// emitter derived its key set from the stored leaf record, whose own
// frontmatter carries no directive when the child inherits one, so an
// inherited directive reached no event.
//
// This is a fidelity gap rather than a leak: manifest.FrontmatterFields
// returns nil for an empty key set, so before the fix no value was surfaced
// into the event at all. A test asserting that a sensitive value reached the
// sink unmasked could never fail, which is why this asserts on the key set and
// on the masked value together.
func TestExtendsAuditRedact_InheritedDirectiveReachesTheReadEvent(t *testing.T) {
	t.Parallel()
	events := earLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
			"audit_redact: [x_bank_account]\nx_bank_account: GB29-NWBK-0000\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\nextends: shared/parent@1.x\n---\n\nchild body\n")

	keys := redactKeysFor(events, earChildID)
	if !hasKey(keys, "x_bank_account") {
		t.Fatalf("RedactKeys = %v, want the inherited x_bank_account directive", keys)
	}
	// The directive needs a concrete target, so the value must be in the
	// event context for the emitter to mask.
	for _, e := range events {
		if e.Target != earChildID || e.Type != "artifact.loaded" {
			continue
		}
		if got := e.Context["x_bank_account"]; got != "GB29-NWBK-0000" {
			t.Errorf("Context[x_bank_account] = %q, want the inherited raw value the sink masks: %v", got, e.Context)
		}
	}
}

// Spec: §4.6 — a child that declares its own audit_redact keeps it. The
// directive is a scalar-list field the child owns, so the child's declaration
// replaces the parent's rather than being unioned with it.
//
// The negative half is the load-bearing one. Asserting only that the child's
// own key survives passes against a merge that serves the union of the two
// directives, which is the fidelity regression the inherited-directive repair
// can introduce. The parent declares audit_redact: [x_parent_key] and carries
// x_parent_key, so a union would put that name in the key set and its value in
// the event context.
func TestExtendsAuditRedact_ChildOwnDirectiveIsKept(t *testing.T) {
	t.Parallel()
	events := earLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n"+
			"audit_redact: [x_parent_key]\nx_parent_key: p\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\n"+
			"audit_redact: [x_child_key]\nx_child_key: c\nextends: shared/parent@1.x\n---\n\nchild body\n")

	ev := loadEventFor(events, earChildID)
	if ev == nil {
		t.Fatalf("no artifact.loaded event for %s: %+v", earChildID, events)
	}
	if !hasKey(ev.RedactKeys, "x_child_key") {
		t.Errorf("RedactKeys = %v, want the child's own x_child_key", ev.RedactKeys)
	}
	if hasKey(ev.RedactKeys, "x_parent_key") {
		t.Errorf("RedactKeys = %v, want the parent's x_parent_key replaced by the child's directive", ev.RedactKeys)
	}
	if _, ok := ev.Context["x_parent_key"]; ok {
		t.Errorf("event context carries the parent's x_parent_key the child's directive does not name: %v", ev.Context)
	}
}

// Spec: §8.2 — an artifact that declares no directive, and whose parent
// declares none either, emits an event with no redaction keys. This is the arm
// that would break if the merged directive were assembled unconditionally.
func TestExtendsAuditRedact_NoDirectiveAnywhereEmitsNoKeys(t *testing.T) {
	t.Parallel()
	events := earLoad(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\nextends: shared/parent@1.x\n---\n\nchild body\n")

	if keys := redactKeysFor(events, earChildID); len(keys) != 0 {
		t.Errorf("RedactKeys = %v, want none", keys)
	}
}
