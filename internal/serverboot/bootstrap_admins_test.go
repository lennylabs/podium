package serverboot

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// spec: §13.1.1 — the evaluation-stack bootstrap "creates the first tenant
// and admin user (configurable via env vars)". PODIUM_BOOTSTRAP_ADMINS
// carries the user IDs; parseBootstrapAdmins splits them and
// seedBootstrapAdmins grants the tenant admin role.

func TestParseBootstrapAdmins(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace only", "   \t\n", nil},
		{"single", "alice@acme.com", []string{"alice@acme.com"}},
		{"comma separated", "alice@acme.com,bob@acme.com", []string{"alice@acme.com", "bob@acme.com"}},
		{"comma and space", "alice@acme.com, bob@acme.com", []string{"alice@acme.com", "bob@acme.com"}},
		{"space separated", "alice@acme.com bob@acme.com", []string{"alice@acme.com", "bob@acme.com"}},
		{"trailing comma", "alice@acme.com,", []string{"alice@acme.com"}},
		{"empty fields dropped", "alice@acme.com,,  ,carol@acme.com", []string{"alice@acme.com", "carol@acme.com"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseBootstrapAdmins(c.raw)
			if len(got) != len(c.want) {
				t.Fatalf("parseBootstrapAdmins(%q) = %v, want %v", c.raw, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("parseBootstrapAdmins(%q)[%d] = %q, want %q", c.raw, i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestSeedBootstrapAdmins_GrantsEachUser(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	const tenantID = "default"
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID, Name: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	admins := []string{"alice@acme.com", "bob@acme.com"}
	n, err := seedBootstrapAdmins(context.Background(), st, tenantID, admins)
	if err != nil {
		t.Fatalf("seedBootstrapAdmins: %v", err)
	}
	if n != 2 {
		t.Fatalf("seeded = %d, want 2", n)
	}
	for _, u := range admins {
		ok, err := st.IsAdmin(context.Background(), u, tenantID)
		if err != nil {
			t.Fatalf("IsAdmin(%q): %v", u, err)
		}
		if !ok {
			t.Errorf("IsAdmin(%q) = false, want true after seeding", u)
		}
	}
	// A non-seeded user must not be an admin.
	if ok, _ := st.IsAdmin(context.Background(), "carol@acme.com", tenantID); ok {
		t.Errorf("IsAdmin(carol) = true, want false (never seeded)")
	}
}

func TestSeedBootstrapAdmins_Idempotent(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	const tenantID = "default"
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID, Name: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	admins := []string{"alice@acme.com"}
	// Re-seeding on a later boot must not error (the grant already exists).
	for i := 0; i < 3; i++ {
		if _, err := seedBootstrapAdmins(context.Background(), st, tenantID, admins); err != nil {
			t.Fatalf("seedBootstrapAdmins pass %d: %v", i, err)
		}
	}
	if ok, _ := st.IsAdmin(context.Background(), "alice@acme.com", tenantID); !ok {
		t.Errorf("IsAdmin(alice) = false after repeated seeding, want true")
	}
}

func TestSeedBootstrapAdmins_EmptyIsNoop(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	n, err := seedBootstrapAdmins(context.Background(), st, "default", nil)
	if err != nil {
		t.Fatalf("seedBootstrapAdmins(nil): %v", err)
	}
	if n != 0 {
		t.Errorf("seeded = %d, want 0 for an empty admin list", n)
	}
}

// LoadConfig must thread PODIUM_BOOTSTRAP_ADMINS into the parsed config so
// Run seeds the grants.
func TestLoadConfig_BootstrapAdminsFromEnv(t *testing.T) {
	t.Setenv("PODIUM_BOOTSTRAP_ADMINS", "alice@acme.com, bob@acme.com")
	c := LoadConfig()
	if len(c.bootstrapAdmins) != 2 {
		t.Fatalf("bootstrapAdmins = %v, want 2 entries", c.bootstrapAdmins)
	}
	if c.bootstrapAdmins[0] != "alice@acme.com" || c.bootstrapAdmins[1] != "bob@acme.com" {
		t.Errorf("bootstrapAdmins = %v, want [alice@acme.com bob@acme.com]", c.bootstrapAdmins)
	}
}
