package sign

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/lennylabs/podium/pkg/spi"
)

// SigstoreKeyless implements §4.7.9 Sigstore-keyless signing.
//
// Sign generates an ephemeral ECDSA P-256 key, mints a short-lived
// certificate from Fulcio with the configured OIDC token, signs the
// SHA-256 content hash with the ephemeral key, obtains an RFC 3161
// timestamp over the signature from the timestamp authority, and records
// the entry in a Rekor v2 transparency log. The returned signature is a
// version 0.3 Sigstore bundle that carries the leaf certificate, the log
// entry with its inclusion proof and signed checkpoint, the timestamp
// response, and the message signature over the digest.
//
// Verify is offline and runs on sigstore-go. It applies the §4.7.9
// acceptance conditions: the timestamp condition and the verified times,
// the leaf's chain, validity, and code-signing usage at each verified
// time, the log entry and its inclusion proof under the log key its log
// ID names, the signature over the digest, and the identity policy.
//
// Test code points the three URLs at an httptest fixture and supplies
// Client; production code points them at the live or staging endpoints
// and leaves Client nil.
type SigstoreKeyless struct {
	// FulcioURL is the Fulcio CA endpoint, e.g.
	// "https://fulcio.sigstore.dev". Required for Sign.
	FulcioURL string
	// RekorURL is the base URL of the Rekor v2 transparency log. Required
	// for Sign; Verify does not read it.
	RekorURL string
	// TSAURL is the RFC 3161 timestamp-authority endpoint. Required for
	// Sign; Verify does not read it.
	TSAURL string
	// OIDCToken is the caller's identity token used to mint the
	// short-lived signing cert. Required for Sign. Verify ignores it.
	OIDCToken string
	// TrustRoot holds the bytes of a Sigstore trusted_root.json. Verify
	// loads it on every call by the §4.7.11 load rules; empty means no
	// trust and Verify refuses every bundle. Sign ignores it.
	TrustRoot []byte
	// Identity is the §4.7.9 signer identity Verify accepts. An empty
	// policy accepts no bundle.
	Identity IdentityPolicy
	// Client overrides the HTTP client Sign uses for Fulcio, Rekor, and
	// the timestamp authority. Production leaves it nil to use
	// http.DefaultClient. The per-request context deadline bounds each
	// call, including calls made through an injected Client.
	Client *http.Client
	// RequestTimeout is the deadline on each request Sign makes. It covers
	// sending the request and reading the whole response. Zero or a negative
	// value means DefaultRequestTimeout, so no value leaves a request unbounded.
	// When the caller's own context is cancelled or reaches its own deadline
	// first, Sign returns that error unchanged, without the "(no response
	// within D)" suffix that marks a missed per-request deadline.
	RequestTimeout time.Duration
}

// DefaultRequestTimeout is the deadline on each Fulcio, timestamp-authority,
// and Rekor request Sign makes when RequestTimeout is not positive.
//
// Spec: §4.7.9, §6.2.
const DefaultRequestTimeout = 60 * time.Second

// ID returns "sigstore-keyless".
func (SigstoreKeyless) ID() string { return "sigstore-keyless" }

// httpClient returns the configured HTTP client, defaulting to
// http.DefaultClient. The client carries no Timeout of its own: post
// bounds each call with a per-request context deadline, which also
// applies to an injected Client.
func (s SigstoreKeyless) httpClient() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

// requestTimeout returns the per-request deadline, falling back to
// DefaultRequestTimeout when RequestTimeout is zero or negative.
func (s SigstoreKeyless) requestTimeout() time.Duration {
	if s.RequestTimeout > 0 {
		return s.RequestTimeout
	}
	return DefaultRequestTimeout
}

// post sends req once with the configured client under a per-request
// deadline derived from req's context, and returns the body of a 2xx
// response. The deadline covers sending the request and reading the
// whole body, so cancel fires on return, after the read. A missed
// per-request deadline gains a "(no response within D)" suffix; a
// cancellation or deadline on the caller's own context does not, so the
// caller can tell the two apart.
//
// Spec: §4.7.9, §6.2.
func (s SigstoreKeyless) post(req *http.Request) ([]byte, error) {
	parent := req.Context()
	timeout := s.requestTimeout()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	body, err := s.roundTrip(req.WithContext(ctx))
	if err != nil && errors.Is(err, context.DeadlineExceeded) && parent.Err() == nil {
		return nil, fmt.Errorf("%w (no response within %s)", err, timeout)
	}
	return body, err
}

// roundTrip performs one request and returns the body of a 2xx response.
// A Do error is returned unchanged because it is a *url.Error that already
// names the URL; the read and status errors name it explicitly.
func (s SigstoreKeyless) roundTrip(req *http.Request) ([]byte, error) {
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", req.URL, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, req.URL, body)
	}
	return body, nil
}

// maxResponseBytes bounds a Fulcio, Rekor, or timestamp-authority
// response body.
// A genuine response is a few kilobytes.
const maxResponseBytes = 4 << 20

// ErrSigstoreUnavailable signals that the Sigstore endpoints are not
// configured and the keyless flow cannot proceed. Sign returns it when
// FulcioURL, RekorURL, TSAURL, or OIDCToken is empty, before any network
// call. Structured per §9.3.
var ErrSigstoreUnavailable = &spi.Error{Code: "config.signature_provider_unavailable", Message: "sign: sigstore-keyless not configured"}

// Sign produces a Sigstore-keyless bundle over contentHash, which must be
// of the form "sha256:hex". A malformed or non-SHA-256 hash returns an
// error before the network is touched.
//
// Spec: §4.7.9.
func (s SigstoreKeyless) Sign(ctx context.Context, contentHash string) (string, error) {
	if s.FulcioURL == "" || s.RekorURL == "" || s.TSAURL == "" || s.OIDCToken == "" {
		return "", fmt.Errorf("%w: FulcioURL, RekorURL, TSAURL, and OIDCToken are all required", ErrSigstoreUnavailable)
	}
	digest, err := sha256Digest(contentHash)
	if err != nil {
		return "", err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("ephemeral key: %w", err)
	}
	// The bundle carries the leaf alone; the trusted root supplies the
	// intermediates a verifier chains through.
	leaf, _, err := s.mintCert(ctx, priv)
	if err != nil {
		return "", fmt.Errorf("fulcio: %w", err)
	}
	sig, err := ecdsa.SignASN1(rand.Reader, priv, digest)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	response, err := s.requestTimestamp(ctx, sig)
	if err != nil {
		return "", fmt.Errorf("tsa: %w", err)
	}
	entry, err := s.uploadRekor(ctx, digest, sig, leaf)
	if err != nil {
		return "", fmt.Errorf("rekor: %w", err)
	}
	return keylessBundleJSON(leaf, digest, sig, entry, response)
}

// keylessBundleJSON encodes the §4.7.9 keyless bundle: a version 0.3
// Sigstore bundle holding the leaf certificate, the Rekor v2 log entry,
// the RFC 3161 timestamp response, and the message signature over the
// SHA-256 digest. The bundle carries the leaf alone, because a verifier
// takes the intermediates from its trusted root.
//
// Spec: §4.7.9.
func keylessBundleJSON(leaf *x509.Certificate, digest, sig []byte, entry *protorekor.TransparencyLogEntry, timestampResponse []byte) (string, error) {
	pb := &protobundle.Bundle{
		MediaType: attestationMediaTypeV03,
		VerificationMaterial: &protobundle.VerificationMaterial{
			Content: &protobundle.VerificationMaterial_Certificate{
				Certificate: &protocommon.X509Certificate{RawBytes: leaf.Raw},
			},
			TlogEntries: []*protorekor.TransparencyLogEntry{entry},
			TimestampVerificationData: &protobundle.TimestampVerificationData{
				Rfc3161Timestamps: []*protocommon.RFC3161SignedTimestamp{{SignedTimestamp: timestampResponse}},
			},
		},
		Content: &protobundle.Bundle_MessageSignature{MessageSignature: &protocommon.MessageSignature{
			MessageDigest: &protocommon.HashOutput{Algorithm: protocommon.HashAlgorithm_SHA2_256, Digest: digest},
			Signature:     sig,
		}},
	}
	// protojson.Marshal fails only on an invalid message, such as one with
	// a string that is not UTF-8, and this message holds bytes and enums.
	out, err := protojson.Marshal(pb)
	if err != nil {
		return "", fmt.Errorf("bundle: %w", err)
	}
	return string(out), nil
}

// Verify validates a Sigstore-keyless bundle offline. Every failure
// wraps ErrSignatureInvalid. ctx is unused: Verify makes no network call.
//
// Spec: §4.7.9.
func (s SigstoreKeyless) Verify(_ context.Context, contentHash, signature string) error {
	if err := s.verify(contentHash, signature); err != nil {
		return keylessRefusal(err)
	}
	return nil
}
