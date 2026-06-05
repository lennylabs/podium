package serverboot

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.1 — "Org IDs are UUIDs; org names are human-readable aliases."
// The bootstrapped org's ID is a valid UUID (not the literal "default"),
// while its name stays the human-readable "default".
func TestBootstrapDefaultTenant_OrgIDIsUUIDNameIsDefault(t *testing.T) {
	st := store.NewMemory()
	id, err := bootstrapDefaultTenant(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("bootstrapDefaultTenant: %v", err)
	}
	if id == "default" {
		t.Fatalf("org ID is the literal %q, want a UUID", id)
	}
	if _, err := uuid.Parse(id); err != nil {
		t.Errorf("org ID %q does not parse as a UUID: %v", id, err)
	}
	tn, err := st.GetTenant(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTenant(%q): %v", id, err)
	}
	if tn.ID != id {
		t.Errorf("stored tenant ID = %q, want %q", tn.ID, id)
	}
	if tn.Name != "default" {
		t.Errorf("stored tenant Name = %q, want \"default\" (the human-readable alias)", tn.Name)
	}
}

// Spec: §4.7.1 — a name-based UUID is stable across restarts so persisted
// tenant-scoped rows are not orphaned. The ID a second boot computes for the
// same org name matches the first.
func TestOrgIDForName_StableAndDistinct(t *testing.T) {
	a := orgIDForName("default")
	b := orgIDForName("default")
	if a != b {
		t.Errorf("orgIDForName(\"default\") not stable: %q != %q", a, b)
	}
	if other := orgIDForName("acme"); other == a {
		t.Errorf("distinct org names collided on %q", a)
	}
	if v := uuid.MustParse(a).Version(); v != 5 {
		t.Errorf("org ID UUID version = %d, want 5 (name-based)", v)
	}
}

// Spec: §13.10 — auto-bootstrap is idempotent. A second bootstrap of the same
// org is a no-op that returns the same UUID without error.
func TestBootstrapDefaultTenant_Idempotent(t *testing.T) {
	st := store.NewMemory()
	first, err := bootstrapDefaultTenant(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	second, err := bootstrapDefaultTenant(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if first != second {
		t.Errorf("bootstrap not idempotent: %q != %q", first, second)
	}
}
