package serverboot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// floorFile writes body as a floor file in a temporary directory.
func floorFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "floor.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Spec: §4.7.11 (Tenant trust floor), §13.12 — the trust_floor key and
// PODIUM_LAYER_TRUST_FLOOR_FILE resolve to the floor of a single-tenant
// registry, with the variable taking precedence and an unquoted owner
// identifier read as written.
func TestLoadConfig_TrustFloorSetting(t *testing.T) {
	t.Setenv(layerTrustFloorFileEnv, "")
	key := "registry:\n  trust_floor:\n    mode: warn\n    require_types: [hook]\n    signers:\n      - issuer: i\n        source_repository_owner_id: 42\n"
	c := loadTrustConfig(t, key, "")
	want := store.TrustFloor{Mode: "warn", RequireTypes: []string{"hook"}, Signers: []store.TrustFloorSigner{{Issuer: "i", SourceRepositoryOwnerID: "42"}}}
	if err := c.validate(); err != nil || !reflect.DeepEqual(c.trustFloor.floor, want) || c.trustFloor.display() != "registry.yaml trust_floor" {
		t.Fatalf("key floor = %+v (%s), validate %v", c.trustFloor.floor, c.trustFloor.display(), err)
	}
	path := floorFile(t, `{"mode":"require"}`)
	t.Setenv(layerTrustFloorFileEnv, " "+path+" ")
	c = loadTrustConfig(t, key, "")
	if c.trustFloor.floor.Mode != "require" || c.trustFloor.floor.RequireTypes != nil || !strings.Contains(c.trustFloor.display(), path) {
		t.Errorf("the variable did not take precedence: %+v (%s)", c.trustFloor.floor, c.trustFloor.display())
	}
	t.Setenv(layerTrustFloorFileEnv, "")
	if c = loadTrustConfig(t, "registry: {}\n", ""); !c.trustFloor.floor.IsZero() || c.trustFloor.display() != "(unset)" {
		t.Errorf("unset floor = %+v (%s)", c.trustFloor.floor, c.trustFloor.display())
	}
}

// Spec: §4.7.11 (Tenant trust floor), §13.12 — a floor setting that cannot
// be read or parsed, that holds a key or member of another name, or that
// breaks a floor rule refuses the start with config.invalid_layer_trust,
// naming the setting.
// Matrix: §6.10 (config.invalid_layer_trust)
func TestValidate_TrustFloorRefusals(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.json")
	for _, tc := range []struct {
		name, key, file, want string
	}{
		{"key scalar", "warn", "", "is not a mapping"},
		{"key empty", "", "", "is not a mapping"},
		{"key unknown key", "{mod: warn}", "", `holds the key "mod"`},
		{"key unknown signer key", "{signers: [{issuer: i, owner: o}]}", "", `trust_floor.signers[0] holds the key "owner"`},
		{"key bad mode", "{mode: enforce}", "", `mode "enforce" is not none, warn, or require`},
		{"key unknown instance", "{signers: [{issuer: i, instance: acme-sigstore}]}", "", `names the Sigstore instance "acme-sigstore"`},
		{"key not decodable", "{signers: {issuer: i}}", "", "registry.yaml trust_floor"},
		{"file absent", "", missing, "read:"},
		{"file null", "", "null", "does not hold a JSON object"},
		{"file not JSON", "", "{mode", "invalid character"},
		{"file member case", "", `{"Mode":"warn"}`, `member "Mode" is not one of`},
		{"file unknown type", "", `{"require_types":["hooks"]}`, `require_types[0] "hooks" is not an artifact type`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "registry:\n  trust_floor: " + tc.key + "\n"
			env := ""
			if tc.file != "" {
				body = "registry: {}\n"
				env = missing
				if tc.file != missing {
					env = floorFile(t, tc.file)
				}
			}
			t.Setenv(layerTrustFloorFileEnv, env)
			c := loadTrustConfig(t, body, "")
			err := c.validate()
			if err == nil || !strings.Contains(err.Error(), "config.invalid_layer_trust") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate = %v, want config.invalid_layer_trust with %q", err, tc.want)
			}
			if errs := c.SettingErrors(); len(errs) == 0 {
				t.Errorf("SettingErrors lists no refusal")
			}
		})
	}
}

// Spec: §4.7.11 (Tenant trust floor), §13.12 — either floor setting on a
// multi-tenant registry is refused, because each tenant carries its own.
func TestResolveTrustFloor_MultiTenantRefused(t *testing.T) {
	t.Parallel()
	s := resolveTrustFloor(floorFile(t, `{"mode":"warn"}`), yamlNode(t, ""), true, nil)
	if s.err == nil || !strings.Contains(s.err.Error(), "config.invalid_layer_trust: "+layerTrustFloorFileEnv) ||
		!strings.Contains(s.err.Error(), "multi-tenant") || !strings.Contains(s.display(), "(invalid)") {
		t.Errorf("multi-tenant file = %v (%s)", s.err, s.display())
	}
	if s := resolveTrustFloor("", yamlNode(t, "{mode: warn}"), true, nil); s.err == nil || !strings.Contains(s.err.Error(), "registry.yaml trust_floor") {
		t.Errorf("multi-tenant key = %v", s.err)
	}
}

// listFailStore fails ListLayerConfigs.
type listFailStore struct{ store.Store }

func (listFailStore) ListLayerConfigs(context.Context, string) ([]store.LayerConfig, error) {
	return nil, errors.New("list refused")
}

// Spec: §4.7.11 (Tenant trust floor) — a multi-tenant registry reads each
// tenant's floor from its tenant object, a tenant the store does not hold
// has no floor, and a failed read is an error; a single-tenant registry
// returns the floor setting of the process.
func TestTrustFloors_Sources(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := store.NewMemory()
	floor := store.TrustFloor{Mode: "warn"}
	if err := mem.CreateTenant(ctx, store.Tenant{ID: "acme", Name: "acme", TrustFloor: floor}); err != nil {
		t.Fatal(err)
	}
	fault := storetest.NewFaultStore(mem)
	multi := (&Config{multiTenant: true}).trustFloors(fault)
	got, err := multi(ctx, "acme")
	absent, aerr := multi(ctx, "globex")
	fault.FailMethod("GetTenant")
	_, ferr := multi(ctx, "acme")
	if err != nil || got.Mode != "warn" || aerr != nil || !absent.IsZero() || ferr == nil {
		t.Errorf("multi-tenant floors: %+v %v, absent %+v %v, failed read %v", got, err, absent, aerr, ferr)
	}
	single := (&Config{trustFloor: trustFloorSetting{floor: store.TrustFloor{Mode: "require"}}}).trustFloors(nil)
	if f, err := single(ctx, "any"); err != nil || f.Mode != "require" {
		t.Errorf("single-tenant floor = %+v, %v", f, err)
	}
}

// Spec: §4.7.11 (Policy changes, Tenant trust floor) — the start logs each
// stored layer whose policy the floor makes invalid, with the constraint,
// and none for a layer that meets it or a tenant with no floor; a read that
// fails is logged and refuses nothing.
func TestLogFloorInvalidLayers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := store.NewMemory()
	for _, lc := range []store.LayerConfig{
		{TenantID: "acme", ID: "low", SourceType: "local", TrustMode: "none"},
		{TenantID: "acme", ID: "ok", SourceType: "local", TrustMode: "warn",
			TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: "i", Identity: "alice@acme.com"}}}}},
		{TenantID: "globex", ID: "free", SourceType: "local", TrustMode: "none"},
	} {
		if err := mem.PutLayerConfig(ctx, lc); err != nil {
			t.Fatal(err)
		}
	}
	floors := func(_ context.Context, tenant string) (store.TrustFloor, error) {
		switch tenant {
		case "acme", "listfail":
			return store.TrustFloor{Mode: "warn"}, nil
		case "fail":
			return store.TrustFloor{}, errors.New("floor refused")
		}
		return store.TrustFloor{}, nil
	}
	var lines []string
	logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	logFloorInvalidLayers(ctx, mem, []string{"acme", "globex", "fail"}, floors, logf)
	logFloorInvalidLayers(ctx, listFailStore{mem}, []string{"listfail"}, floors, logf)
	if len(lines) != 3 || !strings.Contains(lines[0], "layer low (tenant acme) does not meet the tenant trust floor (below_floor)") ||
		!strings.Contains(lines[1], "trust floor of tenant fail") || !strings.Contains(lines[2], "list the layers of tenant listfail") {
		t.Errorf("start log:\n%s", strings.Join(lines, "\n"))
	}
}

// Spec: §4.7.11 (Re-evaluation), §7.3.3 — the floor change hook
// re-evaluates each layer of the tenant and survives a list that fails.
func TestTenantFloorChangeHook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := store.NewMemory()
	if err := mem.PutLayerConfig(ctx, store.LayerConfig{TenantID: "acme", ID: "a", SourceType: "local", TrustMode: "none"}); err != nil {
		t.Fatal(err)
	}
	read := 0
	att := &ingest.AttestationConfig{Floors: func(context.Context, string) (store.TrustFloor, error) {
		read++
		return store.TrustFloor{}, errors.New("floor refused")
	}}
	tenantFloorChangeHook(mem, att, nil, nil)(ctx, "acme", "olivia@acme.com")
	tenantFloorChangeHook(listFailStore{mem}, att, nil, nil)(ctx, "acme", "olivia@acme.com")
	if read != 1 {
		t.Errorf("the hook read the floor %d times, want once for the one layer", read)
	}
}

// yamlNode parses body as the value node of a trust_floor key; an empty
// body is no node.
func yamlNode(t *testing.T, body string) yaml.Node {
	t.Helper()
	var doc yaml.Node
	if body == "" {
		return doc
	}
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	return *doc.Content[0]
}
