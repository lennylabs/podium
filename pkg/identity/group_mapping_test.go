package identity_test

import (
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
)

// Spec: §6.3.1 — the IdpGroupMapping adapter rewrites OIDC group-claim
// values to layer group names; an unmapped value passes through unchanged
// so direct-match deployments keep working.
func TestIdpGroupMapping_Map(t *testing.T) {
	t.Parallel()
	m := identity.NewIdpGroupMapping(map[string]string{
		"00g1financeOID": "finance",
		"00g2engOID":     "engineering",
	})
	got := m.Map([]string{"00g1financeOID", "already-friendly", "00g2engOID"})
	want := []string{"already-friendly", "engineering", "finance"} // sorted, deduped
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Map = %v, want %v", got, want)
	}
}

// Spec: §6.3.1 — duplicate inputs collapse to a single mapped group.
func TestIdpGroupMapping_Dedup(t *testing.T) {
	t.Parallel()
	m := identity.NewIdpGroupMapping(map[string]string{"a": "finance", "b": "finance"})
	got := m.Map([]string{"a", "b", "finance"})
	if !reflect.DeepEqual(got, []string{"finance"}) {
		t.Errorf("Map = %v, want [finance]", got)
	}
}

// Spec: §6.3.1 — an empty mapping is the identity transform (pass-through).
func TestIdpGroupMapping_EmptyPassThrough(t *testing.T) {
	t.Parallel()
	m := identity.NewIdpGroupMapping(nil)
	if !m.Empty() {
		t.Errorf("Empty() = false for nil table")
	}
	if m.Len() != 0 {
		t.Errorf("Len() = %d, want 0", m.Len())
	}
	got := m.Map([]string{"x", "y"})
	if !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("Map = %v, want [x y]", got)
	}
	// A nil receiver also passes through without panicking.
	var nilMap *identity.IdpGroupMapping
	if !nilMap.Empty() {
		t.Errorf("nil.Empty() = false")
	}
	if got := nilMap.Map([]string{"z"}); !reflect.DeepEqual(got, []string{"z"}) {
		t.Errorf("nil.Map = %v, want [z]", got)
	}
}

// Spec: §6.3.1 — the PODIUM_IDP_GROUP_MAPPING spec form parses, and a
// malformed entry is an error so a misconfiguration surfaces at startup.
func TestParseIdpGroupMapping(t *testing.T) {
	t.Parallel()
	m, err := identity.ParseIdpGroupMapping(" 00g1=finance , 00g2 = engineering ")
	if err != nil {
		t.Fatalf("ParseIdpGroupMapping: %v", err)
	}
	if m.Len() != 2 {
		t.Errorf("Len() = %d, want 2", m.Len())
	}
	if got := m.Map([]string{"00g1"}); !reflect.DeepEqual(got, []string{"finance"}) {
		t.Errorf("Map = %v, want [finance]", got)
	}
	// Empty spec yields an empty pass-through mapping.
	if e, err := identity.ParseIdpGroupMapping(""); err != nil || !e.Empty() {
		t.Errorf("empty spec: m.Empty=%v err=%v", e.Empty(), err)
	}
	// Malformed entry (no "=") is an error.
	if _, err := identity.ParseIdpGroupMapping("finance"); err == nil {
		t.Errorf("expected error for malformed entry")
	}
}
