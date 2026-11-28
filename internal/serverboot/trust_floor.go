package serverboot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// layerTrustFloorFileEnv names the file that holds the §4.7.11 trust floor
// of a single-tenant registry's default org, and trustFloorKey the
// registry.yaml key that holds the same object.
const (
	layerTrustFloorFileEnv = "PODIUM_LAYER_TRUST_FLOOR_FILE"
	trustFloorKey          = "trust_floor"
)

// trustFloorSetting is the resolved floor setting: the floor, the setting
// it came from for the error and the config display, and the refusal of a
// value the registry cannot use.
type trustFloorSetting struct {
	floor  store.TrustFloor
	source string
	err    error
}

// yamlTrustFloor is the registry.yaml trust_floor object, in the form of
// the floor file.
type yamlTrustFloor struct {
	Mode         string            `yaml:"mode"`
	RequireTypes []string          `yaml:"require_types"`
	Signers      []yamlFloorSigner `yaml:"signers"`
}

// yamlFloorSigner is one floor entry of the trust_floor key. The identifier
// member is a string, and an unquoted number decodes into one as written.
type yamlFloorSigner struct {
	Issuer                  string `yaml:"issuer"`
	Instance                string `yaml:"instance"`
	SourceRepositoryOwner   string `yaml:"source_repository_owner"`
	SourceRepositoryOwnerID string `yaml:"source_repository_owner_id"`
	SourceRepositoryRef     string `yaml:"source_repository_ref"`
	Identity                string `yaml:"identity"`
}

// The keys of the trust_floor object and of one of its entries.
var (
	floorKeys       = []string{"mode", "require_types", "signers"}
	floorSignerKeys = []string{"issuer", "instance", "source_repository_owner", "source_repository_owner_id", "source_repository_ref", "identity"}
)

// resolveTrustFloor resolves the floor setting of a single-tenant registry:
// the JSON file path names, which takes precedence, or the registry.yaml
// trust_floor node. A value that cannot be read or parsed, that holds a key
// or member of another name, or that breaks a §4.7.11 floor rule is refused
// with config.invalid_layer_trust, and so is either setting on a
// multi-tenant registry, whose tenants carry their own floors. An unset
// setting is no floor.
//
// Spec: §4.7.11 (Tenant trust floor), §13.12
func resolveTrustFloor(path string, node yaml.Node, multiTenant bool, known layer.TrustInstances) trustFloorSetting {
	path = strings.TrimSpace(path)
	var s trustFloorSetting
	var err error
	switch {
	case path != "":
		s.source = layerTrustFloorFileEnv + " " + path
		s.floor, err = readFloorFile(path)
	case node.Kind != 0:
		s.source = "registry.yaml " + trustFloorKey
		s.floor, err = decodeFloorNode(&node)
	default:
		return s
	}
	if err == nil && multiTenant {
		err = errors.New("is set on a multi-tenant registry (PODIUM_MULTI_TENANT), where each tenant carries its own trust floor in its tenant object; set the floor with PATCH /v1/admin/tenants/{id}")
	}
	if err == nil {
		err = layer.CheckTrustFloor(s.floor, known)
	}
	if err != nil {
		s.err = fmt.Errorf("config.invalid_layer_trust: %s: %w", s.source, err)
	}
	return s
}

// readFloorFile reads a floor file: one JSON object whose members are those
// of the floor (store.TrustFloor.UnmarshalJSON refuses another member, a
// member in another letter case, and a member given twice). A value that is
// not an object, such as null, is refused rather than read as no floor.
func readFloorFile(path string) (store.TrustFloor, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return store.TrustFloor{}, fmt.Errorf("read: %w", err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		return store.TrustFloor{}, errors.New("the file does not hold a JSON object")
	}
	var f store.TrustFloor
	if err := json.Unmarshal(b, &f); err != nil {
		return store.TrustFloor{}, err
	}
	return f.Clone(), nil
}

// decodeFloorNode decodes the trust_floor key. A value that is not a
// mapping, an empty value included, and a key of the object or of an entry
// that is not one the floor defines, are refused with the key's line, so a
// misspelled owner cannot widen an entry. yaml.v3 refuses a key given twice.
func decodeFloorNode(node *yaml.Node) (store.TrustFloor, error) {
	n := resolveAlias(node)
	if n.Kind != yaml.MappingNode {
		return store.TrustFloor{}, fmt.Errorf("the value (line %d) is not a mapping", n.Line)
	}
	if err := checkKeys(trustFloorKey, n, floorKeys); err != nil {
		return store.TrustFloor{}, err
	}
	for j := 0; j+1 < len(n.Content); j += 2 {
		signers := resolveAlias(n.Content[j+1])
		if n.Content[j].Value != "signers" || signers.Kind != yaml.SequenceNode {
			continue
		}
		for k, e := range signers.Content {
			if err := checkKeys(fmt.Sprintf("%s.signers[%d]", trustFloorKey, k), resolveAlias(e), floorSignerKeys); err != nil {
				return store.TrustFloor{}, err
			}
		}
	}
	var y yamlTrustFloor
	if err := n.Decode(&y); err != nil {
		return store.TrustFloor{}, err
	}
	f := store.TrustFloor{Mode: y.Mode, RequireTypes: y.RequireTypes}
	for _, e := range y.Signers {
		f.Signers = append(f.Signers, store.TrustFloorSigner(e))
	}
	return f.Clone(), nil
}

// display is the value `podium config show` prints: the setting the floor
// came from, marked when it is refused.
func (s trustFloorSetting) display() string {
	if s.err != nil {
		return s.source + " (invalid)"
	}
	if s.source == "" {
		return "(unset)"
	}
	return s.source
}

// trustFloors returns the §4.7.11 floor source of the registry: the tenant
// object's floor on a multi-tenant registry, read from st on each call, and
// the floor setting of the process on a single-tenant one. A tenant the
// store does not hold has no record that could carry a floor, so it reads
// as no floor; any other read failure is returned, and every caller fails
// closed on it.
//
// Spec: §4.7.11 (Tenant trust floor)
func (c *Config) trustFloors(st store.Store) layer.TrustFloors {
	if c.multiTenant {
		return func(ctx context.Context, tenantID string) (store.TrustFloor, error) {
			t, err := st.GetTenant(ctx, tenantID)
			switch {
			case errors.Is(err, store.ErrTenantNotFound):
				return store.TrustFloor{}, nil
			case err != nil:
				return store.TrustFloor{}, err
			}
			return t.TrustFloor, nil
		}
	}
	floor := c.trustFloor.floor
	return func(context.Context, string) (store.TrustFloor, error) { return floor.Clone(), nil }
}

// logFloorInvalidLayers logs the §4.7.11 start warning for each stored layer
// of tenants whose trust policy does not meet its tenant's floor, with the
// rule it breaks. Ingest refuses such a layer's cycles, and serving reports
// its verified versions as failed; the warning names it before any cycle
// runs. A failed read is logged and the start proceeds, because the
// warning refuses nothing. The trust default is not passed, so a layer that
// only the default leaves with no requirement is left to
// logUnrequiredTrustLayers.
//
// Spec: §4.7.11 (Policy changes, Tenant trust floor)
func logFloorInvalidLayers(ctx context.Context, st store.Store, tenants []string, floors layer.TrustFloors, logf func(string, ...any)) {
	for _, tenant := range tenants {
		floor, err := floors.Floor(ctx, tenant)
		if err != nil {
			logf("warning: layer trust: could not read the trust floor of tenant %s: %v", tenant, err)
			continue
		}
		if floor.IsZero() {
			continue
		}
		layers, err := st.ListLayerConfigs(ctx, tenant)
		if err != nil {
			logf("warning: layer trust: could not list the layers of tenant %s: %v", tenant, err)
			continue
		}
		for _, l := range layers {
			if r := layer.CheckFloor(l, floor, ""); r != nil {
				logf("warning: layer trust: layer %s (tenant %s) does not meet the tenant trust floor (%s): %s; its ingest cycles are refused with ingest.attestation_policy_invalid until its policy meets the floor",
					l.ID, tenant, r.Constraint, r.Message)
			}
		}
	}
}

// tenantFloorChangeHook returns the re-evaluation a tenant update runs after
// it changed the tenant's floor: every live layer of the tenant is
// re-evaluated as after a trust policy change, because the floor decides the
// mode an empty trust_mode resolves to. A failure is logged and changes
// nothing the request stored, because each layer's next ingest cycle runs
// the re-evaluation again. clk dates the outcomes a downgrade writes.
//
// Spec: §4.7.11 (Re-evaluation), §7.3.3
func tenantFloorChangeHook(st store.Store, att *ingest.AttestationConfig, sink audit.Sink, scrubber *audit.PIIScrubber, clk clock.Clock) func(context.Context, string, string) {
	return func(ctx context.Context, tenantID, caller string) {
		layers, err := st.ListLayerConfigs(ctx, tenantID)
		if err != nil {
			log.Printf("attestation: tenant %s: list the layers to re-evaluate after a trust floor change: %v; each layer's next ingest cycle runs it", tenantID, err)
			return
		}
		var emit ingest.AuditEmitterFunc
		if sink != nil {
			emit = ingestAuditEmitter(ctx, sink, scrubber, caller, tenantID)
		}
		for _, lc := range layers {
			if err := att.Reevaluate(ctx, st, lc, clk, emit); err != nil {
				log.Printf("attestation: layer %s (tenant %s): re-evaluate the links after a trust floor change: %v; the next ingest cycle of the layer runs it again", lc.ID, tenantID, err)
			}
		}
	}
}
