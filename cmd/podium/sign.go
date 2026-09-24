package main

import (
	"context"
	"crypto/ed25519"
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
	providerName := fs.String("provider", envDefault("PODIUM_SIGNATURE_PROVIDER", "registry-managed"), "noop|registry-managed|sigstore-keyless")
	fs.SetOutput(os.Stderr)
	artifact, _, err := parsePositional(fs, args)
	if err != nil {
		return parseExit(err)
	}

	hash := *contentHash
	if artifact != "" {
		if *contentHash != "" {
			fmt.Fprintln(os.Stderr, "error: pass either <artifact> or --content-hash, not both")
			return 2
		}
		if *registry == "" {
			fmt.Fprintln(os.Stderr, "error: --registry is required to resolve <artifact>")
			return 2
		}
		h, _, code := resolveArtifactSignature(*registry, artifact)
		if code != 0 {
			return code
		}
		hash = h
	}
	if hash == "" {
		fmt.Fprintln(os.Stderr, "error: provide <artifact> or --content-hash sha256:<hex>")
		return 2
	}

	provider, err := loadSignatureProvider(*providerName, keyForSign)
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
	providerName := fs.String("provider", envDefault("PODIUM_SIGNATURE_PROVIDER", "registry-managed"), "noop|registry-managed|sigstore-keyless")
	fs.SetOutput(os.Stderr)
	artifact, _, err := parsePositional(fs, args)
	if err != nil {
		return parseExit(err)
	}

	hash := *contentHash
	sig := *signature
	if artifact != "" {
		if *contentHash != "" {
			fmt.Fprintln(os.Stderr, "error: pass either <artifact> or --content-hash, not both")
			return 2
		}
		if *registry == "" {
			fmt.Fprintln(os.Stderr, "error: --registry is required to resolve <artifact>")
			return 2
		}
		h, storedSig, code := resolveArtifactSignature(*registry, artifact)
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
			fmt.Fprintf(os.Stderr, "verify failed: artifact %s has no stored signature; ingest with a signer or pass --signature\n", artifact)
			return 1
		}
	}
	if hash == "" || sig == "" {
		fmt.Fprintln(os.Stderr, "error: provide <artifact>, or --content-hash and --signature")
		return 2
	}

	provider, err := loadSignatureProvider(*providerName, keyForVerify)
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

// keyUse names the half of the registry-managed keypair a command needs.
type keyUse int

const (
	// keyForSign resolves the private half, for podium sign.
	keyForSign keyUse = iota
	// keyForVerify resolves the public half, for podium verify.
	keyForVerify
)

// loadSignatureProvider builds the named provider. The noop and
// sigstore-keyless arms ignore use; sigstore-keyless reads the
// PODIUM_SIGSTORE_* variables. The registry-managed arm, the default, resolves
// exactly the half use names: the public half from PODIUM_SIGNATURE_VERIFY_KEY
// when set (authoritative, so a malformed value is an error) and otherwise
// from the registry key file at sign.KeyFilePath(PODIUM_SIGN_KEY_PATH); the
// private half from that key file alone, because the variable carries a public
// key only. On a standalone machine both resolve the file the registry
// generated. A failed resolution leads with config.signature_provider_unavailable
// and returns before any Sign or Verify call.
//
// Spec: §4.7.9, §6.2.
func loadSignatureProvider(name string, use keyUse) (sign.Provider, error) {
	switch strings.ToLower(name) {
	case "noop":
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
		if use == keyForSign {
			return registryManagedSigner()
		}
		return registryManagedVerifier()
	}
	return nil, fmt.Errorf("unknown signature provider: %s", name)
}

// registryManagedSigner reads the private half from the registry key file.
// PODIUM_SIGNATURE_VERIFY_KEY is not read.
func registryManagedSigner() (sign.Provider, error) {
	path, err := sign.KeyFilePath(os.Getenv("PODIUM_SIGN_KEY_PATH"))
	if err == nil {
		var priv ed25519.PrivateKey
		if priv, err = sign.PrivateKeyFromKeyFile(path); err == nil {
			return sign.RegistryManagedKey{PrivateKey: priv}, nil
		}
	}
	return nil, fmt.Errorf("config.signature_provider_unavailable: the registry key file (PODIUM_SIGN_KEY_PATH) yields no private key to sign with: %w", err)
}

// registryManagedVerifier resolves the public half in the §4.7.9 order.
func registryManagedVerifier() (sign.Provider, error) {
	if raw := os.Getenv("PODIUM_SIGNATURE_VERIFY_KEY"); raw != "" {
		pub, err := sign.PublicKeyFromBase64(raw)
		if err != nil {
			return nil, fmt.Errorf("config.signature_provider_unavailable: PODIUM_SIGNATURE_VERIFY_KEY: %w", err)
		}
		return sign.RegistryManagedKey{PublicKey: pub}, nil
	}
	path, err := sign.KeyFilePath(os.Getenv("PODIUM_SIGN_KEY_PATH"))
	if err == nil {
		var pub ed25519.PublicKey
		if pub, err = sign.PublicKeyFromKeyFile(path); err == nil {
			return sign.RegistryManagedKey{PublicKey: pub}, nil
		}
	}
	return nil, fmt.Errorf("config.signature_provider_unavailable: PODIUM_SIGNATURE_VERIFY_KEY is unset and the registry key file (PODIUM_SIGN_KEY_PATH) yields no usable public key: %w", err)
}
