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
// absent verifier rather than the configured provider value, so
// PODIUM_IDENTITY_PROVIDER=external, which installs no verifier, is covered
// without naming it here.
//
// Spec: §4.6 (No-identity bypass), §13.10
func appliesNoVisibility(publicMode, verifierInstalled bool) bool {
	return publicMode || !verifierInstalled
}

// logUnappliedVisibility logs the §13.10 startup warning that names the
// layers of tenantID whose declared visibility the registry does not apply.
// It logs nothing when every such layer grants public. A store read that
// fails is logged and the start proceeds, because the warning refuses
// nothing.
//
// The read covers the bound tenant alone. A registry that applies no
// visibility is single-tenant, because a multi-tenant registry without a
// verifier refuses startup with config.multitenant_no_verifier, so it serves
// that tenant and no other. Listing another tenant the store holds would name
// layers no caller of this registry can see.
//
// Spec: §13.10 (Startup warning for unapplied visibility)
func logUnappliedVisibility(ctx context.Context, st store.Store, tenantID string, publicMode bool, logf func(string, ...any)) {
	ids, err := unappliedVisibilityLayers(ctx, st, tenantID)
	if err != nil {
		logf("warning: could not list the stored layers whose visibility this registry does not apply: %v", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	// A public-mode registry refuses an identity provider
	// (config.public_mode_with_idp), so its declarations apply only once
	// public mode is turned off as well.
	reason, remedy := "verifies no caller", "once an identity provider is configured"
	if publicMode {
		reason, remedy = "runs in public mode", "once public mode is turned off and an identity provider is configured"
	}
	logf("warning: %d stored layer(s) declare visibility other than public, but this registry %s and applies no layer visibility, so every caller sees them; their declarations take effect %s: %s",
		len(ids), reason, remedy, summarizeIDs(ids, unappliedVisibilityListCap))
}

// unappliedVisibilityLayers returns the IDs of tenantID's live layers that
// set no public grant.
func unappliedVisibilityLayers(ctx context.Context, st store.Store, tenantID string) ([]string, error) {
	layers, err := st.ListLayerConfigs(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list layers of tenant %s: %w", tenantID, err)
	}
	var ids []string
	for _, l := range layers {
		if !l.Public {
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
