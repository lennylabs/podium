package serverboot

import (
	"os"
	"path/filepath"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
)

// registrySignerFor returns the §4.7.9 ingest SignerFunc for the standalone
// signing mode (§13.10 "Disabled by default; opt in via --sign registry-key").
// It returns (nil, nil) when signing is disabled so the caller leaves
// ingest.Request.Signer unset and manifests carry no signature.
//
// When the mode is "registry-key" the registry holds a registry-managed
// Ed25519 keypair, loaded from PODIUM_SIGN_KEY_PATH (default
// ~/.podium/standalone/registry-signing.key) and generated on first run. The
// returned SignerFunc is the provider's Sign method, whose
// (ctx, contentHash) -> (envelope, error) signature matches ingest.SignerFunc.
func registrySignerFor(mode string) (ingest.SignerFunc, error) {
	if mode != "registry-key" {
		return nil, nil
	}
	signer, err := loadOrGenerateRegistrySigner(os.Getenv("PODIUM_SIGN_KEY_PATH"))
	if err != nil {
		return nil, err
	}
	return signer.Sign, nil
}

// loadOrGenerateRegistrySigner reads (or creates) the registry-managed signing
// keypair used for §4.7.9 ingest signing. It mirrors loadOrGenerateAuditSigner
// but keeps a distinct default path so the ingest-signing key and the audit-
// anchor key never alias.
func loadOrGenerateRegistrySigner(path string) (sign.Provider, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".podium", "standalone", "registry-signing.key")
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
