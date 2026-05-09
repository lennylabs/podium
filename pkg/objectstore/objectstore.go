// Package objectstore implements the §4.1 inline-cutoff path for
// large bundled resources. Resources whose payload exceeds
// InlineCutoff are stored externally and referenced from the
// `load_artifact` response by URL; smaller resources stay inline in
// the manifest record as today.
//
// Two backends ship in this package:
//
//   - Filesystem: stores under a configurable root, served via the
//     registry's authenticated /objects/{key} route. URL bears no
//     embedded signature or expiry; consumers send the same session
//     token they used for load_artifact, and the registry checks
//     visibility (§4.6) before serving the bytes.
//   - S3:         stores in an S3-compatible bucket, served via
//     AWS Signature V4 presigned URLs that expire after the
//     configured TTL.
//
// The Provider SPI is identical for both. Phase 2 ships the SPI plus
// these two implementations; future SPIs (Azure Blob, GCS-native
// signed URLs) plug in through the same interface.
package objectstore

import (
	"context"
	"errors"
	"time"
)

// Errors returned by Provider implementations. Tests assert via errors.Is.
var (
	// ErrNotFound is returned by Get when the key does not exist.
	ErrNotFound = errors.New("objectstore: not found")
	// ErrInvalidKey signals a malformed key (e.g., empty, contains "..").
	ErrInvalidKey = errors.New("objectstore: invalid key")
)

// InlineCutoff is the §4.1 size threshold. Resources up to this many
// bytes ship inline in the load_artifact response; resources above
// this go through the object store.
const InlineCutoff = 256 * 1024

// DefaultPresignTTL is the §6.2 default expiry for S3 presigned URLs.
// Operators override via PODIUM_PRESIGN_TTL_SECONDS.
const DefaultPresignTTL = 3600 * time.Second

// Provider is the SPI a backend satisfies. Implementations are safe
// for concurrent use; callers may share one instance across requests.
type Provider interface {
	// ID returns the backend identifier ("filesystem" | "s3" | ...).
	ID() string
	// Put stores body under key with the given content type. Calling
	// Put twice with the same key and identical body is a no-op (an
	// idempotent retry); calling with the same key and different body
	// is an error — keys are immutable per §4.7 invariants since the
	// canonical key is the content hash.
	Put(ctx context.Context, key string, body []byte, contentType string) error
	// Get fetches the body for key. Returns ErrNotFound when missing.
	Get(ctx context.Context, key string) ([]byte, error)
	// Presign returns a URL the consumer follows to fetch the body.
	// The URL's auth model is backend-specific (S3: signature in the
	// URL; Filesystem: bearer token on the request). ttl is the
	// requested expiry; backends that have no clock-bound TTL ignore
	// it and return a URL bound to caller credentials instead.
	Presign(ctx context.Context, key string, ttl time.Duration) (string, error)
	// Delete removes the object. Deleting a missing key is a no-op.
	// Used by GDPR erasure (§8.5) and by retention enforcement.
	Delete(ctx context.Context, key string) error
}
