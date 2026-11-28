package serverboot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// layerTrustRequirementsFileEnv names the requirements file of the layers
// that PODIUM_LAYER_PATH bootstraps.
const layerTrustRequirementsFileEnv = "PODIUM_LAYER_TRUST_REQUIREMENTS_FILE"

// bootstrapRequirements is the resolved PODIUM_LAYER_TRUST_REQUIREMENTS_FILE:
// the requirements every bootstrap layer carries and the trust_layer_root of
// each layer that layer_roots names.
type bootstrapRequirements struct {
	path  string
	reqs  []store.TrustRequirement
	roots map[string]string
	err   error
}

// resolveBootstrapRequirements reads and checks the requirements file at
// path. An unset path resolves to no file. A file that cannot be read, that
// is not valid JSON in the §4.7.11 form, whose requirements break a policy
// rule, or whose layer_roots holds a value that is no valid root refuses the
// start with config.invalid_layer_trust.
//
// Spec: §4.7.11 (Local layers and the standalone bootstrap), §13.12
func resolveBootstrapRequirements(path string, known layer.TrustInstances) bootstrapRequirements {
	path = strings.TrimSpace(path)
	b := bootstrapRequirements{path: path}
	if path == "" {
		return b
	}
	fail := func(err error) bootstrapRequirements {
		b.err = fmt.Errorf("config.invalid_layer_trust: %s %s: %w", layerTrustRequirementsFileEnv, path, err)
		return b
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fail(err)
	}
	f, err := layer.ParseRequirementsFile(raw)
	if err != nil {
		return fail(err)
	}
	if err := layer.CheckRequirements(f.TrustRequirements, known); err != nil {
		return fail(err)
	}
	if b.roots, err = layerRoots(f.LayerRoots); err != nil {
		return fail(err)
	}
	b.reqs = f.TrustRequirements
	return b
}

// layerRoots decodes the layer_roots object: a layer ID to a trust_layer_root
// that the §4.7.11 rules admit for a local layer.
func layerRoots(raw json.RawMessage) (map[string]string, error) {
	roots := map[string]string{}
	if len(raw) == 0 {
		return roots, nil
	}
	if err := json.Unmarshal(raw, &roots); err != nil {
		return nil, fmt.Errorf("layer_roots: %w", err)
	}
	for id, root := range roots {
		if err := layer.CheckTrustPolicy(store.LayerConfig{SourceType: "local", TrustLayerRoot: root}, nil, ""); err != nil {
			return nil, fmt.Errorf("layer_roots[%q]: %w", id, err)
		}
	}
	return roots, nil
}

// bootstrapTrust is the trust input of the --layer-path seed: the
// requirements file, the trust default, and the ingest verification of the
// startup ingest.
type bootstrapTrust struct {
	file        bootstrapRequirements
	defaultMode string
	att         *ingest.AttestationConfig
}

// apply sets the trust policy of a bootstrap layer. With a requirements
// file, the layer carries the file's requirements and the trust_layer_root
// layer_roots names for it, empty when it names none. Without one, the layer
// keeps the policy it stores, and a layer whose resolved mode is above none
// with no requirement refuses the start with config.invalid_layer_trust,
// because a bootstrap layer carries no signers of its own.
//
// Spec: §4.7.11 (Local layers and the standalone bootstrap), §7.3.1
func (b bootstrapTrust) apply(ctx context.Context, st store.Store, cfg store.LayerConfig) (store.LayerConfig, error) {
	cfg, err := keepStoredTrust(ctx, st, cfg)
	if err != nil {
		return cfg, err
	}
	if b.file.path != "" {
		cfg.TrustRequirements, cfg.TrustLayerRoot = b.file.reqs, b.file.roots[cfg.ID]
		return cfg, nil
	}
	mode := layer.ResolveTrustMode(cfg.TrustMode, b.defaultMode)
	if mode != layer.TrustModeNone && len(cfg.TrustRequirements) == 0 {
		return cfg, fmt.Errorf("config.invalid_layer_trust: layer %s resolves to trust mode %s and carries no requirement; set %s",
			cfg.ID, mode, layerTrustRequirementsFileEnv)
	}
	return cfg, nil
}

// publicGoodRefresh starts the refresh of the public-good trusted root once,
// when ingest verification first needs that instance, and stops it at the
// end of the run.
//
// Spec: §4.7.11 (Trusted root refresh)
type publicGoodRefresh struct {
	once  sync.Once
	start func(context.Context) func()
	ctx   context.Context
	// mu guards stopFn, which the start sets and the shutdown reads.
	mu     sync.Mutex
	stopFn func()
}

// trigger starts the refresh on its first call.
func (p *publicGoodRefresh) trigger() {
	p.once.Do(func() {
		stop := p.start(p.ctx)
		p.mu.Lock()
		p.stopFn = stop
		p.mu.Unlock()
	})
}

// stop ends a started refresh and waits for it to return.
func (p *publicGoodRefresh) stop() {
	p.mu.Lock()
	stop := p.stopFn
	p.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// ingestAttestation builds the §4.7.11 ingest verification of the registry
// from its configured instances, the trust default, and the bundle limits.
// The public-good refresh starts when a cycle first verifies under a policy
// that names that instance, so a registry that never does makes no request
// to the TUF repository. The returned stop ends that refresh.
//
// Spec: §4.7.11 (Ingest verification, Trusted root refresh)
func (c *Config) ingestAttestation(ctx context.Context) (*ingest.AttestationConfig, func()) {
	refresh := &publicGoodRefresh{start: c.StartAttestationRefresh, ctx: ctx}
	att := &ingest.AttestationConfig{
		Instances:      c.AttestationInstances(),
		DefaultMode:    c.layerTrust.mode,
		MaxBundleBytes: c.attestation.limits.MaxBytes,
		MaxBundles:     c.attestation.limits.MaxBundles,
		OnPublicGood:   refresh.trigger,
	}
	return att, refresh.stop
}

// startRefreshForStoredPolicies starts the public-good refresh at the start
// when a stored layer of tenants resolves to warn or require under a policy
// that names a signer of that instance, so a binary whose embedded root has
// no known age verifies once the refresh succeeds rather than at the first
// cycle. A failed read starts nothing; the first such cycle starts it.
//
// Spec: §4.7.11 (Trusted root refresh)
func startRefreshForStoredPolicies(ctx context.Context, st store.Store, tenants []string, att *ingest.AttestationConfig) {
	for _, tenant := range tenants {
		layers, err := st.ListLayerConfigs(ctx, tenant)
		if err != nil {
			continue
		}
		for _, lc := range layers {
			if verifiesOnPublicGood(lc, att.DefaultMode) {
				att.OnPublicGood()
				return
			}
		}
	}
}

// verifiesOnPublicGood reports whether a cycle of lc verifies on the
// public-good instance: its resolved mode is warn or require and a signer
// names that instance, which an empty instance names.
func verifiesOnPublicGood(lc store.LayerConfig, def string) bool {
	if layer.ResolveTrustMode(lc.TrustMode, def) == layer.TrustModeNone {
		return false
	}
	for _, req := range lc.TrustRequirements {
		for _, s := range req.Signers {
			if s.Instance == "" || s.Instance == sign.PublicGoodInstance {
				return true
			}
		}
	}
	return false
}

// startupRefusal reports whether err is an attestation refusal of the
// startup ingest of layerID, which the start logs and passes over: the
// refusal is in the layer's attestation record, the stored versions stay
// served, and the registry listens so the mark reset can run.
//
// Spec: §4.7.11 (Startup ingest), §13.10
func startupRefusal(layerID string, err error) bool {
	if !errors.Is(err, ingest.ErrAttestationRefused) {
		return false
	}
	var ae *ingest.AttestationError
	code, constraint := "", ""
	if errors.As(err, &ae) {
		code, constraint = ae.Code, ae.Constraint()
	}
	log.Printf("warning: attestation: the startup ingest of layer %s was refused with %s (constraint %q); its stored versions stay served, and `podium layer show %s` prints the next step",
		layerID, code, attest.ReportString(constraint), layerID)
	return true
}

// trustPolicyChangeHook returns the re-evaluation a register or an update
// runs after it changed a layer's trust policy, followed by the ingest cycle
// the change requests. The layer events record the change; the
// re-evaluation records layer.attestation_links_downgraded through the audit
// stream with the caller that made it. A failure is logged and changes
// nothing the request did, because the requested cycle runs the
// re-evaluation again. reingest is nil where the registry runs no ingest.
//
// Spec: §4.7.11 (Re-evaluation), §7.3.1 (Layer trust policy)
func trustPolicyChangeHook(st store.Store, att *ingest.AttestationConfig, sink audit.Sink, scrubber *audit.PIIScrubber, reingest func(store.LayerConfig)) func(context.Context, store.LayerConfig, string) {
	return func(ctx context.Context, lc store.LayerConfig, caller string) {
		var emit ingest.AuditEmitterFunc
		if sink != nil {
			emit = ingestAuditEmitter(ctx, sink, scrubber, caller, lc.TenantID)
		}
		if err := att.Reevaluate(ctx, st, lc, clock.Real{}, emit); err != nil {
			log.Printf("attestation: layer %s: re-evaluate the links after a trust policy change: %v; the next ingest cycle of the layer runs it again", lc.ID, err)
		}
		if reingest != nil {
			reingest(lc)
		}
	}
}

// backgroundReingest returns a request for an ingest cycle of a layer that
// runs outside the request that made it, under ctx, so a git fetch does not
// hold the update's response. The cycle ends with ctx at shutdown. A failure
// is logged; the layer's next trigger runs a cycle again.
//
// Spec: §4.7.11 (Re-evaluation)
func backgroundReingest(ctx context.Context, run func(context.Context, store.LayerConfig) error) func(store.LayerConfig) {
	return func(lc store.LayerConfig) {
		go func() {
			if err := run(ctx, lc); err != nil {
				log.Printf("attestation: layer %s: the ingest cycle a trust policy change requested failed: %v", lc.ID, err)
			}
		}()
	}
}

// reevaluateAtStart runs the re-evaluation of every stored layer of each
// tenant once the start has seeded its layers and loaded its trusted roots.
// A failure is logged, and each layer's next cycle runs the re-evaluation
// again.
//
// Spec: §4.7.11 (Re-evaluation)
func reevaluateAtStart(ctx context.Context, st store.Store, tenants []string, att *ingest.AttestationConfig, sink audit.Sink, scrubber *audit.PIIScrubber) {
	for _, tenant := range tenants {
		var emit ingest.AuditEmitterFunc
		if sink != nil {
			emit = ingestAuditEmitter(ctx, sink, scrubber, "", tenant)
		}
		if err := att.ReevaluateStoredLayers(ctx, st, tenant, clock.Real{}, emit); err != nil {
			log.Printf("attestation: tenant %s: re-evaluate the stored layers at the start: %v; each layer's next ingest cycle runs it again", tenant, err)
		}
	}
}
