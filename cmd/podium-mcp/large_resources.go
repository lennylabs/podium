package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lennylabs/podium/pkg/tracing"
)

// resourceRefresher re-requests /v1/load_artifact and returns a freshly
// presigned large_resources URL set. It is non-nil only on the live-fetch
// path; the cache and overlay paths cannot reach the registry and pass nil,
// in which case a 403 is retried against the same URL (riding through a
// transient blip) rather than refreshed.
type resourceRefresher func() (map[string]largeResourceLink, error)

// fetchLargeResources resolves every large_resource link in resp via an
// authenticated HTTP GET, validates each blob's content hash against the
// manifest, and merges the bytes into resp.Resources so the downstream
// adapter sees a single map. Per §6.6 step 1, a 403/expired response triggers
// a retry with a fresh URL set (max 3 attempts, exponential backoff).
func (s *mcpServer) fetchLargeResources(resp *loadArtifactResponse, refresh resourceRefresher) error {
	if len(resp.LargeResources) == 0 {
		return nil
	}
	// §13.8: child span for the object-storage fetch stage. It nests under the
	// active meta-tool root span (via reqCtx) as a sibling to the registry
	// round-trip, adapter.translate, and materialize spans, so an exported
	// load_artifact trace carries all four named child spans. Opened after the
	// no-op early return above so a manifest-only call records no empty span.
	_, span := tracing.Tracer().Start(s.reqCtx(), "objectstore.fetch")
	defer span.End()
	if resp.Resources == nil {
		resp.Resources = map[string]string{}
	}
	for path, link := range resp.LargeResources {
		body, err := s.fetchOneLargeResource(path, link, refresh)
		if err != nil {
			return fmt.Errorf("large resource %s: %w", path, err)
		}
		if link.ContentHash != "" {
			sum := sha256.Sum256(body)
			got := "sha256:" + hex.EncodeToString(sum[:])
			if got != link.ContentHash {
				return fmt.Errorf("large resource %s content hash mismatch: got %s want %s",
					path, got, link.ContentHash)
			}
		}
		resp.Resources[path] = string(body)
	}
	return nil
}

// fetchOneLargeResource downloads a single presigned URL with the §6.6 retry
// contract: up to 3 attempts, exponential backoff on 403 / network failure /
// 5xx. On a 403 (a genuinely expired presigned URL, or a filesystem-backend
// auth rejection) it re-requests a fresh URL set via refresh, when available,
// and swaps in the freshly presigned link for this resource before the next
// attempt, so an expired URL is replaced rather than retried unchanged.
func (s *mcpServer) fetchOneLargeResource(path string, link largeResourceLink, refresh resourceRefresher) ([]byte, error) {
	const maxAttempts = 3
	backoff := 250 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		body, status, err := s.getLargeResource(link.URL)
		if err == nil && status == http.StatusOK {
			return body, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("HTTP %d", status)
			// A 4xx other than 403 (e.g. 404) will not recover via retry
			// or a fresh URL.
			if status != http.StatusForbidden && status < 500 {
				return nil, lastErr
			}
			// §6.6 step 1: on 403/expired, request a fresh URL set and
			// swap in the new link for this resource before retrying.
			if status == http.StatusForbidden && refresh != nil {
				if fresh, rerr := refresh(); rerr == nil {
					if nl, ok := fresh[path]; ok && nl.URL != "" {
						link = nl
					}
				}
			}
		}
		if attempt+1 < maxAttempts {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return nil, fmt.Errorf("after %d attempts: %v", maxAttempts, lastErr)
}

// getLargeResource issues one authenticated GET against a presigned large
// resource URL and returns the body and HTTP status. Per §13.11 the
// filesystem backend's /objects/{content_hash} route has no embedded
// signature: the consumer sends the same session token and tenant header it
// used for load_artifact, which the registry validates before serving. The
// S3 backend's URL carries its own Signature V4 and ignores the extra header,
// so attaching the credential is safe on both backends (§6.6 step 1).
func (s *mcpServer) getLargeResource(rawURL string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	tok, err := s.bearerToken()
	if err != nil {
		return nil, 0, err
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if s.cfg.tenantID != "" {
		req.Header.Set("X-Podium-Tenant", s.cfg.tenantID)
	}
	httpResp, err := s.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return nil, httpResp.StatusCode, nil
	}
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, httpResp.StatusCode, err
	}
	return body, httpResp.StatusCode, nil
}
