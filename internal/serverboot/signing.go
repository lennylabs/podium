package serverboot

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lennylabs/podium/pkg/sign"
)

// registrySigningEnabled reports whether the §13.10 signing mode selects the
// registry-managed key. Signing is on by default: an unset mode and
// "registry-key" enable it, and "none" turns it off. registrySignerFor,
// refuseUnpersistedSigningKey, and refuseGeneratedSigningKey all call it, so
// ingest, the §13.4 rewrite, and both refusals cannot read the mode
// differently. Spec: §13.10, §13.12.
func registrySigningEnabled(mode string) bool {
	return mode != "none"
}

// refuseUnpersistedSigningKey refuses a start whose registry signing key would
// be generated on storage with a different fate from the store it signs. The
// default key path resolves under the process's home, and a store need not
// live there, so a key the loader generates could be lost while the rows it
// signed survive, and no path re-signs a stored row. A memory store persists
// nothing and so strands no signature; the zero-configuration standalone
// SQLite store sits in the directory the default key resolves to, so the two
// share a fate. The error names the backend and PODIUM_SIGN_KEY_PATH and never
// a DSN. Spec: §13.12, §4.7.9.
func refuseUnpersistedSigningKey(cfg *Config) error {
	if !registrySigningEnabled(cfg.signMode) || os.Getenv("PODIUM_SIGN_KEY_PATH") != "" {
		return nil
	}
	if cfg.storeType == "memory" {
		return nil
	}
	keyPath, err := registrySigningKeyPath("")
	if err != nil {
		return fmt.Errorf("registry signing key: %w", err)
	}
	backend := cfg.storeType
	if cfg.storeType == "sqlite" {
		if filepath.Dir(filepath.Clean(cfg.sqlitePath)) == filepath.Dir(keyPath) {
			return nil
		}
		backend = fmt.Sprintf("sqlite (%s)", cfg.sqlitePath)
	}
	return fmt.Errorf("registry signing key: the store is %s and PODIUM_SIGN_KEY_PATH is unset, so the registry signing key would be generated under the process's home, outside the directory that holds the store; set PODIUM_SIGN_KEY_PATH to a file on the store's persistent storage, or set PODIUM_SIGN=none", backend)
}

// registrySigningKeyPath resolves the registry-managed signing key's location
// from PODIUM_SIGN_KEY_PATH, falling back to the standalone default. It
// delegates to sign.KeyFilePath, which the consumers (podium-mcp and the
// podium sign and verify commands) also call, so the loader, the §13.4
// generated-key refusal, and every reader resolve the same file.
func registrySigningKeyPath(env string) (string, error) { return sign.KeyFilePath(env) }

// registrySignerFor returns the §4.7.9 registry-managed signature provider for
// the signing mode. It returns (nil, nil) only for "none", so the caller leaves
// ingest.Request.Signer unset and manifests carry no signature.
//
// When signing is on (an empty mode or "registry-key") the registry holds a
// registry-managed Ed25519 keypair, loaded from PODIUM_SIGN_KEY_PATH (default
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
