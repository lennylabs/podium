package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/tracing"
)

// manifestBodyRefreshKey is the synthetic resource path under which the
// manifest-body channel reuses fetchOneLargeResource's 403-refresh retry
// loop. A real bundled-resource path is slash-separated and non-empty, so
// this NUL-prefixed sentinel cannot collide with one.
const manifestBodyRefreshKey = "\x00podium-manifest-body"

// fetchManifestBody resolves a presigned manifest_body_url (§6.6 step 1)
// back into the inline manifest fields. It downloads the canonical manifest
// document with the same retry/refresh contract as a large resource,
// verifies its content hash, restores the canonical-document field
// (Frontmatter, or SkillRaw for a skill), and re-derives ManifestBody from
// it. The frontmatter/body split reproduces ingest's own split, so the
// derived body is byte-identical to the inline value the registry would have
// served below the cutoff. A no-op when the body arrived inline
// (ManifestBodyURL nil).
func (s *mcpServer) fetchManifestBody(resp *loadArtifactResponse, refresh resourceRefresher) error {
	if resp.ManifestBodyURL == nil {
		return nil
	}
	_, span := tracing.Tracer().Start(s.reqCtx(), "objectstore.fetch")
	defer span.End()
	link := *resp.ManifestBodyURL
	body, err := s.fetchOneLargeResource(manifestBodyRefreshKey, link, refresh)
	if err != nil {
		return fmt.Errorf("manifest body: %w", err)
	}
	if link.ContentHash != "" {
		sum := sha256.Sum256(body)
		got := "sha256:" + hex.EncodeToString(sum[:])
		if got != link.ContentHash {
			return fmt.Errorf("manifest body content hash mismatch: got %s want %s", got, link.ContentHash)
		}
	}
	if resp.Type == "skill" {
		resp.SkillRaw = string(body)
		if sk, perr := manifest.ParseSkill(body); perr == nil {
			resp.ManifestBody = sk.Body
		}
	} else {
		resp.Frontmatter = string(body)
		if art, perr := manifest.ParseArtifact(body); perr == nil {
			resp.ManifestBody = art.Body
		}
	}
	resp.ManifestBodyURL = nil
	return nil
}

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

// getLargeResource issues one GET against a large-resource URL and returns the
// body and HTTP status. The §13.11 authentication mechanism differs by backend:
//
//   - Filesystem backend: the URL points at the registry's token-bound
//     /objects/{content_hash} route, which carries no embedded signature. The
//     consumer sends the same session token and tenant header it used for
//     load_artifact, which the registry validates before serving.
//   - S3 backend: the URL is presigned with AWS Signature V4 and is
//     self-validating. The spec is explicit that "consumers do not send
//     credentials when following the URL." Sending an Authorization header
//     alongside the SigV4 query makes S3 reject the request as "multiple
//     authentication types" (HTTP 400), so the credential MUST be withheld.
//
// presignedSigV4 distinguishes the two by the SigV4 query parameters only the
// S3 URL carries, so the credential is attached to the registry /objects route
// and withheld from a presigned S3 URL.
func (s *mcpServer) getLargeResource(rawURL string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	if !presignedSigV4(rawURL) {
		tok, terr := s.bearerToken()
		if terr != nil {
			return nil, 0, terr
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		if s.cfg.tenantID != "" {
			req.Header.Set("X-Podium-Tenant", s.cfg.tenantID)
		}
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

// presignedSigV4 reports whether rawURL is an AWS Signature V4 presigned URL,
// identified by the X-Amz-Signature query parameter the S3 backend appends
// (X-Amz-Algorithm/X-Amz-Credential accompany it). The filesystem backend's
// /objects/{content_hash} route carries none of these, so this cleanly
// separates a self-validating S3 URL (no caller credential) from the
// token-bound registry route (§13.11). A URL that fails to parse is treated as
// not presigned so the caller credential is attached, which is the safe default
// for the registry route.
func presignedSigV4(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return u.Query().Get("X-Amz-Signature") != ""
}
