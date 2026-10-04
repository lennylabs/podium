package server

import (
	"testing"
	"time"
)

// Spec: §4.7.8 — the meter refuses a tenant once its daily count reaches the limit.
func TestAuditVolumeMeter_EnforcesDailyLimit(t *testing.T) {
	m := NewAuditVolumeMeter()
	fixed := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return fixed }

	for i := 0; i < 3; i++ {
		if !m.Allow("acme", 3) {
			t.Fatalf("Allow returned false at event %d, want true (under budget)", i)
		}
		m.Record("acme")
	}
	if m.Allow("acme", 3) {
		t.Error("Allow returned true after the daily budget was spent")
	}
	// A different tenant has its own budget.
	if !m.Allow("globex", 3) {
		t.Error("globex was gated by acme's budget")
	}
}

// Spec: §4.7.8 — the daily count resets at the UTC day boundary.
func TestAuditVolumeMeter_ResetsAtUTCDayBoundary(t *testing.T) {
	m := NewAuditVolumeMeter()
	day := time.Date(2026, 6, 1, 23, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return day }

	m.Record("acme")
	m.Record("acme")
	if m.Allow("acme", 2) {
		t.Fatal("budget not spent on day one")
	}
	// Advance to the next UTC day.
	day = time.Date(2026, 6, 2, 0, 30, 0, 0, time.UTC)
	if !m.Allow("acme", 2) {
		t.Error("budget did not reset at the UTC day boundary")
	}
}

// Spec: §4.7.8 — a zero limit is unenforced.
func TestAuditVolumeMeter_ZeroLimitDisablesEnforcement(t *testing.T) {
	m := NewAuditVolumeMeter()
	for i := 0; i < 1000; i++ {
		m.Record("acme")
	}
	if !m.Allow("acme", 0) {
		t.Error("a zero limit must not gate writes")
	}
}

// Spec: §4.7.8 — a nil meter records nothing and allows every write.
func TestAuditVolumeMeter_NilSafe(t *testing.T) {
	var m *AuditVolumeMeter
	m.Record("acme") // must not panic
	if !m.Allow("acme", 0) {
		t.Error("a nil meter must allow")
	}
}

// Spec: §4.7.8 — Allow compares the tenant's count against the limit the
// caller resolved, treats zero and negative limits as unenforced, and keeps
// each tenant's count separate.
func TestAuditVolumeMeter_AllowLimit(t *testing.T) {
	m := NewAuditVolumeMeter()
	fixed := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return fixed }
	m.Record("acme")
	m.Record("acme")
	cases := []struct {
		tenant string
		limit  int64
		want   bool
	}{
		{"acme", 2, false},
		{"acme", 3, true},
		{"acme", 0, true},
		{"acme", -1, true},
		{"globex", 1, true},
	}
	for _, tc := range cases {
		if got := m.Allow(tc.tenant, tc.limit); got != tc.want {
			t.Errorf("Allow(%q, %d) = %v, want %v", tc.tenant, tc.limit, got, tc.want)
		}
	}
}

// Spec: §4.7.8 — a nil meter allows every write whatever the limit.
func TestAuditVolumeMeter_NilAllowsAnyLimit(t *testing.T) {
	var m *AuditVolumeMeter
	for _, limit := range []int64{-1, 0, 1} {
		if !m.Allow("acme", limit) {
			t.Errorf("nil meter Allow(acme, %d) = false, want true", limit)
		}
	}
}
