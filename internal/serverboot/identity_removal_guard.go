package serverboot

import (
	"context"
	"fmt"
	"log"
	"slices"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/store"
)

// identityRecordStore is the part of store.Store the removal guard reads and
// writes. The verified-identity record shares the store's marker table with
// the §13.4 data-migration markers, so the methods carry that table's names.
type identityRecordStore interface {
	DataMigrationApplied(ctx context.Context, name string) (bool, error)
	SetDataMigrationApplied(ctx context.Context, name string, applied bool) error
}

// expectsRequestVerifier reports, from the configuration alone, whether this
// start installs a request-time identity verifier. It holds when public mode
// is off and the configured provider is registered and is one of
// verifiedProviders, the providers whose boot arm installs a verifier. A
// start that names such a provider and fails to install its verifier is
// refused by that arm (the audience, runtime-key, issuer, and JWKS guards), so
// the prediction cannot admit a start that then serves without a verifier.
// recordVerifiedIdentity checks the prediction against the installed result,
// so a provider that gains or loses a verifier without updating
// verifiedProviders fails the start rather than skip the check.
//
// Spec: §13.10 (Identity provider removed between starts)
func expectsRequestVerifier(cfg *Config) bool {
	return !cfg.publicMode &&
		identity.Default.Has(cfg.identityProvider) &&
		slices.Contains(verifiedProviders, cfg.identityProvider)
}

// identityRemovalCheck is the outcome of refuseIdentityProviderRemoval that
// recordVerifiedIdentity needs once the verifier phase has run.
type identityRemovalCheck struct {
	// recorded reports that the store held the verified-identity record when
	// a start without a verifier read it.
	recorded bool
}

// refuseIdentityProviderRemoval refuses a start that installs no request-time
// verifier over a store recording an earlier start that did. It only reads,
// and it runs right after the store opens: the bootstrap that follows writes
// the default tenant, admin grants, and the layer configuration, and a start
// with no identity provider writes a `--layer-path` layer as public. A
// verified replica still serving the same store reads those rows on every
// request, so a refused start that wrote them first would widen visibility
// on the replica it was refused to protect.
//
// A registry without a verifier resolves every caller as anonymous-public, so
// the §4.6 bypass shows every layer to every caller. That is the intended
// posture of a registry that never had an identity provider, which never sets
// the record and is unaffected. The guard targets a registry restarted after
// a dropped environment variable or Helm value removed its provider.
//
// A provider the registry recognises but does not verify at request time,
// such as oauth-device-code or an imported provider with no verifier, is
// refused here with identityVisibilityGuard's config.identity_provider_unverified,
// whether or not the store holds the record. Acknowledging a removal cannot
// admit that start, so the removal code would send the operator to a remedy
// that ends in the second refusal. The refusal comes before the bootstrap for
// the same reason as the removal check. identityVisibilityGuard still runs
// after the verifier phase as the backstop for the same condition.
//
// An unreadable record fails closed, because the start cannot tell whether it
// drops verified identity.
//
// Spec: §13.10 (Identity provider removed between starts), §4.6
func refuseIdentityProviderRemoval(ctx context.Context, st identityRecordStore, cfg *Config) (identityRemovalCheck, error) {
	if expectsRequestVerifier(cfg) {
		return identityRemovalCheck{}, nil
	}
	if identity.Default.Has(cfg.identityProvider) && !cfg.publicMode {
		return identityRemovalCheck{}, identityVisibilityGuard(cfg.identityProvider, true, cfg.publicMode, false)
	}
	recorded, err := st.DataMigrationApplied(ctx, store.DeploymentMarkerVerifiedIdentity)
	if err != nil {
		return identityRemovalCheck{}, fmt.Errorf("identity provider: read the verified-identity record from the metadata store, refusing to start without a request-time verifier (§13.10): %w", err)
	}
	if recorded && !cfg.allowIdentityProviderRemoval {
		posture := identityRemovalPosture(cfg)
		return identityRemovalCheck{}, fmt.Errorf("config.identity_provider_removed: this registry previously ran with verified identity, and this start installs no request-time identity verifier because %s; starting without it would %s (§4.6, §13.10). Restore the identity provider, or acknowledge the removal with --allow-identity-provider-removal or PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL=true and remove that setting after this start", posture.cause, posture.effect)
	}
	return identityRemovalCheck{recorded: recorded}, nil
}

// recordVerifiedIdentity runs after the verifier phase and before the server
// is built. A start with a verifier writes the record; an acknowledged
// removal clears it. A write failure refuses the start, because a verified
// start that cannot record would leave a later removal undetected. The
// acknowledgement on a start where it has no effect logs a warning, because a
// setting left in place, such as a Helm extraEnv entry, admits every later
// removal without a refusal.
//
// Spec: §13.10 (Identity provider removed between starts)
func recordVerifiedIdentity(ctx context.Context, st identityRecordStore, cfg *Config, verifierInstalled bool, check identityRemovalCheck) error {
	if verifierInstalled != expectsRequestVerifier(cfg) {
		// Unreachable while verifiedProviders matches the boot arms; the
		// removal check above ran on the prediction, so a mismatch means it
		// may have checked the wrong start.
		return fmt.Errorf("identity provider: PODIUM_IDENTITY_PROVIDER=%q installed a request-time verifier = %v, but verifiedProviders predicts %v; update verifiedProviders (§13.10)", cfg.identityProvider, verifierInstalled, !verifierInstalled)
	}
	if verifierInstalled {
		if err := st.SetDataMigrationApplied(ctx, store.DeploymentMarkerVerifiedIdentity, true); err != nil {
			return fmt.Errorf("identity provider: record verified identity in the metadata store (§13.10): %w", err)
		}
		warnUnusedRemovalAcknowledgement(cfg, "this start installs a request-time identity verifier")
		return nil
	}
	if !check.recorded {
		warnUnusedRemovalAcknowledgement(cfg, "the metadata store records no earlier start with verified identity")
		return nil
	}
	if err := st.SetDataMigrationApplied(ctx, store.DeploymentMarkerVerifiedIdentity, false); err != nil {
		return fmt.Errorf("identity provider: clear the verified-identity record in the metadata store (§13.10): %w", err)
	}
	posture := identityRemovalPosture(cfg)
	log.Printf("warning: identity provider removed: this registry previously ran with verified identity and now starts without a request-time identity verifier because %s, as PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL acknowledges; this start will %s. The verified-identity record is cleared, so later starts need no acknowledgement: remove PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL / --allow-identity-provider-removal (§13.10)", posture.cause, posture.effect)
	return nil
}

// warnUnusedRemovalAcknowledgement logs that the acknowledgement is set on a
// start where it does nothing, for the reason given.
func warnUnusedRemovalAcknowledgement(cfg *Config, reason string) {
	if !cfg.allowIdentityProviderRemoval {
		return
	}
	log.Printf("warning: PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL / --allow-identity-provider-removal has no effect on this start because %s; remove it, because while it stays set a later start that drops the identity provider is admitted without the config.identity_provider_removed refusal (§13.10)", reason)
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
// start with `external` leaves the anonymous caller without an admin grant.
// The configured-provider arm is reached only by `external`: validate refuses
// any other unrecognised value with config.identity_provider_unverified, and
// refuseIdentityProviderRemoval returns that code for a recognised provider
// without a verifier before it builds the message.
func identityRemovalPosture(cfg *Config) removalPosture {
	const visible = "show every restricted layer to every caller"
	switch {
	case cfg.publicMode:
		return removalPosture{cause: "public mode (PODIUM_PUBLIC_MODE) is on", effect: visible}
	case cfg.hasIdentityProvider():
		return removalPosture{cause: fmt.Sprintf("PODIUM_IDENTITY_PROVIDER=%q leaves authentication to whatever is in front of the registry, which verifies no caller", cfg.identityProvider), effect: visible}
	default:
		return removalPosture{cause: "no identity provider is configured", effect: visible + " and admit every caller as an admin"}
	}
}
