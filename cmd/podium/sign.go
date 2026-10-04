package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

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
		served, code := resolveArtifactSignature(*registry, artifact)
		if code != 0 {
			return code
		}
		hash = served.ContentHash
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

// verifyCmd verifies a signature envelope. spec §4.7.9: `podium verify
// <artifact>` performs ad-hoc verification of the §4.7.10 delivery signature
// the registry serves for the artifact over the delivery hash it serves. With
// an explicit --signature, the <artifact> form verifies that envelope over the
// artifact's content hash instead, so `podium verify <artifact> --signature
// "$(podium sign <artifact>)"` round-trips. The lower-level `--content-hash` +
// `--signature` form verifies an explicit pair without resolving an artifact.
// Exits 0 on a valid signature, 1 on mismatch or other error.
//
//	podium verify <artifact> [--signature <envelope>] [--registry URL] [--provider ...]
//	podium verify --content-hash sha256:... --signature <envelope> [--provider ...]
func verifyCmd(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	setUsage(fs, "Verify an artifact's delivery signature (or an explicit content hash + signature).")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL (resolves the <artifact> form)")
	contentHash := fs.String("content-hash", "", "sha256:<hex> content hash (lower-level alternative to <artifact>)")
	signature := fs.String("signature", "", "signature envelope; with <artifact>, verified over its content hash in place of the delivery signature")
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
		served, code := resolveArtifactSignature(*registry, artifact)
		if code != 0 {
			return code
		}
		if hash, sig, code = verifiedPair(artifact, served, sig); code != 0 {
			return code
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

// verifiedPair selects the (hash, signature) pair the <artifact> form of
// verifyCmd checks. An explicit envelope is checked over the content hash,
// which is what `podium sign <artifact>` signs; only the registry mints a
// signature over a per-response delivery hash. Without one, the served
// delivery pair is checked, and a response missing either half is refused
// here rather than in resolveArtifactSignature, because `podium sign` reads
// only the content hash.
//
// Spec: §4.7.9, §4.7.10.
func verifiedPair(artifact string, served servedAttestation, explicitSig string) (hash, sig string, code int) {
	if explicitSig != "" {
		return served.ContentHash, explicitSig, 0
	}
	if served.DeliveryHash == "" {
		fmt.Fprintf(os.Stderr, "verify failed: resolve %s: registry returned no delivery hash\n", artifact)
		return "", "", 1
	}
	if served.DeliverySignature == "" {
		fmt.Fprintf(os.Stderr, "verify failed: artifact %s has no delivery signature; the registry runs without a signing key\n", artifact)
		return "", "", 1
	}
	return served.DeliveryHash, served.DeliverySignature, 0
}

// servedAttestation is the integrity material a load_artifact response
// carries: the §4.7.6 content hash and the §4.7.10 delivery pair.
type servedAttestation struct {
	ContentHash       string `json:"content_hash"`
	DeliveryHash      string `json:"delivery_hash"`
	DeliverySignature string `json:"delivery_signature"`
}

// resolveArtifactSignature fetches an artifact's content hash and delivery
// pair via the registry's load_artifact path. spec: §4.7.9 — the `<artifact>`
// form of `podium sign` / `podium verify` resolves the artifact rather than
// operating on a raw hash. A non-zero code is the process exit to return; the
// caller stops on a non-zero.
func resolveArtifactSignature(registry, artifactID string) (servedAttestation, int) {
	endpoint := registry + "/v1/load_artifact?id=" + url.QueryEscape(artifactID)
	out, status := doJSON(endpoint, "GET", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "resolve %s failed: HTTP %d\n%s\n", artifactID, status, out)
		return servedAttestation{}, 1
	}
	var resp servedAttestation
	if err := json.Unmarshal(out, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "resolve %s: decode response: %v\n", artifactID, err)
		return servedAttestation{}, 1
	}
	if resp.ContentHash == "" {
		fmt.Fprintf(os.Stderr, "resolve %s: registry returned no content hash\n", artifactID)
		return servedAttestation{}, 1
	}
	return resp, 0
}

// keyUse names the half of the registry-managed keypair a command needs.
type keyUse int

const (
	// keyForSign resolves the private half, for podium sign.
	keyForSign keyUse = iota
	// keyForVerify resolves the verification key set, for podium verify.
	keyForVerify
)

// loadSignatureProvider builds the named provider. The noop arm ignores use.
// The sigstore-keyless arm is built by sigstoreKeylessProvider from the
// PODIUM_SIGSTORE_* variables, and only its sign use reads
// PODIUM_SIGSTORE_REQUEST_TIMEOUT. The registry-managed arm, the default,
// resolves
// exactly the material use names: the verification key set from
// PODIUM_SIGNATURE_VERIFY_KEY when set (authoritative, so a malformed list is
// an error) and otherwise from the public: and verify: lines of the registry
// key file at sign.KeyFilePath(PODIUM_SIGN_KEY_PATH); the private half from
// that key file alone, because the variable carries public keys only. On a
// standalone machine both resolve the file the registry generated. A failed
// resolution leads with config.signature_provider_unavailable and returns
// before any Sign or Verify call.
//
// Spec: §4.7.9, §6.2.
func loadSignatureProvider(name string, use keyUse) (sign.Provider, error) {
	switch strings.ToLower(name) {
	case "noop":
		return sign.Noop{}, nil
	case "sigstore-keyless":
		return sigstoreKeylessProvider(use)
	case "registry-managed":
		if use == keyForSign {
			return registryManagedSigner()
		}
		return registryManagedVerifier()
	}
	return nil, fmt.Errorf("unknown signature provider: %s", name)
}

// The §6.2 defaults for the sigstore-keyless signing endpoints. The library
// applies no default, so only the CLI ever reaches a public-good instance.
// The Rekor value is the Rekor v2 shard the public-good signing_config lists
// as current; the shard rotates, so a later release updates it.
//
// Spec: §6.2.
const (
	defaultFulcioURL = "https://fulcio.sigstore.dev"
	defaultRekorURL  = "https://log2025-1.rekor.sigstore.dev"
	defaultTSAURL    = "https://timestamp.sigstore.dev/api/v1/timestamp"
)

// sigstoreKeylessProvider builds the keyless provider from the environment.
// The trust root is the trusted_root.json at
// PODIUM_SIGSTORE_TRUSTED_ROOT_FILE; a read failure leaves it empty rather
// than failing the command, because Verify refuses every envelope against an
// empty trust root and Sign does not read it. The signing endpoints take their
// §6.2 defaults when unset or empty. The identity policy comes from
// PODIUM_SIGSTORE_CERT_IDENTITY and PODIUM_SIGSTORE_CERT_OIDC_ISSUER, and an
// incomplete policy makes Verify refuse every envelope. For keyForSign the
// per-request deadline comes from PODIUM_SIGSTORE_REQUEST_TIMEOUT, and an
// invalid value fails before Sign contacts any Sigstore endpoint. For
// keyForVerify the variable is not read, because verification is offline and
// a stray value must not break it.
//
// Spec: §4.7.9, §6.2.
func sigstoreKeylessProvider(use keyUse) (sign.SigstoreKeyless, error) {
	var timeout time.Duration
	if use == keyForSign {
		var err error
		if timeout, err = sigstoreRequestTimeout(); err != nil {
			return sign.SigstoreKeyless{}, err
		}
	}
	root, _ := os.ReadFile(os.Getenv("PODIUM_SIGSTORE_TRUSTED_ROOT_FILE"))
	return sign.SigstoreKeyless{
		FulcioURL: envDefault("PODIUM_SIGSTORE_FULCIO_URL", defaultFulcioURL),
		RekorURL:  envDefault("PODIUM_SIGSTORE_REKOR_URL", defaultRekorURL),
		TSAURL:    envDefault("PODIUM_SIGSTORE_TSA_URL", defaultTSAURL),
		OIDCToken: os.Getenv("PODIUM_SIGSTORE_OIDC_TOKEN"),
		TrustRoot: root,
		Identity: sign.NewIdentityPolicy(
			os.Getenv("PODIUM_SIGSTORE_CERT_IDENTITY"),
			os.Getenv("PODIUM_SIGSTORE_CERT_OIDC_ISSUER"),
		),
		RequestTimeout: timeout,
	}, nil
}

// sigstoreRequestTimeout reads PODIUM_SIGSTORE_REQUEST_TIMEOUT. An unset
// value, or one that is blank after trimming, takes the library default. A
// value that is not a positive duration is refused with config.invalid,
// because an interactive command reports an operator's typo rather than
// silently signing under a different bound.
//
// Spec: §4.7.9, §6.2.
func sigstoreRequestTimeout() (time.Duration, error) {
	raw := os.Getenv("PODIUM_SIGSTORE_REQUEST_TIMEOUT")
	v := strings.TrimSpace(raw)
	if v == "" {
		return sign.DefaultRequestTimeout, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("config.invalid: PODIUM_SIGSTORE_REQUEST_TIMEOUT %q is not a positive duration such as 60s", raw)
	}
	return d, nil
}

// registryManagedSigner reads the private half from the registry key file.
// PODIUM_SIGNATURE_VERIFY_KEY is not read. A key file with no private: line, a
// consumer's public-only copy, is refused before any provider is built, so it
// never yields a signer with a nil private key.
//
// Spec: §4.7.9.
func registryManagedSigner() (sign.Provider, error) {
	path, err := sign.KeyFilePath(os.Getenv("PODIUM_SIGN_KEY_PATH"))
	if err == nil {
		var kf sign.KeyFile
		if kf, err = sign.ReadKeyFile(path); err == nil {
			if kf.Private != nil {
				return sign.RegistryManagedKey{PrivateKey: kf.Private}, nil
			}
			err = fmt.Errorf("key file %s carries no \"private:\" line", path)
		}
	}
	return nil, fmt.Errorf("config.signature_provider_unavailable: the registry key file (PODIUM_SIGN_KEY_PATH) yields no private key to sign with: %w", err)
}

// registryManagedVerifier resolves the verification key set in the §4.7.9
// order through sign.VerificationKeys, the resolver podium-mcp also uses.
//
// Spec: §4.7.9, §6.2.
func registryManagedVerifier() (sign.Provider, error) {
	keys, err := sign.VerificationKeys(os.Getenv("PODIUM_SIGNATURE_VERIFY_KEY"), os.Getenv("PODIUM_SIGN_KEY_PATH"))
	if err != nil {
		return nil, fmt.Errorf("config.signature_provider_unavailable: %w", err)
	}
	return sign.RegistryManagedKey{Trusted: keys}, nil
}
