// Package embedding implements the §4.7 EmbeddingProvider SPI plus
// the four built-in providers (`openai`, `voyage`, `cohere`,
// `ollama`). Providers translate text into a fixed-dimensional
// float vector that the vector store indexes for hybrid retrieval.
//
// Implementations are stateless HTTP clients over the provider's
// embeddings endpoint. None of them require an SDK; the wire format
// is JSON over `net/http`. Tests use `httptest.NewServer` to replay
// vendored response fixtures so the parsing path is exercised
// without external services.
package embedding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Errors returned by Provider implementations.
var (
	// ErrEmptyTexts is returned when Embed is called with no inputs.
	ErrEmptyTexts = errors.New("embedding: empty texts")
	// ErrUnreachable wraps a transient network or 5xx failure.
	// Callers retry with backoff or fall back to BM25-only.
	ErrUnreachable = errors.New("embedding: provider unreachable")
	// ErrAuth wraps a 401/403 from the provider.
	ErrAuth = errors.New("embedding: auth failed")
	// ErrQuota wraps a 429 / quota-exceeded response.
	ErrQuota = errors.New("embedding: quota exceeded")
)

// Provider is the SPI implementations satisfy.
type Provider interface {
	// ID returns the provider identifier ("openai" | "voyage" |
	// "cohere" | "ollama" | ...).
	ID() string
	// Model returns the configured model name (e.g. "voyage-3").
	Model() string
	// Dimensions returns the output vector size. Providers report
	// this from configuration or by hard-coded model-default tables;
	// callers use it to validate vector-store schema compatibility.
	Dimensions() int
	// Embed converts a batch of texts into a batch of float vectors.
	// The returned slice has the same length and order as texts.
	// Errors map to the package-level sentinels via errors.Is.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// httpClient is the package-internal default HTTP client. Tests
// override per-provider via the explicit Client field on each
// concrete struct.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// classify maps an HTTP status code to one of the package-level
// error sentinels so consumers can distinguish failure modes
// uniformly across providers.
func classify(status int, bodyHint string) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w: HTTP %d: %s", ErrAuth, status, bodyHint)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("%w: HTTP %d: %s", ErrQuota, status, bodyHint)
	case status >= 500:
		return fmt.Errorf("%w: HTTP %d: %s", ErrUnreachable, status, bodyHint)
	default:
		return fmt.Errorf("embedding: HTTP %d: %s", status, bodyHint)
	}
}
