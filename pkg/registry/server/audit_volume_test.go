package server

import (
	"testing"
	"time"
)

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

func TestAuditVolumeMeter_ZeroLimitDisablesEnforcement(t *testing.T) {
	m := NewAuditVolumeMeter()
	for i := 0; i < 1000; i++ {
		m.Record("acme")
	}
	if !m.Allow("acme", 0) {
		t.Error("a zero limit must not gate writes")
	}
}

func TestAuditVolumeMeter_NilSafe(t *testing.T) {
	var m *AuditVolumeMeter
	m.Record("acme") // must not panic
	if !m.Allow("acme", 0) {
		t.Error("a nil meter must allow")
	}
}
