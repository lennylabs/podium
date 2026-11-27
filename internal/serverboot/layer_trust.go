package serverboot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// layerTrustDefaultEnv is the §13.12 setting an empty layer trust mode
// resolves to.
const layerTrustDefaultEnv = "PODIUM_LAYER_TRUST_DEFAULT"

// layerTrustDefault is the resolved PODIUM_LAYER_TRUST_DEFAULT: the mode, the
// value as set, where it came from, and the refusal of a value that is no
// mode.
type layerTrustDefault struct {
	mode   string
	raw    string
	source string
	err    error
}

// resolveLayerTrustDefault parses PODIUM_LAYER_TRUST_DEFAULT. Surrounding
// whitespace is removed, an empty value is unset and resolves to none, and a
// value other than none, warn, or require is refused with
// config.invalid_layer_trust rather than read as none, because an operator
// who mistyped require would otherwise run every layer with no policy.
//
// Spec: §13.12, §4.7.11
func resolveLayerTrustDefault(raw string) layerTrustDefault {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return layerTrustDefault{mode: layer.TrustModeNone, source: "default"}
	}
	if !layer.ValidTrustMode(raw) {
		return layerTrustDefault{mode: layer.TrustModeNone, raw: raw, source: layerTrustDefaultEnv,
			err: fmt.Errorf("config.invalid_layer_trust: %s must be none, warn, or require, got %q", layerTrustDefaultEnv, raw)}
	}
	return layerTrustDefault{mode: raw, raw: raw, source: layerTrustDefaultEnv}
}

// display is the value `podium config show` prints, with an invalid value
// shown as set and marked.
func (d layerTrustDefault) display() string {
	if d.err != nil {
		return d.raw + " (invalid)"
	}
	return d.mode
}

// trustInstanceKnown reports whether name is a Sigstore instance this
// registry configures: public-good, or an entry of sigstore.instances.
//
// Spec: §4.7.11 (Policy rules)
func (c *Config) trustInstanceKnown(name string) bool {
	if name == sign.PublicGoodInstance {
		return true
	}
	for _, fi := range c.attestation.files {
		if fi.fromConfig && fi.name == name {
			return true
		}
	}
	return false
}

// yamlTrustRequirement is one entry of a declared layer's
// trust_requirements, in the form of the §7.3.1 layer object member.
type yamlTrustRequirement struct {
	Signers       []yamlTrustSigner `yaml:"signers"`
	PredicateType string            `yaml:"predicate_type"`
}

// yamlTrustSigner is one signer of a declared requirement. The identifier
// members are strings, and an unquoted number decodes into one as written.
type yamlTrustSigner struct {
	Issuer                  string `yaml:"issuer"`
	Identity                string `yaml:"identity"`
	SourceRepository        string `yaml:"source_repository"`
	SourceRepositoryOwner   string `yaml:"source_repository_owner"`
	SourceRepositoryID      string `yaml:"source_repository_id"`
	SourceRepositoryOwnerID string `yaml:"source_repository_owner_id"`
	AnySource               bool   `yaml:"any_source"`
	SourceRepositoryRef     string `yaml:"source_repository_ref"`
	AnyRef                  bool   `yaml:"any_ref"`
	Instance                string `yaml:"instance"`
}

// The keys a declared requirement and signer accept, which are the yaml tags
// above.
var (
	requirementKeys = []string{"signers", "predicate_type"}
	signerKeys      = []string{"issuer", "identity", "source_repository", "source_repository_owner",
		"source_repository_id", "source_repository_owner_id", "any_source", "source_repository_ref", "any_ref", "instance"}
)

// declaredTrustPolicy returns the trust policy of a declared layer, with the
// §4.7.11 source binding derived from the declared repository, or an error
// that names the layer and the rule. repo is the declared source.git.repo,
// which carries no credential the source.git keys add.
//
// Spec: §4.7.11 (Policy rules), §7.3.1 (Layer trust policy)
func declaredTrustPolicy(entry yamlLayerEntry, sourceType, repo string, known layer.TrustInstances) (store.LayerConfig, error) {
	if entry.trustKeyErr != nil {
		return store.LayerConfig{}, fmt.Errorf("config.invalid_layer_trust: declared layer %s: %w", entry.ID, entry.trustKeyErr)
	}
	reqs, err := declaredTrustRequirements(entry.TrustRequirements)
	if err != nil {
		return store.LayerConfig{}, fmt.Errorf("config.invalid_layer_trust: declared layer %s: %w", entry.ID, err)
	}
	cfg := store.LayerConfig{
		ID:                entry.ID,
		SourceType:        sourceType,
		TrustMode:         entry.TrustMode,
		TrustRequirements: layer.DeriveSourceBinding(sourceType, repo, reqs),
		TrustLayerRoot:    entry.TrustLayerRoot,
	}
	// The empty default: a declared layer whose empty mode only the default
	// raises aborts no start; the start log names it (§4.7.11).
	if err := layer.CheckTrustPolicy(cfg, known, ""); err != nil {
		return store.LayerConfig{}, fmt.Errorf("config.invalid_layer_trust: declared layer %s: %w", entry.ID, err)
	}
	return cfg, nil
}

// declaredTrustRequirements decodes a declared trust_requirements node. A key
// a requirement or a signer does not define is refused with its line, so a
// misspelled binding member cannot leave a signer unbound.
func declaredTrustRequirements(node yaml.Node) ([]store.TrustRequirement, error) {
	n := resolveAlias(&node)
	if n.Kind == 0 || n.Tag == "!!null" {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("trust_requirements (line %d) is not a list", n.Line)
	}
	for i, req := range n.Content {
		name := fmt.Sprintf("trust_requirements[%d]", i)
		req = resolveAlias(req)
		if err := checkKeys(name, req, requirementKeys); err != nil {
			return nil, err
		}
		for j := 0; j+1 < len(req.Content); j += 2 {
			signers := resolveAlias(req.Content[j+1])
			if req.Content[j].Value != "signers" || signers.Kind != yaml.SequenceNode {
				continue
			}
			for k, s := range signers.Content {
				if err := checkKeys(fmt.Sprintf("%s.signers[%d]", name, k), resolveAlias(s), signerKeys); err != nil {
					return nil, err
				}
			}
		}
	}
	var decoded []yamlTrustRequirement
	if err := n.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("trust_requirements: %w", err)
	}
	out := make([]store.TrustRequirement, 0, len(decoded))
	for _, r := range decoded {
		req := store.TrustRequirement{PredicateType: r.PredicateType}
		for _, s := range r.Signers {
			req.Signers = append(req.Signers, store.TrustSigner(s))
		}
		out = append(out, req)
	}
	return out, nil
}

// resolveAlias follows a YAML alias to the node it names, so the key check
// reads a requirement or signer written as *anchor as the decode does. A
// merge key (<<) stays a key, which checkKeys refuses.
func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// checkKeys refuses a mapping node that holds a key outside allowed. A node
// that is not a mapping is left to the decode, which refuses it.
func checkKeys(name string, n *yaml.Node, allowed []string) error {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		if !slices.Contains(allowed, key.Value) {
			return fmt.Errorf("%s holds the key %q (line %d), which is not one of %s", name, key.Value, key.Line, strings.Join(allowed, ", "))
		}
	}
	return nil
}

// declaredRepo returns the repository a declared entry's policy derives its
// source binding from: source.git.repo as declared, empty for a local layer.
func declaredRepo(entry yamlLayerEntry) (sourceType, repo string) {
	if entry.Source.Git != nil {
		return "git", entry.Source.Git.Repo
	}
	return "local", ""
}

// declaredLayerTrustErr applies the §4.7.11 policy rules to every declared
// layer, so validate refuses the start before any store opens.
//
// Spec: §7.3.1 (Layer trust policy)
func (c *Config) declaredLayerTrustErr() error {
	for _, entry := range c.declaredLayers {
		sourceType, repo := declaredRepo(entry)
		if _, err := declaredTrustPolicy(entry, sourceType, repo, c.trustInstanceKnown); err != nil {
			return err
		}
	}
	return nil
}

// keepStoredTrust copies the trust policy the store holds for cfg's layer
// onto cfg, so a start that re-seeds a --layer-path layer keeps a policy an
// operator set through the layer endpoints. A layer the store does not hold
// keeps cfg's policy, and a failed read aborts the start rather than drop
// the policy.
//
// Spec: §7.3.1 (Layer trust policy)
func keepStoredTrust(ctx context.Context, st store.Store, cfg store.LayerConfig) (store.LayerConfig, error) {
	prev, err := st.GetLayerConfig(ctx, cfg.TenantID, cfg.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return cfg, nil
	case err != nil:
		return cfg, fmt.Errorf("read layer config %s: %w", cfg.ID, err)
	}
	cfg.TrustMode, cfg.TrustRequirements, cfg.TrustLayerRoot = prev.TrustMode, prev.TrustRequirements, prev.TrustLayerRoot
	return cfg, nil
}

// logUnrequiredTrustLayers logs the §4.7.11 start warning for each stored
// layer of the tenants whose empty trust mode def resolves to warn or require
// and that has no requirement. Ingest verification refuses such a layer's
// cycles; the warning names it before any cycle runs. A failed read is logged
// and the start proceeds, because the warning refuses nothing.
//
// Spec: §4.7.11 (Policy changes)
func logUnrequiredTrustLayers(ctx context.Context, st store.Store, tenants []string, def string, logf func(string, ...any)) {
	if def == "" || def == layer.TrustModeNone {
		return
	}
	for _, tenant := range tenants {
		layers, err := st.ListLayerConfigs(ctx, tenant)
		if err != nil {
			logf("warning: layer trust: could not list the layers of tenant %s: %v", tenant, err)
			continue
		}
		for _, l := range layers {
			if l.TrustMode == "" && len(l.TrustRequirements) == 0 {
				logf("warning: layer trust: layer %s (tenant %s) resolves to trust mode %s through %s and has no trust requirement; set one with `podium layer update --id %s --trust-signer ...`, or set its trust_mode",
					l.ID, tenant, def, layerTrustDefaultEnv, l.ID)
			}
		}
	}
}

// trustLogTenants returns the tenants the start warning reads: every
// provisioned tenant of a multi-tenant registry, and the bound tenant
// otherwise.
func trustLogTenants(ctx context.Context, st store.Store, multiTenant bool, tenantID string) []string {
	if !multiTenant {
		return []string{tenantID}
	}
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		return []string{tenantID}
	}
	ids := make([]string, 0, len(tenants))
	for _, t := range tenants {
		ids = append(ids, t.ID)
	}
	return ids
}

// isDeclaredLayer returns the predicate the layer endpoint reads: whether a
// layer of a tenant is declared in registry.yaml. Declared layers are seeded
// into the bound tenant alone.
//
// Spec: §7.3.1 (Layer trust policy)
func (c *Config) isDeclaredLayer(tenantID string) func(tenant, id string) bool {
	ids := map[string]bool{}
	for _, e := range c.declaredLayers {
		ids[e.ID] = true
	}
	return func(tenant, id string) bool {
		return tenant == tenantID && ids[id]
	}
}
