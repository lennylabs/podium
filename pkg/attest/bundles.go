package attest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/podium/pkg/version"
)

// ErrBundleNotFound reports a digest the registry answers with not found.
// The consumer procedure treats its reference as unusable (§4.7.11 step 8).
var ErrBundleNotFound = errors.New("attest: the registry holds no bundle under the digest")

// ErrBundleUnavailable reports a registry that answered a bundle fetch with
// a 5xx status. The consumer procedure treats the reference as unusable
// and goes on with the others (§4.7.11 step 8).
var ErrBundleUnavailable = errors.New("attest: the registry could not serve the bundle")

// DefaultBundleFetchTimeout bounds a bundle fetch of an HTTPBundleSource
// that has no client of its own. A consumer that passes its registry
// client uses that client's deadline instead, as §4.7.11 step 8 states.
const DefaultBundleFetchTimeout = 30 * time.Second

// BundleSource supplies the bytes of a bundle by its attestation digest,
// sha256:<hex>. An implementation returns ErrBundleNotFound for a digest
// the registry does not serve, a *Failure for a failure with a code of its
// own, and any other error for a registry it cannot reach.
type BundleSource interface {
	Bundle(ctx context.Context, digest string) ([]byte, error)
}

// maxReferences is the most attestation references a record may carry
// (§4.7.11 step 7).
const maxReferences = 16

// responseSlack is how far past the base64 length of a bundle at the size
// limit a consumer reads a response body before it stops (§4.7.11 step 8).
const responseSlack = 1024

// HTTPBundleSource fetches bundles from a registry's
// GET /v1/attestations/{digest} endpoint (§7.3.5).
type HTTPBundleSource struct {
	// BaseURL is the registry's base URL.
	BaseURL string
	// Client sends the request. It carries the consumer's deadline, as a
	// load_artifact request does; the context of each call bounds it too.
	Client *http.Client
	// Prepare sets the request's credential and client headers. It may be
	// nil.
	Prepare func(*http.Request)
	// MaxBytes is the consumer's PODIUM_ATTESTATION_MAX_BYTES; zero is the
	// §13.12 default.
	MaxBytes int64
	// Timeout bounds each fetch when Client is nil; zero is
	// DefaultBundleFetchTimeout.
	Timeout time.Duration
}

// Bundle fetches the bundle with the digest. The body is read up to the
// base64 length of a bundle at the size limit plus 1 KiB, and a longer body
// is a materialize.attestation_invalid Failure with the constraint limits.
// A body that is not a JSON object whose bundle member is a canonical base64
// string, and a 4xx answer other than 404, are invalid with the constraint
// fetch. A 5xx answer is ErrBundleUnavailable, and a request that does not
// reach the registry is a network.registry_unreachable Failure. A nil
// Client is replaced by one bounded by Timeout.
//
// Spec: §4.7.11 (Attestation check, step 8), §7.3.5
func (s HTTPBundleSource) Bundle(ctx context.Context, digest string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.BaseURL, "/")+"/v1/attestations/"+url.PathEscape(digest), nil)
	if err != nil {
		return nil, fail(CodeAttestationInvalid, ConstraintFetch, "the bundle request cannot be built", err)
	}
	if s.Prepare != nil {
		s.Prepare(req)
	}
	client := s.Client
	if client == nil {
		timeout := s.Timeout
		if timeout <= 0 {
			timeout = DefaultBundleFetchTimeout
		}
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fail(CodeRegistryUnreachable, "", "the bundle fetch did not reach the registry", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrBundleNotFound
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: status %d", ErrBundleUnavailable, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fail(CodeAttestationInvalid, ConstraintFetch, fmt.Sprintf("the registry answered the bundle fetch with %d", resp.StatusCode), nil)
	}
	limit := base64Len(maxBytesOr(s.MaxBytes)) + responseSlack
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fail(CodeRegistryUnreachable, "", "the bundle response could not be read", err)
	}
	if int64(len(body)) > limit {
		return nil, fail(CodeAttestationInvalid, ConstraintLimits, "the bundle response exceeds the size limit", nil)
	}
	return DecodeBundleResponse(body)
}

// DecodeBundleResponse reads the bundle bytes from a §7.3.5 response body:
// a JSON object, under the §4.7.10 step 1 JSON rule, whose bundle member is
// a string in canonical base64. Any other body is a
// materialize.attestation_invalid Failure with the constraint fetch.
//
// Spec: §7.3.5, §4.7.11 (Attestation check, step 8)
func DecodeBundleResponse(body []byte) ([]byte, error) {
	invalid := func(detail string, cause error) error {
		return fail(CodeAttestationInvalid, ConstraintFetch, detail, cause)
	}
	m, err := version.DecodeJSONObject(body)
	if err != nil {
		return nil, invalid("the bundle response is not a JSON object", err)
	}
	raw, ok := m["bundle"]
	var s string
	if !ok || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) || json.Unmarshal(raw, &s) != nil {
		return nil, invalid("the bundle response has no bundle string", nil)
	}
	b, err := version.DecodeBase64(s)
	if err != nil {
		return nil, invalid("the bundle is not in canonical base64", err)
	}
	return b, nil
}

// base64Len is the length of the standard padded base64 encoding of n
// bytes.
func base64Len(n int64) int64 { return (n + 2) / 3 * 4 }

// maxBytesOr returns n, or the §13.12 default when n is not positive.
func maxBytesOr(n int64) int64 {
	if n <= 0 {
		return DefaultMaxBundleBytes
	}
	return n
}

// BundleCache keeps bundle bytes by attestation digest. A bundle's bytes
// never change, so an entry never needs revalidation.
type BundleCache interface {
	// Get returns the cached bytes of digest.
	Get(digest string) ([]byte, bool)
	// Put stores the bytes of digest, which the caller has checked.
	Put(digest string, b []byte) error
}

// CachedBundles is the bundle source of a consumer: it reads a bundle from
// its cache, and otherwise fetches it once and keeps it, so the consumer
// fetches a bundle at most once for each digest (§4.7.11 step 8). A cached
// entry whose bytes do not have its digest is ignored. Under offline-only a
// bundle that is not cached is materialize.attestation_missing.
type CachedBundles struct {
	Cache       BundleCache
	Source      BundleSource
	OfflineOnly bool
}

// Bundle returns the bundle with the digest.
//
// Spec: §4.7.11 (Attestation check, step 8), §7.4
func (c CachedBundles) Bundle(ctx context.Context, digest string) ([]byte, error) {
	if c.Cache != nil {
		if b, ok := c.Cache.Get(digest); ok && AttestationDigest(b) == digest {
			return b, nil
		}
	}
	if c.OfflineOnly || c.Source == nil {
		return nil, fail(CodeAttestationMissing, "", "the bundle is not cached, and the consumer runs offline-only", nil)
	}
	b, err := c.Source.Bundle(ctx, digest)
	if err != nil {
		return nil, err
	}
	if c.Cache != nil && AttestationDigest(b) == digest {
		// A failed cache write costs a later fetch and nothing else, so it
		// is not the check's failure.
		_ = c.Cache.Put(digest, b)
	}
	return b, nil
}

// MemoryCache is a BundleCache held in memory, bounded to Max entries
// (default 256). It is safe for concurrent use.
type MemoryCache struct {
	// Max bounds the entries; reaching it empties the cache.
	Max int
	// mu guards m.
	mu sync.Mutex
	m  map[string][]byte
}

// Get returns the cached bytes.
func (c *MemoryCache) Get(digest string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.m[digest]
	return b, ok
}

// Put stores the bytes.
func (c *MemoryCache) Put(digest string, b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	limit := c.Max
	if limit <= 0 {
		limit = 256
	}
	if c.m == nil || len(c.m) >= limit {
		c.m = map[string][]byte{}
	}
	c.m[digest] = append([]byte(nil), b...)
	return nil
}

// DirCache is a BundleCache in a directory: each bundle is the file named
// by the 64 hexadecimal digits of its digest, as the files of an
// attestation directory are named without the sha256: prefix, which is not
// valid in a file name on Windows. A file is written to a temporary file in
// the directory and renamed into place, so a reader never sees a partial
// bundle.
type DirCache struct {
	// Dir is the cache directory, such as ${PODIUM_CACHE_DIR}/attestations.
	Dir string
	// MaxBytes bounds the bytes read from one file; zero is the §13.12
	// default.
	MaxBytes int64
}

// Get reads the bundle of digest. A file whose bytes do not have the
// digest, or that exceeds the size bound, is no entry.
func (c DirCache) Get(digest string) ([]byte, bool) {
	name, ok := cacheName(digest)
	if !ok {
		return nil, false
	}
	f, err := os.Open(filepath.Join(c.Dir, name))
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	limit := maxBytesOr(c.MaxBytes)
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit || AttestationDigest(b) != digest {
		return nil, false
	}
	return b, true
}

// Put writes the bundle of digest.
func (c DirCache) Put(digest string, b []byte) error {
	name, ok := cacheName(digest)
	if !ok {
		return fmt.Errorf("attest: bundle cache: %q is not an attestation digest", digest)
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return fmt.Errorf("attest: bundle cache: %w", err)
	}
	tmp, err := os.CreateTemp(c.Dir, ".bundle-*")
	if err != nil {
		return fmt.Errorf("attest: bundle cache: %w", err)
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("attest: bundle cache: write %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(c.Dir, name)); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("attest: bundle cache: %w", err)
	}
	return nil
}

// cacheName returns the file name of digest: its hexadecimal digits, when
// it is sha256: followed by 64 lowercase hexadecimal digits.
func cacheName(digest string) (string, bool) {
	hexPart, ok := strings.CutPrefix(digest, digestPrefix)
	if !ok || !isLowerHex(hexPart) {
		return "", false
	}
	return hexPart, true
}

// isLowerHex reports whether s is 64 lowercase hexadecimal digits.
func isLowerHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ReadReferences applies step 7 of the §4.7.11 attestation check to the
// exact-name member map of a served response: it returns the digest of each
// usable reference of the attestations member, in order and without
// repeats. A reference is usable when it is an object whose digest member is
// sha256: followed by 64 lowercase hexadecimal digits, and no other member
// is read, because the registry asserts them. An absent or null member
// holds no reference. A member that is no array, and an array of more than
// 16 elements, are a materialize.attestation_invalid Failure with the
// constraint references.
//
// Spec: §4.7.11 (Attestation check, step 7)
func ReadReferences(members map[string]json.RawMessage) ([]string, error) {
	raw, ok := members["attestations"]
	if !ok || isNull(raw) {
		return nil, nil
	}
	var elems []json.RawMessage
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] != '[' || json.Unmarshal(t, &elems) != nil {
		return nil, fail(CodeAttestationInvalid, ConstraintReferences, "the attestations member is not an array", nil)
	}
	if len(elems) > maxReferences {
		return nil, fail(CodeAttestationInvalid, ConstraintReferences, fmt.Sprintf("the record carries more than %d attestation references", maxReferences), nil)
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range elems {
		m, err := version.DecodeJSONObject(e)
		if err != nil {
			continue
		}
		var d string
		if raw, ok := m["digest"]; !ok || json.Unmarshal(raw, &d) != nil {
			continue
		}
		if _, ok := cacheName(d); ok && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}
