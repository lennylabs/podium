package serverboot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// Spec: §8.7, §13.12 — the usage settings default to 1m, 365 days, and 32
// client keys, accept a duration of at least 1s, a positive integer, and an
// integer from 1 to 1000, and refuse anything else with config.invalid naming
// the variable.
func TestUsageConfigFromEnv(t *testing.T) {
	t.Parallel()
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	def := usageConfigFromEnv(env(nil))
	if def.flushInterval != time.Minute || def.retentionDays != 365 || def.maxClientKeys != 32 || def.err != nil {
		t.Errorf("defaults = %+v", def)
	}
	set := usageConfigFromEnv(env(map[string]string{"PODIUM_USAGE_FLUSH_INTERVAL": " 5s ", "PODIUM_USAGE_RETENTION_DAYS": "90", "PODIUM_USAGE_MAX_CLIENT_KEYS": "1000"}))
	if set.flushInterval != 5*time.Second || set.retentionDays != 90 || set.maxClientKeys != 1000 || set.err != nil {
		t.Errorf("set = %+v", set)
	}
	for name, kv := range map[string]map[string]string{
		"PODIUM_USAGE_FLUSH_INTERVAL":  {"PODIUM_USAGE_FLUSH_INTERVAL": "500ms"},
		"PODIUM_USAGE_RETENTION_DAYS":  {"PODIUM_USAGE_RETENTION_DAYS": "0"},
		"PODIUM_USAGE_MAX_CLIENT_KEYS": {"PODIUM_USAGE_MAX_CLIENT_KEYS": "1001"},
	} {
		c := usageConfigFromEnv(env(kv))
		if c.err == nil || !strings.HasPrefix(c.err.Error(), "config.invalid: "+name) {
			t.Errorf("%v: err = %v, want config.invalid naming %s", kv, c.err, name)
		}
	}
	for _, bad := range []string{"soon", "-1m"} {
		if c := usageConfigFromEnv(env(map[string]string{"PODIUM_USAGE_FLUSH_INTERVAL": bad})); c.err == nil {
			t.Errorf("flush interval %q accepted", bad)
		}
	}
	if c := usageConfigFromEnv(env(map[string]string{"PODIUM_USAGE_RETENTION_DAYS": "ten"})); c.err == nil {
		t.Errorf("retention %q accepted", "ten")
	}
	for _, bad := range []string{"0", "many"} {
		if c := usageConfigFromEnv(env(map[string]string{"PODIUM_USAGE_MAX_CLIENT_KEYS": bad})); c.err == nil {
			t.Errorf("max client keys %q accepted", bad)
		}
	}
}

// Spec: §8.7 — the retention pass at startup deletes the rows of every day
// before the last retentionDays days, and the flush loop adds buffered counts
// to the store and flushes once more at shutdown.
func TestStartUsageCounts_RetentionAndFlush(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	row := func(day string) store.UsageCount {
		return store.UsageCount{Day: day, Layer: "team", ArtifactID: "x", Client: "mcp", Harness: "none", Loads: 1}
	}
	if err := st.AddUsageCounts(ctx, "t", []store.UsageCount{row("2026-11-08"), row("2026-11-09"), row("2026-11-18")}); err != nil {
		t.Fatal(err)
	}
	now := clock.NewFrozen(time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC))
	counter := core.NewUsageCounter(now)
	reg := core.New(st, "t", nil).WithUsageCounts(counter)
	final := startUsageCounts(ctx, usageConfig{flushInterval: 10 * time.Millisecond, retentionDays: 10}, st, counter, now)
	rows, _ := st.ListUsageCounts(ctx, "t", "2026-01-01", "2026-12-31")
	if len(rows) != 2 || rows[0].Day != "2026-11-09" {
		t.Fatalf("rows after retention = %+v, want 2026-11-09 and 2026-11-18", rows)
	}
	if err := st.PutManifest(ctx, storetest.Seal(t, store.ManifestRecord{TenantID: "t", ArtifactID: "x", Version: "1.0.0", ContentHash: "sha256:x", Type: "agent", Body: []byte("b"), Layer: "team"}, nil, nil)); err != nil {
		t.Fatal(err)
	}
	loads := func() int64 {
		rows, _ := st.ListUsageCounts(context.Background(), "t", "2026-11-18", "2026-11-18")
		var n int64
		for _, r := range rows {
			n += r.Loads
		}
		return n
	}
	load := func() {
		if _, err := reg.LoadArtifact(context.Background(), layer.Identity{IsPublic: true}, "x", core.LoadArtifactOptions{}); err != nil {
			t.Fatalf("LoadArtifact: %v", err)
		}
	}
	load()
	waitFor(t, func() bool { return loads() == 2 })
	cancel()
	time.Sleep(30 * time.Millisecond)
	load()
	final()
	if n := loads(); n != 3 {
		t.Errorf("loads after the final flush = %d, want 3", n)
	}
}

// waitFor polls cond for up to two seconds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
