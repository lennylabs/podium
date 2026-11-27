package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// recordView renders the attestation record of a layer read in one
// comparable string.
func recordView(cfg store.LayerConfig) string {
	r := cfg.AttestationRecord
	mark, checked := "-", "-"
	if r.Mark != nil {
		mark = r.Mark.UTC().Format(time.RFC3339Nano)
	}
	if r.CheckedAt != nil {
		checked = r.CheckedAt.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%s|%s|%s|%v|%d|%s", mark, r.Commit, r.Status, r.Details, r.Dropped, checked)
}

// layerAttestationRecord covers the §4.7.11 attestation record: the
// conditions of a cycle's record write, the record kept by every layer write
// whatever the caller passes, over a live row and a soft-deleted one, and the
// mark reset written with the claim.
//
// Spec: §4.7.11 (Attestation record, Mark reset), §7.3.1
func layerAttestationRecord(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")
	t1 := time.Date(2026, 11, 20, 9, 14, 2, 123456789, time.UTC)
	t2, at := t1.Add(time.Hour), t1.Add(2*time.Hour)
	must(t, s.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: "/x"}))
	_, _, err := s.ClaimLayerIngest(ctx, "t", "L", "c1")
	must(t, err)
	var got []string
	write := func(claim string, w store.AttestationWrite) {
		err := s.WriteLayerAttestation(ctx, "t", "L", claim, w)
		cfg, rerr := s.GetLayerConfig(ctx, "t", "L")
		must(t, rerr)
		got = append(got, fmt.Sprintf("%v %s", errors.Is(err, store.ErrIngestSuperseded), recordView(cfg)))
	}
	pass := store.AttestationWrite{Status: "ok", CheckedAt: at, Advance: true, Mark: t1, Commit: "aaa"}
	write("c0", pass) // another claim
	write("c1", pass)
	write("c1", pass) // the stored mark moved since the read
	write("c1", store.AttestationWrite{Status: "ingest.attestation_stale", Details: map[string]any{"constraint": "mark", "n": 2}, Dropped: 3, CheckedAt: at})
	mark := t1.Truncate(time.Microsecond)
	write("c1", store.AttestationWrite{Status: "ok", CheckedAt: at, Advance: true, Mark: t2, ReadMark: &mark, ReadCommit: "aaa"})
	// A layer write keeps the record whatever it carries, on a live row and,
	// through an unregister and a restore, on a soft-deleted one.
	must(t, s.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: "/y",
		AttestationRecord: store.AttestationRecord{Status: "forged", Commit: "zzz"}}))
	must(t, s.DeleteLayerConfig(ctx, "t", "L"))
	must(t, s.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: "/z", DeletedAt: &at}))
	must(t, s.RestoreLayerConfig(ctx, "t", "L"))
	kept, err := s.GetLayerConfig(ctx, "t", "L")
	must(t, err)
	got = append(got, recordView(kept))
	// The reset claims the layer, supersedes the earlier claim, and returns
	// the record it replaced.
	claimed, _, prior, err := s.ClaimLayerIngestWithReset(ctx, "t", "L", "c2", "bbb")
	must(t, err)
	got = append(got, recordView(store.LayerConfig{AttestationRecord: prior}), recordView(claimed))
	write("c1", store.AttestationWrite{Status: "ok", CheckedAt: at})
	if _, _, _, err := s.ClaimLayerIngestWithReset(ctx, "t", "absent", "c", ""); !errors.Is(err, store.ErrNotFound) {
		got = append(got, "reset of an absent layer: "+fmt.Sprint(err))
	}
	m2 := "2026-11-20T10:14:02.123456Z"
	m1 := "2026-11-20T09:14:02.123456Z"
	checkedAt := at.Truncate(time.Microsecond).Format(time.RFC3339Nano)
	want := []string{
		"true -|||map[]|0|-",
		"false " + m1 + "|aaa|ok|map[]|0|" + checkedAt,
		"true " + m1 + "|aaa|ok|map[]|0|" + checkedAt,
		"false " + m1 + "|aaa|ingest.attestation_stale|map[constraint:mark n:2]|3|" + checkedAt,
		"false " + m2 + "|aaa|ok|map[]|0|" + checkedAt,
		m2 + "|aaa|ok|map[]|0|" + checkedAt,
		m2 + "|aaa|ok|map[]|0|" + checkedAt,
		"-|bbb|ok|map[]|0|" + checkedAt,
		"true -|bbb|ok|map[]|0|" + checkedAt,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("records:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// layerAttestationMigration covers the §13.4 copy of the mark and the commit:
// a target with no row, a target record a cycle wrote, and a record no cycle
// wrote with an earlier mark, a later mark, and a later mark with no commit.
//
// Spec: §13.4, §4.7.11 (Migration copy)
func layerAttestationMigration(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")
	t1 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	t2, t3 := t1.Add(time.Hour), t1.Add(2*time.Hour)
	layer := func(id string, mark *time.Time, commit string) store.LayerConfig {
		return store.LayerConfig{TenantID: "t", ID: id, SourceType: "local", LocalPath: "/x",
			AttestationRecord: store.AttestationRecord{Mark: mark, Commit: commit}}
	}
	var got []string
	migrate := func(cfg store.LayerConfig) {
		out, err := s.MigrateLayerConfig(ctx, cfg)
		must(t, err)
		read, err := s.GetLayerConfig(ctx, "t", cfg.ID)
		must(t, err)
		got = append(got, string(out)+" "+recordView(read))
	}
	migrate(layer("A", &t1, "aaa"))
	migrate(layer("A", &t2, "bbb"))
	migrate(layer("A", &t1, "ccc"))
	migrate(layer("A", &t3, ""))
	migrate(layer("B", &t1, "aaa"))
	_, _, err := s.ClaimLayerIngest(ctx, "t", "B", "c")
	must(t, err)
	must(t, s.WriteLayerAttestation(ctx, "t", "B", "c", store.AttestationWrite{Status: "ok", CheckedAt: t1}))
	migrate(layer("B", &t2, "bbb"))
	want := strings.Join([]string{
		"written 2026-11-20T09:00:00Z|aaa||map[]|0|-",
		"replaced 2026-11-20T10:00:00Z|bbb||map[]|0|-",
		"left 2026-11-20T10:00:00Z|bbb||map[]|0|-",
		"left 2026-11-20T10:00:00Z|bbb||map[]|0|-",
		"written 2026-11-20T09:00:00Z|aaa||map[]|0|-",
		"left 2026-11-20T09:00:00Z|aaa|ok|map[]|0|2026-11-20T09:00:00Z",
	}, "\n")
	if strings.Join(got, "\n") != want {
		t.Errorf("copies:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}
