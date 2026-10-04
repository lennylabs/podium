package serverboot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// A spent audit-volume budget makes the §7.3.1 reingest runner refuse the
// write with quota.audit_volume_exceeded before it touches the source.
func TestReingestRunner_RefusesWhenAuditBudgetSpent(t *testing.T) {
	meter := server.NewAuditVolumeMeter()
	meter.Record("default") // spend the single-event budget

	runner := buildReingestRunner(nil, nil, &Config{auditVolumePerDay: 1}, nil, nil, nil, nil, nil, meter, false, collocatedVectorIngest{})
	_, err := runner(context.Background(), store.LayerConfig{TenantID: "default", SourceType: "git"}, nil)
	if !errors.Is(err, ingest.ErrAuditVolumeExceeded) {
		t.Fatalf("err = %v, want ErrAuditVolumeExceeded", err)
	}
}

// A zero (disabled) budget lets the runner past the audit gate, so any error is
// not the audit-volume one (here it reaches source resolution).
func TestReingestRunner_AuditBudgetDisabledPassesGate(t *testing.T) {
	meter := server.NewAuditVolumeMeter()
	runner := buildReingestRunner(nil, nil, &Config{}, nil, nil, nil, nil, nil, meter, false, collocatedVectorIngest{})
	_, err := runner(context.Background(), store.LayerConfig{TenantID: "default", SourceType: "nonsense"}, nil)
	if errors.Is(err, ingest.ErrAuditVolumeExceeded) {
		t.Fatalf("disabled budget should not gate; got audit-volume error: %v", err)
	}
}

// faultingTenantStore wraps a memory store and fails every GetTenant with err,
// standing in for a store fault at the multi-tenant reingest gate.
type faultingTenantStore struct {
	store.Store
	err error
}

func (s faultingTenantStore) GetTenant(context.Context, string) (store.Tenant, error) {
	return store.Tenant{}, s.err
}

// newQuotaTenantStore returns a memory store holding tenant A with an
// audit-volume quota of 1 and tenant B with a zero quota.
func newQuotaTenantStore(t *testing.T, a, b string) store.Store {
	t.Helper()
	st := store.NewMemory()
	for _, tn := range []store.Tenant{
		{ID: a, Name: "acme", Quota: store.Quota{AuditVolumePerDay: 1}},
		{ID: b, Name: "globex"},
	} {
		if err := st.CreateTenant(t.Context(), tn); err != nil {
			t.Fatalf("CreateTenant(%s): %v", tn.ID, err)
		}
	}
	return st
}

// On a multi-tenant registry the reingest gate keys on the stored layer's
// tenant and resolves that tenant's limit from its record: A's own limit of 1
// refuses A, B's zero quota selects the deployment default, which admits B
// when the default is 0 and refuses B when the default is 1.
//
// Spec: §4.7.8
func TestReingestRunner_AuditGatePerTenantLimitAndKey(t *testing.T) {
	t.Parallel()
	const a, b = "tenant-a", "tenant-b"
	st := newQuotaTenantStore(t, a, b)
	meter := server.NewAuditVolumeMeter()
	meter.Record(a)
	meter.Record(b)

	runner := buildReingestRunner(st, nil, &Config{multiTenant: true}, nil, nil, nil, nil, nil, meter, false, collocatedVectorIngest{})
	_, err := runner(t.Context(), store.LayerConfig{TenantID: a, SourceType: "nonsense"}, nil)
	if !errors.Is(err, ingest.ErrAuditVolumeExceeded) || !strings.Contains(err.Error(), a) {
		t.Fatalf("tenant A: err = %v, want ErrAuditVolumeExceeded naming %s", err, a)
	}
	_, err = runner(t.Context(), store.LayerConfig{TenantID: b, SourceType: "nonsense"}, nil)
	if err == nil || errors.Is(err, ingest.ErrAuditVolumeExceeded) {
		t.Fatalf("tenant B with a zero default: err = %v, want a post-gate error", err)
	}

	runner = buildReingestRunner(st, nil, &Config{multiTenant: true, auditVolumePerDay: 1}, nil, nil, nil, nil, nil, meter, false, collocatedVectorIngest{})
	_, err = runner(t.Context(), store.LayerConfig{TenantID: b, SourceType: "nonsense"}, nil)
	if !errors.Is(err, ingest.ErrAuditVolumeExceeded) {
		t.Fatalf("tenant B with a default of 1: err = %v, want ErrAuditVolumeExceeded", err)
	}
}

// A tenant read that fails at the multi-tenant reingest gate refuses the
// write before the source provider is resolved, whether the store faults or
// the tenant record is missing (D6 table). The refusal wraps the store error
// rather than the audit-volume sentinel.
//
// Spec: §4.7.8
func TestReingestRunner_AuditGateTenantReadFault(t *testing.T) {
	t.Parallel()
	t.Run("store fault", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("tenant read failed")
		st := faultingTenantStore{Store: store.NewMemory(), err: boom}
		runner := buildReingestRunner(st, nil, &Config{multiTenant: true}, nil, nil, nil, nil, nil, server.NewAuditVolumeMeter(), false, collocatedVectorIngest{})
		_, err := runner(t.Context(), store.LayerConfig{TenantID: "tenant-a", SourceType: "nonsense"}, nil)
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want it to wrap the store error", err)
		}
		if errors.Is(err, ingest.ErrAuditVolumeExceeded) || errors.Is(err, source.ErrInvalidConfig) {
			t.Fatalf("err = %v, want the gate to refuse before the audit check and provider resolution", err)
		}
	})
	t.Run("missing tenant", func(t *testing.T) {
		t.Parallel()
		runner := buildReingestRunner(store.NewMemory(), nil, &Config{multiTenant: true, auditVolumePerDay: 1}, nil, nil, nil, nil, nil, server.NewAuditVolumeMeter(), false, collocatedVectorIngest{})
		_, err := runner(t.Context(), store.LayerConfig{TenantID: "tenant-c", SourceType: "nonsense"}, nil)
		if !errors.Is(err, store.ErrTenantNotFound) {
			t.Fatalf("err = %v, want it to wrap store.ErrTenantNotFound", err)
		}
		if errors.Is(err, ingest.ErrAuditVolumeExceeded) {
			t.Fatalf("err = %v, want a read refusal rather than the audit-volume error", err)
		}
	})
}

// A single-tenant registry resolves the reingest gate's limit from the
// deployment default alone and reads no tenant record, so a nil store is
// never dereferenced (D11).
//
// Spec: §4.7.8
func TestReingestRunner_AuditGateSingleTenantNoRead(t *testing.T) {
	t.Parallel()
	meter := server.NewAuditVolumeMeter()
	meter.Record("default")
	runner := buildReingestRunner(nil, nil, &Config{auditVolumePerDay: 1}, nil, nil, nil, nil, nil, meter, false, collocatedVectorIngest{})
	_, err := runner(t.Context(), store.LayerConfig{TenantID: "default", SourceType: "git"}, nil)
	if !errors.Is(err, ingest.ErrAuditVolumeExceeded) {
		t.Fatalf("err = %v, want ErrAuditVolumeExceeded", err)
	}
}
