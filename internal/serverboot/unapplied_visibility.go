package serverboot

import (
	"context"
	"fmt"
	"strings"

	"github.com/lennylabs/podium/pkg/store"
)

// unappliedVisibilityListCap is the number of layer IDs the §13.10 startup
// warning lists before it summarizes the rest as "and N more". The spec fixes
// the value, so it carries no override.
const unappliedVisibilityListCap = 20

// appliesNoVisibility reports whether the registry applies no layer
// visibility: public mode, or no request-time identity verifier, either of
// which resolves every caller as anonymous-public. The condition is the
// absent verifier rather than the configured provider label, so a label that
// resolves to no configured provider is covered whatever the startup checks
// on labels admit.
//
// Spec: §4.6 (No-identity bypass), §13.10
func appliesNoVisibility(publicMode, verifierInstalled bool) bool {
	return publicMode || !verifierInstalled
}

// logUnappliedVisibility logs the §13.10 startup warning that names the
// stored layers whose declared visibility the registry does not apply. It
// logs nothing when every stored layer grants public. A store read that fails
// is logged and the start proceeds, because the warning refuses nothing.
//
// Spec: §13.10 (Startup warning for unapplied visibility)
func logUnappliedVisibility(ctx context.Context, st store.Store, logf func(string, ...any)) {
	ids, err := unappliedVisibilityLayers(ctx, st)
	if err != nil {
		logf("warning: could not list the stored layers whose visibility this registry does not apply: %v", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	logf("warning: %d stored layer(s) declare visibility other than public, but this registry applies no layer visibility because it verifies no caller, so every caller sees them; their declarations take effect once an identity provider is configured: %s",
		len(ids), summarizeIDs(ids, unappliedVisibilityListCap))
}

// unappliedVisibilityLayers returns the live layers, across every tenant the
// store holds, that set no public grant. Each ID is qualified with its
// tenant's name, or the tenant ID for a tenant with no name, when the store
// holds more than one tenant, so the operator can tell two tenants' layers of
// the same ID apart.
func unappliedVisibilityLayers(ctx context.Context, st store.Store) ([]string, error) {
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	var ids []string
	for _, t := range tenants {
		layers, err := st.ListLayerConfigs(ctx, t.ID)
		if err != nil {
			return nil, fmt.Errorf("list layers of tenant %s: %w", t.ID, err)
		}
		for _, l := range layers {
			if l.Public {
				continue
			}
			if len(tenants) > 1 {
				ids = append(ids, fmt.Sprintf("%s (tenant %s)", l.ID, tenantLabel(t)))
				continue
			}
			ids = append(ids, l.ID)
		}
	}
	return ids, nil
}

// summarizeIDs joins at most limit IDs with ", " and appends "and N more"
// for the IDs past the limit.
func summarizeIDs(ids []string, limit int) string {
	if len(ids) <= limit {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(ids[:limit], ", "), len(ids)-limit)
}

// tenantLabel names a tenant in an operator log line: its human-readable
// name, or its ID when it has none.
func tenantLabel(t store.Tenant) string {
	if t.Name != "" {
		return t.Name
	}
	return t.ID
}
