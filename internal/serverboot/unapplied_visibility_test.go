package serverboot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// recordLines returns a log function that records each formatted line.
func recordLines(lines *[]string) func(string, ...any) {
	return func(format string, args ...any) {
		*lines = append(*lines, fmt.Sprintf(format, args...))
	}
}

// seedTenantLayers creates the tenant and stores the given layers in it.
func seedTenantLayers(t *testing.T, st store.Store, tenant store.Tenant, layers ...store.LayerConfig) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("CreateTenant %s: %v", tenant.ID, err)
	}
	for i, l := range layers {
		l.TenantID = tenant.ID
		l.SourceType = "local"
		l.LocalPath = "/tmp/" + l.ID
		l.Order = (i + 1) * 10
		if err := st.PutLayerConfig(ctx, l); err != nil {
			t.Fatalf("PutLayerConfig %s: %v", l.ID, err)
		}
	}
}

// Spec: §4.6 (No-identity bypass), §13.10 — the registry applies no layer
// visibility in public mode and whenever no request-time verifier is wired,
// whatever provider label the configuration names.
func TestAppliesNoVisibility(t *testing.T) {
	t.Parallel()
	cases := []struct {
		publicMode, verifier, want bool
	}{
		{false, false, true},
		{true, false, true},
		{false, true, false},
	}
	for _, tc := range cases {
		if got := appliesNoVisibility(tc.publicMode, tc.verifier); got != tc.want {
			t.Errorf("appliesNoVisibility(public=%v, verifier=%v) = %v, want %v", tc.publicMode, tc.verifier, got, tc.want)
		}
	}
}

// Spec: §13.10 (Startup warning for unapplied visibility) — one line names
// how many stored layers set no public grant and lists their IDs; a public
// layer is not listed, and a store whose layers are all public logs nothing.
func TestLogUnappliedVisibility_SingleTenant(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	seedTenantLayers(t, st, store.Tenant{ID: "t1", Name: "default"},
		store.LayerConfig{ID: "open", Public: true},
		store.LayerConfig{ID: "team-finance", Groups: []string{"acme-finance"}},
		store.LayerConfig{ID: "alice-personal", UserDefined: true, Owner: "alice@acme.com", Users: []string{"alice@acme.com"}},
		store.LayerConfig{ID: "sealed"},
	)
	var lines []string
	logUnappliedVisibility(context.Background(), st, recordLines(&lines))
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want 1: %q", len(lines), lines)
	}
	line := lines[0]
	for _, want := range []string{
		"warning: 3 stored layer(s) declare visibility other than public",
		"applies no layer visibility because it verifies no caller, so every caller sees them",
		"take effect once an identity provider is configured",
		"team-finance", "alice-personal", "sealed",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "open") || strings.Contains(line, "(tenant") {
		t.Errorf("line %q lists the public layer or a tenant on a single-tenant store", line)
	}

	public := store.NewMemory()
	seedTenantLayers(t, public, store.Tenant{ID: "t1"}, store.LayerConfig{ID: "open", Public: true})
	lines = nil
	logUnappliedVisibility(context.Background(), public, recordLines(&lines))
	if len(lines) != 0 {
		t.Errorf("an all-public store logged %q, want nothing", lines)
	}
}

// Spec: §13.10 (Startup warning for unapplied visibility) — on a store that
// holds more than one tenant, each listed ID names its tenant, by name or by
// ID for a tenant with no name.
func TestLogUnappliedVisibility_QualifiesTenants(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	seedTenantLayers(t, st, store.Tenant{ID: "id-acme", Name: "acme"}, store.LayerConfig{ID: "team"})
	seedTenantLayers(t, st, store.Tenant{ID: "id-globex"}, store.LayerConfig{ID: "team", Organization: true})
	var lines []string
	logUnappliedVisibility(context.Background(), st, recordLines(&lines))
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want 1: %q", len(lines), lines)
	}
	for _, want := range []string{"2 stored layer(s)", "team (tenant acme)", "team (tenant id-globex)"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("line %q lacks %q", lines[0], want)
		}
	}
}

// Spec: §13.10 (Startup warning for unapplied visibility) — the line lists at
// most 20 IDs and summarizes the rest as "and N more".
func TestLogUnappliedVisibility_CapsTheList(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	var layers []store.LayerConfig
	for i := range unappliedVisibilityListCap + 5 {
		layers = append(layers, store.LayerConfig{ID: fmt.Sprintf("layer-%02d", i)})
	}
	seedTenantLayers(t, st, store.Tenant{ID: "t1"}, layers...)
	var lines []string
	logUnappliedVisibility(context.Background(), st, recordLines(&lines))
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want 1: %q", len(lines), lines)
	}
	line := lines[0]
	if !strings.Contains(line, "25 stored layer(s)") || !strings.HasSuffix(line, "layer-19, and 5 more") {
		t.Errorf("line %q, want 25 counted and the list ending at layer-19, and 5 more", line)
	}
	if strings.Contains(line, "layer-20") {
		t.Errorf("line %q lists an ID past the cap", line)
	}
}

// failingLayerStore fails the store reads the startup warning makes.
type failingLayerStore struct {
	store.Store
	tenantsErr, layersErr error
}

func (f failingLayerStore) ListTenants(ctx context.Context) ([]store.Tenant, error) {
	if f.tenantsErr != nil {
		return nil, f.tenantsErr
	}
	return f.Store.ListTenants(ctx)
}

func (f failingLayerStore) ListLayerConfigs(ctx context.Context, tenantID string) ([]store.LayerConfig, error) {
	if f.layersErr != nil {
		return nil, f.layersErr
	}
	return f.Store.ListLayerConfigs(ctx, tenantID)
}

// Spec: §13.10 (Startup warning for unapplied visibility) — the warning
// refuses nothing, so a store read that fails is logged as a warning and
// the function returns.
func TestLogUnappliedVisibility_StoreFailureIsLogged(t *testing.T) {
	t.Parallel()
	base := store.NewMemory()
	seedTenantLayers(t, base, store.Tenant{ID: "t1"}, store.LayerConfig{ID: "team"})
	boom := errors.New("store offline")
	for _, st := range []failingLayerStore{
		{Store: base, tenantsErr: boom},
		{Store: base, layersErr: boom},
	} {
		var lines []string
		logUnappliedVisibility(context.Background(), st, recordLines(&lines))
		if len(lines) != 1 || !strings.Contains(lines[0], "could not list the stored layers") || !strings.Contains(lines[0], "store offline") {
			t.Errorf("logged %q, want one line reporting the store failure", lines)
		}
	}
}
