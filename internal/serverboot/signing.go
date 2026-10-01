package serverboot

import (
	"crypto/ed25519"
	"fmt"
	"log"
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
// signed survive. Those rows are then refused until the key file is restored,
// or until a surviving copy of the lost key's public half is listed on a
// verify: line and the registry restarts, which admits them; sign-stored-rows
// then re-signs them under the current key so the line can be removed. A
// memory store, a test backend that §13.12 does not list (see openStore),
// persists nothing and so strands no signature;
// the zero-configuration standalone SQLite store sits in the directory the
// default key resolves to, so the two share a fate. The error names the
// backend and PODIUM_SIGN_KEY_PATH and never a DSN. Spec: §13.12, §4.7.9.
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
	if keyCoLocatedWithStore(cfg, keyPath) {
		return nil
	}
	return fmt.Errorf("registry signing key: the store is %s and PODIUM_SIGN_KEY_PATH is unset, so the registry signing key would be generated under the process's home, outside the directory that holds the store; set PODIUM_SIGN_KEY_PATH to a file on the store's persistent storage, or set PODIUM_SIGN=none", storeLabel(cfg))
}

// storeLabel names the configured store in a startup refusal: the backend
// with its file path for SQLite, and the backend alone otherwise, so a
// refusal never prints a DSN.
func storeLabel(cfg *Config) string {
	if cfg.storeType == "sqlite" {
		return fmt.Sprintf("sqlite (%s)", cfg.sqlitePath)
	}
	return cfg.storeType
}

// registrySigningKeyPath resolves the registry-managed signing key's location
// from PODIUM_SIGN_KEY_PATH, falling back to the standalone default. It
// delegates to sign.KeyFilePath, which the consumers (podium-mcp and the
// podium sign and verify commands) also call, so the loader, the §13.4
// generated-key refusal, and every reader resolve the same file.
func registrySigningKeyPath(env string) (string, error) { return sign.KeyFilePath(env) }

// registrySignerFor returns the §4.7.9 registry-managed key for the signing
// mode and reports whether signing is on. It returns the zero key and false
// only for "none"; the caller then hands nil to ingest, admission, the
// delivery signer, and the §13.4 rewrite, so manifests carry no signature. The
// flag exists because the zero RegistryManagedKey stored in an interface is
// never nil, so the caller cannot test the key itself.
//
// When signing is on (an empty mode or "registry-key") the key is loaded from
// PODIUM_SIGN_KEY_PATH (default ~/.podium/standalone/registry-signing.key) by
// loadRegistrySigner, which generates it on first run. One loader serves
// ingest and the §13.4 rehash pass, which verifies a stored envelope before
// it rewrites the row, so every envelope the registry mints carries one
// key_id. Spec: §4.7.9, §13.10.
func registrySignerFor(mode string) (sign.RegistryManagedKey, bool, error) {
	if !registrySigningEnabled(mode) {
		return sign.RegistryManagedKey{}, false, nil
	}
	key, err := loadRegistrySigner(os.Getenv("PODIUM_SIGN_KEY_PATH"), true)
	if err != nil {
		return sign.RegistryManagedKey{}, false, err
	}
	return key, true, nil
}

// loadRegistrySigner reads the registry-managed signing key file at the path
// env resolves to. The private: key signs, and the public: key and every
// verify: key form the verification key set. With generate set, an absent
// file is generated (the registry start); without it, an absent file is
// refused and nothing is written (sign-stored-rows, which never mints a key).
// A file with no private: line is refused because the registry signs.
//
// Every error carries config.signature_provider_unavailable in its text and
// wraps sign.ErrRegistryManagedUnavailable: spi.Error.Error() returns the
// message alone, and podium-server prints the returned error verbatim, so the
// code would otherwise never reach the operator. The loader keeps a distinct
// default path from loadOrGenerateAuditSigner so the ingest-signing key and
// the audit-anchor key never alias. Spec: §4.7.9, §13.12.
func loadRegistrySigner(env string, generate bool) (sign.RegistryManagedKey, error) {
	path, err := registrySigningKeyPath(env)
	if err != nil {
		return sign.RegistryManagedKey{}, fmt.Errorf("config.signature_provider_unavailable: registry signing key path: %w: %w", sign.ErrRegistryManagedUnavailable, err)
	}
	kf, err := readRegistryKeyFile(path, generate)
	if err != nil {
		return sign.RegistryManagedKey{}, signingKeyUnavailable(path, err)
	}
	key := sign.RegistryManagedKey{
		PrivateKey: kf.Private,
		PublicKey:  kf.Private.Public().(ed25519.PublicKey),
		Trusted:    kf.Verify,
	}
	log.Printf("registry signing key: key_id %s signs; verification-only key_ids %v", key.CurrentKeyID(), key.VerifyKeyIDs())
	return key, nil
}

// readRegistryKeyFile reads the key file, generating it only when generate is
// set, and refuses a file that holds no private: line.
func readRegistryKeyFile(path string, generate bool) (sign.KeyFile, error) {
	if generate {
		return readOrCreateKeyFile(path)
	}
	kf, err := sign.ReadKeyFile(path)
	if err != nil {
		return sign.KeyFile{}, err
	}
	if kf.Private == nil {
		return sign.KeyFile{}, fmt.Errorf("sign: key file %s carries no \"private:\" line", path)
	}
	return kf, nil
}

// signingKeyUnavailable formats a loader failure with the §6.10 code in the
// text and the sentinel in the chain, so both errors.Is and a reader of
// stderr see config.signature_provider_unavailable.
func signingKeyUnavailable(path string, cause error) error {
	return fmt.Errorf("config.signature_provider_unavailable: registry signing key %s: %w: %w", path, sign.ErrRegistryManagedUnavailable, cause)
}

// keyCoLocatedWithStore reports whether the key file at keyPath sits in the
// directory of the SQLite store, so the key and the rows it signs share one
// fate on disk. It is false for every other backend. Spec: §13.12, §13.4.
func keyCoLocatedWithStore(cfg *Config, keyPath string) bool {
	return cfg.storeType == "sqlite" &&
		filepath.Dir(filepath.Clean(cfg.sqlitePath)) == filepath.Dir(filepath.Clean(keyPath))
}

// unmigratedStoreGoverned reports whether the §13.4 unmigrated-store refusal
// and the pre-ingest record check govern this start, and returns the key
// location the co-location test used. Every store other than the SQLite store
// in the directory of the registry signing key location is governed, in both
// signing modes: a signing-off rewrite signs nothing, but it still moves every
// stored row to a new digest with no review. The key file need not exist, so a
// signing start over the zero-configuration standalone store that has no key
// yet keeps the automatic rewrite and generates the key.
//
// A key location that cannot be resolved, because the process has no home
// directory, cannot show co-location. A signing start returns that error,
// because it cannot load a key without the path; a signing-off start treats
// the store as outside the directory and returns an empty keyPath, which the
// refusal messages name as PODIUM_SIGN_KEY_PATH. refuseUnmigratedStore and
// refuseUnrecordedIngest are its only callers, so the two apply one
// predicate. Spec: §13.4, §13.12.
func unmigratedStoreGoverned(cfg *Config) (keyPath string, governed bool, err error) {
	keyPath, err = registrySigningKeyPath(os.Getenv("PODIUM_SIGN_KEY_PATH"))
	if err != nil {
		if registrySigningEnabled(cfg.signMode) {
			return "", false, fmt.Errorf("registry signing key: %w", err)
		}
		return "", true, nil
	}
	return keyPath, !keyCoLocatedWithStore(cfg, keyPath), nil
}
