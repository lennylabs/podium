package serverboot

import (
	"os"
	"path/filepath"

	"github.com/lennylabs/podium/pkg/sign"
)

// registrySigningEnabled reports whether the §13.10 signing mode selects the
// registry-managed key ("Disabled by default; opt in via --sign registry-key").
// registrySignerFor and refuseGeneratedSigningKey both call it, so the two
// cannot read the mode differently.
func registrySigningEnabled(mode string) bool {
	return mode == "registry-key"
}

// registrySigningKeyPath resolves the registry-managed signing key's location
// from PODIUM_SIGN_KEY_PATH, falling back to the standalone default. The loader
// and the §13.4 generated-key refusal both call it, so the two cannot resolve
// different files.
func registrySigningKeyPath(env string) (string, error) {
	if env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".podium", "standalone", "registry-signing.key"), nil
}

// registrySignerFor returns the §4.7.9 registry-managed signature provider for
// the standalone signing mode. It returns (nil, nil) when signing is disabled
// so the caller leaves ingest.Request.Signer unset and manifests carry no
// signature.
//
// When the mode is "registry-key" the registry holds a registry-managed
// Ed25519 keypair, loaded from PODIUM_SIGN_KEY_PATH (default
// ~/.podium/standalone/registry-signing.key) and generated on first run. The
// caller takes ingest's SignerFunc from the provider's Sign method, whose
// (ctx, contentHash) -> (envelope, error) signature matches it. The whole
// provider is returned because the §13.4 rehash pass verifies a stored envelope
// before it rewrites the row, and one loader serving both is what makes the
// envelope the pass mints carry the key_id an ingest-minted envelope carries.
func registrySignerFor(mode string) (sign.Provider, error) {
	if !registrySigningEnabled(mode) {
		return nil, nil
	}
	return loadOrGenerateRegistrySigner(os.Getenv("PODIUM_SIGN_KEY_PATH"))
}

// loadOrGenerateRegistrySigner reads (or creates) the registry-managed signing
// keypair used for §4.7.9 ingest signing. It mirrors loadOrGenerateAuditSigner
// but keeps a distinct default path so the ingest-signing key and the audit-
// anchor key never alias.
func loadOrGenerateRegistrySigner(env string) (sign.Provider, error) {
	path, err := registrySigningKeyPath(env)
	if err != nil {
		return nil, err
	}
	priv, pub, err := readOrCreateEd25519(path)
	if err != nil {
		return nil, err
	}
	return sign.RegistryManagedKey{
		PrivateKey: priv,
		PublicKey:  pub,
		KeyID:      keyIDFor(pub),
	}, nil
}
