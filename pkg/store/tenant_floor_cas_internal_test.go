package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Spec: §7.3.3, §4.7.11 (Tenant trust floor) — the conditional tenant write
// compares decoded values: a floor column that decodes but is not in the
// form the store writes ("[]", "null", or spaced JSON, from a hand edit or
// an external migration) matches the record the caller read, so a PATCH
// that would repair the tenant is not refused as changed forever.
func TestSQLite_UpdateTenantIfComparesDecodedFloor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, col := range []struct{ column, value string }{
		{"trust_floor_require_types", "[]"}, {"trust_floor_require_types", "null"}, {"trust_floor_require_types", `[ "hook" ]`},
		{"trust_floor_signers", "[]"}, {"trust_floor_signers", ` [ {"issuer" : "i"} ] `},
	} {
		s, err := OpenSQLite(filepath.Join(t.TempDir(), "s.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CreateTenant(ctx, Tenant{ID: "acme", Name: "acme", TrustFloor: TrustFloor{Mode: "require"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE tenants SET `+col.column+` = ? WHERE id = 'acme'`, col.value); err != nil {
			t.Fatal(err)
		}
		cur, err := s.GetTenant(ctx, "acme")
		if err != nil {
			t.Fatalf("%s %q: read: %v", col.column, col.value, err)
		}
		next := cur
		next.Quota.StorageBytes = 7
		err = s.UpdateTenantIf(ctx, next, cur)
		got, gerr := s.GetTenant(ctx, "acme")
		if err != nil || gerr != nil || got.Quota.StorageBytes != 7 {
			t.Errorf("%s %q: UpdateTenantIf = %v, stored quota %d (%v)", col.column, col.value, err, got.Quota.StorageBytes, gerr)
		}
		_ = s.Close()
	}
}
