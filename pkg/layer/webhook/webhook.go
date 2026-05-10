// Package webhook implements the webhook signature verification half
// of §7.3.1 ingest. The registry receives webhook deliveries from
// configured Git providers (GitHub, GitLab, Bitbucket via the
// GitProvider SPI) and verifies HMAC signatures before fetching the
// referenced commit. Failed verifications log ingest.webhook_invalid
// and never reach the content store.
package webhook

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"strings"
)

// Errors returned by Verify functions.
var (
	// ErrInvalidSignature signals the signature does not match
	// HMAC(body, secret). Maps to ingest.webhook_invalid in §6.10.
	ErrInvalidSignature = errors.New("webhook: invalid_signature")
	// ErrUnsupportedScheme signals an unrecognized signature scheme
	// (e.g., a header missing the expected `sha256=` prefix).
	ErrUnsupportedScheme = errors.New("webhook: unsupported_signature_scheme")
)

// Provider is the interface every Git provider's webhook verifier
// satisfies. Built-ins cover GitHub; GitLab and Bitbucket plug in
// through the same SPI.
type Provider interface {
	// ID returns the provider identifier (e.g., "github").
	ID() string
	// Verify checks that signature is the HMAC of body under secret.
	Verify(body []byte, signature, secret string) error
}

// GitHub verifies GitHub-style webhook signatures. The signature is
// passed in the X-Hub-Signature-256 header in the form "sha256=<hex>".
// Older webhooks use X-Hub-Signature with "sha1=<hex>"; that form is
// supported as a fallback.
type GitHub struct{}

// ID returns "github".
func (GitHub) ID() string { return "github" }

// Verify expects signature in the form "sha256=<hex>" or
// "sha1=<hex>". The body is the raw request body bytes.
func (GitHub) Verify(body []byte, signature, secret string) error {
	if signature == "" || secret == "" {
		return ErrInvalidSignature
	}
	parts := strings.SplitN(signature, "=", 2)
	if len(parts) != 2 {
		return ErrUnsupportedScheme
	}
	scheme, hex := parts[0], parts[1]
	var h func() hash.Hash
	switch scheme {
	case "sha256":
		h = sha256.New
	case "sha1":
		h = sha1.New
	default:
		return ErrUnsupportedScheme
	}
	if !verifyHMAC(body, []byte(secret), hex, h) {
		return ErrInvalidSignature
	}
	return nil
}

// GitLab verifies GitLab webhook signatures. GitLab uses a shared
// secret token sent in the X-Gitlab-Token header — equality is the
// validation, not HMAC. The Verify function still takes a body
// argument for interface symmetry; it is unused.
type GitLab struct{}

// ID returns "gitlab".
func (GitLab) ID() string { return "gitlab" }

// Verify compares signature to secret in constant time.
func (GitLab) Verify(_ []byte, signature, secret string) error {
	if signature == "" || secret == "" {
		return ErrInvalidSignature
	}
	if !hmac.Equal([]byte(signature), []byte(secret)) {
		return ErrInvalidSignature
	}
	return nil
}

// Bitbucket verifies Bitbucket Cloud webhook signatures. Bitbucket
// signs payloads with HMAC-SHA256 in the X-Hub-Signature header
// (similar to GitHub but always sha256 prefix-less).
type Bitbucket struct{}

// ID returns "bitbucket".
func (Bitbucket) ID() string { return "bitbucket" }

// Verify computes HMAC-SHA256(body, secret) and constant-time-compares
// against the received hex digest.
func (Bitbucket) Verify(body []byte, signature, secret string) error {
	if signature == "" || secret == "" {
		return ErrInvalidSignature
	}
	if !verifyHMAC(body, []byte(secret), signature, sha256.New) {
		return ErrInvalidSignature
	}
	return nil
}

// verifyHMAC computes hash(body, key) and constant-time-compares
// against expectedHex.
func verifyHMAC(body, key []byte, expectedHex string, h func() hash.Hash) bool {
	mac := hmac.New(h, key)
	_, _ = mac.Write(body)
	want := mac.Sum(nil)
	got, err := hex.DecodeString(expectedHex)
	if err != nil {
		return false
	}
	return hmac.Equal(want, got)
}

// Sign produces a signature for body under secret using the named
// provider. Used by tests to round-trip and by integrators that mint
// outbound webhooks.
func Sign(providerID string, body []byte, secret string) (string, error) {
	switch providerID {
	case "github":
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil)), nil
	case "gitlab":
		return secret, nil
	case "bitbucket":
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		return hex.EncodeToString(mac.Sum(nil)), nil
	}
	return "", ErrUnsupportedScheme
}
