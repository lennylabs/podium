package serverboot

import (
	"context"
	"errors"
	"testing"

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
