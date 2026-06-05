package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// EndpointSink forwards audit events to an external HTTP(S) endpoint as
// JSON. It backs the §9 LocalAuditSink "external endpoint" redirect for
// PODIUM_AUDIT_SINK and the §8.3 statement that the local sink "can be
// redirected to external SIEM / log aggregation". spec: §6.2, §8.3, §9.
//
// Each event is hash-chained in-process per §8.6 before it is POSTed, so a
// receiver sees the same tamper-evident hash / prev_hash fields the file
// sink writes. Durable storage and integrity are the receiving
// aggregator's responsibility; Verify is a no-op because there is no local
// log to walk.
type EndpointSink struct {
	mu       sync.Mutex
	url      string
	client   *http.Client
	lastHash string
}

// endpointTimeout bounds a single forward POST so a slow or unreachable
// aggregator cannot stall a meta-tool call.
const endpointTimeout = 10 * time.Second

// NewEndpointSink returns a sink that POSTs each event to rawURL. The URL
// must use the http or https scheme; any other value is rejected so a
// filesystem path is never mistaken for an endpoint.
func NewEndpointSink(rawURL string) (*EndpointSink, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("audit: parse endpoint %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("audit: endpoint sink requires an http or https URL, got %q", rawURL)
	}
	return &EndpointSink{url: rawURL, client: &http.Client{Timeout: endpointTimeout}}, nil
}

// Append chains the event in-process and forwards it to the endpoint. A
// transport error or non-2xx response is returned to the caller; the MCP
// server swallows it so a forwarding failure never breaks a tool call.
func (s *EndpointSink) Append(ctx context.Context, e Event) error {
	s.mu.Lock()
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	e.PrevHash = s.lastHash
	hash := sha256.Sum256(append(e.canonicalBody(), []byte(s.lastHash)...))
	e.Hash = hex.EncodeToString(hash[:])
	s.lastHash = e.Hash
	payload := eventForJSON(e)
	s.mu.Unlock()

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("audit: endpoint sink POST %s returned status %d", s.url, resp.StatusCode)
	}
	return nil
}

// Verify is a no-op for a forwarding sink: integrity of the forwarded
// stream is owned by the receiving aggregator.
func (s *EndpointSink) Verify(context.Context) error { return nil }

// URL returns the configured endpoint. Used by tests and operator output.
func (s *EndpointSink) URL() string { return s.url }
