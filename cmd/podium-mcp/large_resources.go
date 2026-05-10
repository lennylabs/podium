package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
)

// fetchLargeResources resolves every large_resource link in
// resp via HTTP GET, validates each blob's content hash against
// the manifest, and merges the bytes into resp.Resources so the
// downstream adapter sees a single map. Per §6.6 step 1, a 403
// response triggers a retry with a fresh URL set (max 3
// attempts, exponential backoff).
//
// In this MCP build there's no separate "fresh URL" call out to
// the registry — the registry's response carries one URL per
// resource, valid for the configured presign TTL. We retry the
// same URL with exponential backoff up to 3 attempts to ride
// through transient 403/expired blips; production deployments
// wire a refresher that re-fetches via /v1/load_artifact when
// the cluster's clocks drift past the TTL.
func (s *mcpServer) fetchLargeResources(resp *loadArtifactResponse) error {
	if len(resp.LargeResources) == 0 {
		return nil
	}
	if resp.Resources == nil {
		resp.Resources = map[string]string{}
	}
	for path, link := range resp.LargeResources {
		body, err := s.fetchOneLargeResource(link)
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

// fetchOneLargeResource downloads a single presigned URL with
// the §6.6 retry contract: up to 3 attempts, exponential backoff
// on 403 / network failure / 5xx.
func (s *mcpServer) fetchOneLargeResource(link largeResourceLink) ([]byte, error) {
	const maxAttempts = 3
	backoff := 250 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		resp, err := http.Get(link.URL)
		if err != nil {
			lastErr = err
		} else if resp.StatusCode == http.StatusOK {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return body, nil
		} else {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			// 4xx other than 403 (e.g. 404) won't recover via retry.
			if resp.StatusCode != http.StatusForbidden && resp.StatusCode < 500 {
				return nil, lastErr
			}
		}
		if attempt+1 < maxAttempts {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return nil, fmt.Errorf("after %d attempts: %v", maxAttempts, lastErr)
}
