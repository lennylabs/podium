package server

import (
	"sync"
	"time"
)

// AuditVolumeMeter enforces the §4.7.8 per-tenant audit-volume quota. It counts
// audit events emitted per tenant within the current UTC calendar day and
// reports when a tenant has reached its configured daily budget. The audit
// emitter calls Record for every event it writes; an auditable write operation
// calls Allow before proceeding and is refused with quota.audit_volume_exceeded
// once the budget is spent. Reads still emit (and count) audit events but are
// not gated, so a spent budget bounds write-driven audit growth without
// dropping events or blocking discovery.
//
// The count resets at the UTC day boundary. A limit of zero disables the quota
// (Allow always returns true) while Record still maintains the count, so a
// deployment can observe volume without enforcing a cap.
type AuditVolumeMeter struct {
	mu    sync.Mutex
	limit int64
	day   string
	count map[string]int64
	now   func() time.Time
}

// NewAuditVolumeMeter returns a meter enforcing limit audit events per tenant
// per UTC day. A non-positive limit disables enforcement.
func NewAuditVolumeMeter(limit int64) *AuditVolumeMeter {
	return &AuditVolumeMeter{
		limit: limit,
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

// Allow reports whether the tenant may perform another auditable write. It is
// true when enforcement is disabled (limit <= 0) or the tenant's current-day
// count is below the limit.
func (m *AuditVolumeMeter) Allow(tenant string) bool {
	if m == nil || m.limit <= 0 {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollover()
	return m.count[tenant] < m.limit
}
