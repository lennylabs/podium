package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/lennylabs/podium/pkg/sign"
)

// signCmd produces a signature envelope for an artifact's canonical
// content hash. spec §4.7.9: `podium sign <artifact>` for explicit
// signing outside the ingest flow. The lower-level `--content-hash`
// form signs a raw hash without resolving an artifact.
//
//	podium sign <artifact> [--registry URL] [--provider ...]
//	podium sign --content-hash sha256:... [--provider ...]
func signCmd(args []string) int {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	setUsage(fs, "Sign an artifact (or an explicit content hash) via the configured signature provider.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL (resolves the <artifact> form)")
	contentHash := fs.String("content-hash", "", "sha256:<hex> content hash (lower-level alternative to <artifact>)")
	providerName := fs.String("provider", envDefault("PODIUM_SIGNATURE_PROVIDER", "noop"), "noop|registry-managed|sigstore-keyless")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}

	hash := *contentHash
	if fs.NArg() > 0 {
		if *contentHash != "" {
			fmt.Fprintln(os.Stderr, "error: pass either <artifact> or --content-hash, not both")
			return 2
		}
		if *registry == "" {
			fmt.Fprintln(os.Stderr, "error: --registry is required to resolve <artifact>")
			return 2
		}
		h, _, code := resolveArtifactSignature(*registry, fs.Arg(0))
		if code != 0 {
			return code
		}
		hash = h
	}
	if hash == "" {
		fmt.Fprintln(os.Stderr, "error: provide <artifact> or --content-hash sha256:<hex>")
		return 2
	}

	provider, err := loadSignatureProvider(*providerName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	envelope, err := provider.Sign(context.Background(), hash)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign failed: %v\n", err)
		return 1
	}
	fmt.Println(envelope)
	return 0
}

// verifyCmd verifies an artifact's stored signature against its
// canonical content hash. spec §4.7.9: `podium verify <artifact>` for
// ad-hoc verification. The lower-level `--content-hash` + `--signature`
// form verifies an explicit pair without resolving an artifact. Exits 0
// on a valid signature, 1 on mismatch or other error.
//
//	podium verify <artifact> [--registry URL] [--provider ...]
//	podium verify --content-hash sha256:... --signature <envelope> [--provider ...]
func verifyCmd(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	setUsage(fs, "Verify an artifact's stored signature (or an explicit content hash + signature).")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL (resolves the <artifact> form)")
	contentHash := fs.String("content-hash", "", "sha256:<hex> content hash (lower-level alternative to <artifact>)")
	signature := fs.String("signature", "", "signature envelope (lower-level; pairs with --content-hash)")
	providerName := fs.String("provider", envDefault("PODIUM_SIGNATURE_PROVIDER", "noop"), "noop|registry-managed|sigstore-keyless")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}

	hash := *contentHash
	sig := *signature
	if fs.NArg() > 0 {
		if *contentHash != "" {
			fmt.Fprintln(os.Stderr, "error: pass either <artifact> or --content-hash, not both")
			return 2
		}
		if *registry == "" {
			fmt.Fprintln(os.Stderr, "error: --registry is required to resolve <artifact>")
			return 2
		}
		h, storedSig, code := resolveArtifactSignature(*registry, fs.Arg(0))
		if code != 0 {
			return code
		}
		hash = h
		// An explicit --signature overrides; otherwise verify the envelope
		// the registry stored at ingest.
		if sig == "" {
			sig = storedSig
		}
		if sig == "" {
			fmt.Fprintf(os.Stderr, "verify failed: artifact %s has no stored signature; ingest with a signer or pass --signature\n", fs.Arg(0))
			return 1
		}
	}
	if hash == "" || sig == "" {
		fmt.Fprintln(os.Stderr, "error: provide <artifact>, or --content-hash and --signature")
		return 2
	}

	provider, err := loadSignatureProvider(*providerName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := provider.Verify(context.Background(), hash, sig); err != nil {
		fmt.Fprintf(os.Stderr, "verify failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "verify ok")
	return 0
}

// resolveArtifactSignature fetches an artifact's canonical content hash
// and stored signature envelope via the registry's load_artifact path.
// spec: §4.7.9 — the `<artifact>` form of `podium sign` / `podium verify`
// resolves the artifact rather than operating on a raw hash. A non-zero
// code is the process exit to return; the caller stops on a non-zero.
func resolveArtifactSignature(registry, artifactID string) (hash, signature string, code int) {
	endpoint := registry + "/v1/load_artifact?id=" + url.QueryEscape(artifactID)
	out, status := doJSON(endpoint, "GET", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "resolve %s failed: HTTP %d\n%s\n", artifactID, status, out)
		return "", "", 1
	}
	var resp struct {
		ContentHash string `json:"content_hash"`
		Signature   string `json:"signature"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "resolve %s: decode response: %v\n", artifactID, err)
		return "", "", 1
	}
	if resp.ContentHash == "" {
		fmt.Fprintf(os.Stderr, "resolve %s: registry returned no content hash\n", artifactID)
		return "", "", 1
	}
	return resp.ContentHash, resp.Signature, 0
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
