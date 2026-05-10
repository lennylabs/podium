package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/lennylabs/podium/pkg/sign"
)

// signCmd produces a signature envelope for a content hash using
// the configured signing provider.
//
//	podium sign --content-hash sha256:... [--provider noop|registry-managed|sigstore-keyless]
func signCmd(args []string) int {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	setUsage(fs, "Sign a content hash via the configured signature provider.")
	contentHash := fs.String("content-hash", "", "sha256:<hex> content hash (required)")
	providerName := fs.String("provider", envDefault("PODIUM_SIGNATURE_PROVIDER", "noop"), "noop|registry-managed|sigstore-keyless")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *contentHash == "" {
		fmt.Fprintln(os.Stderr, "error: --content-hash is required (sha256:<hex>)")
		return 2
	}
	provider, err := loadSignatureProvider(*providerName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	envelope, err := provider.Sign(*contentHash)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign failed: %v\n", err)
		return 1
	}
	fmt.Println(envelope)
	return 0
}

// verifyCmd verifies a signature envelope against a content hash.
// Exits 0 on a valid signature, 1 on mismatch or other error.
//
//	podium verify --content-hash sha256:... --signature <envelope> [--provider ...]
func verifyCmd(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	setUsage(fs, "Verify a signature envelope against a content hash.")
	contentHash := fs.String("content-hash", "", "sha256:<hex> content hash (required)")
	signature := fs.String("signature", "", "signature envelope (required)")
	providerName := fs.String("provider", envDefault("PODIUM_SIGNATURE_PROVIDER", "noop"), "noop|registry-managed|sigstore-keyless")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *contentHash == "" || *signature == "" {
		fmt.Fprintln(os.Stderr, "error: --content-hash and --signature are required")
		return 2
	}
	provider, err := loadSignatureProvider(*providerName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := provider.Verify(*contentHash, *signature); err != nil {
		fmt.Fprintf(os.Stderr, "verify failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "verify ok")
	return 0
}

// loadSignatureProvider builds a signing provider from the named
// flag plus the §13 PODIUM_SIGSTORE_* / PODIUM_REGISTRY_KEY env
// vars. The Noop provider needs no config and is the default.
func loadSignatureProvider(name string) (sign.Provider, error) {
	switch strings.ToLower(name) {
	case "", "noop":
		return sign.Noop{}, nil
	case "sigstore-keyless":
		root, _ := os.ReadFile(os.Getenv("PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE"))
		return sign.SigstoreKeyless{
			FulcioURL: os.Getenv("PODIUM_SIGSTORE_FULCIO_URL"),
			RekorURL:  os.Getenv("PODIUM_SIGSTORE_REKOR_URL"),
			OIDCToken: os.Getenv("PODIUM_SIGSTORE_OIDC_TOKEN"),
			TrustRoot: root,
		}, nil
	case "registry-managed":
		// Loading the per-tenant key from the secret backend is the
		// production path. The CLI shim here returns the structured
		// type without a key; callers wiring this end-to-end supply
		// keys via env or a file in subsequent passes.
		return sign.RegistryManagedKey{}, nil
	}
	return nil, fmt.Errorf("unknown signature provider: %s", name)
}
