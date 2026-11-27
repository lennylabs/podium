package serverboot

import (
	"context"
	"fmt"
	"log"

	"github.com/lennylabs/podium/pkg/store"
)

// identityRecordStore is the part of store.Store the removal guard reads and
// writes. The verified-identity record shares the store's marker table with
// the §13.4 data-migration markers, so the methods carry that table's names.
type identityRecordStore interface {
	DataMigrationApplied(ctx context.Context, name string) (bool, error)
	SetDataMigrationApplied(ctx context.Context, name string, applied bool) error
}

// identityProviderRemovalGuard records verified identity for a start that
// installs a request-time verifier, and refuses a start that installs none
// over a store recording an earlier start that did.
//
// A registry without a verifier resolves every caller as anonymous-public, so
// the §4.6 bypass shows every layer to every caller. That is the intended
// posture of a registry that never had an identity provider, which never sets
// the record and is unaffected. The guard targets a registry restarted after
// a dropped environment variable or Helm value removed its provider: without
// it, layers that were restricted become visible to everyone with nothing in
// the start to say so. The condition is the absent verifier rather than the
// provider label, so a label the registry resolves to no provider and public
// mode are covered alongside the unset provider.
//
// Store failures fail closed: a start that cannot read the record cannot tell
// whether it is dropping verified identity, and a verified start that cannot
// write it would leave a later removal undetected.
//
// Spec: §13.10 (Identity provider removed between starts), §4.6
func identityProviderRemovalGuard(ctx context.Context, st identityRecordStore, cfg *Config, verifierInstalled bool) error {
	if verifierInstalled {
		if err := st.SetDataMigrationApplied(ctx, store.DeploymentMarkerVerifiedIdentity, true); err != nil {
			return fmt.Errorf("identity provider: record verified identity in the metadata store (§13.10): %w", err)
		}
		return nil
	}
	recorded, err := st.DataMigrationApplied(ctx, store.DeploymentMarkerVerifiedIdentity)
	if err != nil {
		return fmt.Errorf("identity provider: read the verified-identity record from the metadata store, refusing to start without a request-time verifier (§13.10): %w", err)
	}
	if !recorded {
		return nil
	}
	posture := identityRemovalPosture(cfg)
	if !cfg.allowIdentityProviderRemoval {
		return fmt.Errorf("config.identity_provider_removed: this registry previously ran with verified identity, and this start installs no request-time identity verifier because %s; starting without it would %s (§4.6, §13.10). Restore the identity provider, or acknowledge the removal with --allow-identity-provider-removal or PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL=true and remove that setting after this start", posture.cause, posture.effect)
	}
	if err := st.SetDataMigrationApplied(ctx, store.DeploymentMarkerVerifiedIdentity, false); err != nil {
		return fmt.Errorf("identity provider: clear the verified-identity record in the metadata store (§13.10): %w", err)
	}
	log.Printf("warning: identity provider removed: this registry previously ran with verified identity and now starts without a request-time identity verifier because %s, as PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL acknowledges; this start will %s. The verified-identity record is cleared, so later starts need no acknowledgement: remove PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL / --allow-identity-provider-removal (§13.10)", posture.cause, posture.effect)
	return nil
}

// removalPosture names, for the refusal and the acknowledgement warning, why a
// start installs no verifier and what such a start admits.
type removalPosture struct {
	cause  string
	effect string
}

// identityRemovalPosture describes a start without a request-time verifier.
// Every such start shows every layer to every caller (§4.6). Only a start with
// no identity provider and not in public mode also admits every caller on the
// §4.7.2 admin arm: public mode gates that arm on the operator token, and a
// label the registry resolves to no provider leaves the anonymous caller
// without an admin grant.
func identityRemovalPosture(cfg *Config) removalPosture {
	const visible = "show every restricted layer to every caller"
	switch {
	case cfg.publicMode:
		return removalPosture{cause: "public mode (PODIUM_PUBLIC_MODE) is on", effect: visible}
	case cfg.hasIdentityProvider():
		return removalPosture{cause: fmt.Sprintf("PODIUM_IDENTITY_PROVIDER=%q resolves to no provider the registry verifies", cfg.identityProvider), effect: visible}
	default:
		return removalPosture{cause: "no identity provider is configured", effect: visible + " and admit every caller as an admin"}
	}
}
