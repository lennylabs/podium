package server

import (
	"sync"
	"time"
)

// AuditVolumeMeter enforces the §4.7.8 per-tenant audit-volume quota. It counts
// audit events emitted per tenant within the current UTC calendar day and
// reports whether a tenant has reached a daily limit its caller resolves. The
// audit emitter calls Record for every event it writes, keyed on the emitting
// request's tenant; an auditable write operation resolves the tenant's limit
// under the §4.7.8 precedence (EffectiveLimits), calls Allow with it before
// proceeding, and is refused with quota.audit_volume_exceeded once the budget
// is spent. Reads still emit (and count) audit events but are
// not gated, so a spent budget bounds write-driven audit growth without
// dropping events or blocking discovery.
//
// The count resets at the UTC day boundary. Record counts whether or not a
// limit applies, so a tenant whose limit is raised from zero is gated on the
// events it already emitted that day.
type AuditVolumeMeter struct {
	mu    sync.Mutex
	day   string
	count map[string]int64
	now   func() time.Time
}

// NewAuditVolumeMeter returns a meter counting audit events per tenant per UTC
// day.
func NewAuditVolumeMeter() *AuditVolumeMeter {
	return &AuditVolumeMeter{
		count: map[string]int64{},
		now:   time.Now,
	}
}

// rollover resets the per-tenant counts when the UTC day has changed. The
// caller holds m.mu.
func (m *AuditVolumeMeter) rollover() {
	today := m.now().UTC().Format("2006-01-02")
	if today != m.day {
		m.day = today
		m.count = map[string]int64{}
	}
}

// Record counts one audit event against the tenant's current-day budget.
func (m *AuditVolumeMeter) Record(tenant string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollover()
	m.count[tenant]++
}

// Allow reports whether the tenant may perform another auditable write under
// limit, the tenant's resolved daily cap. It is true for a nil meter, for a
// limit of zero or less (unenforced), or when the tenant's current-day count
// is below limit.
//
// Spec: §4.7.8
func (m *AuditVolumeMeter) Allow(tenant string, limit int64) bool {
	if m == nil || limit <= 0 {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollover()
	return m.count[tenant] < limit
}
