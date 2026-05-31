package sync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/manifest"
)

// defaultServerTimeout bounds every server-source HTTP request so a sync
// against an unresponsive registry fails rather than hanging.
const defaultServerTimeout = 30 * time.Second

// syncManifestResponse is the GET /v1/sync/manifest body: the caller's
// effective view as a flat artifact list (§7.5 server-source enumeration).
type syncManifestResponse struct {
	Artifacts []struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Version string `json:"version"`
		Layer   string `json:"layer"`
	} `json:"artifacts"`
}

// serverLoadResponse mirrors the subset of GET /v1/load_artifact that
// podium sync materializes.
type serverLoadResponse struct {
	ID             string            `json:"id"`
	Type           string            `json:"type"`
	Layer          string            `json:"layer"`
	ManifestBody   string            `json:"manifest_body"`
	Frontmatter    string            `json:"frontmatter"`
	SkillRaw       string            `json:"skill_raw"`
	Resources      map[string]string `json:"resources"`
	ResourcesB64   bool              `json:"resources_base64"`
	LargeResources map[string]struct {
		URL string `json:"presigned_url"`
	} `json:"large_resources"`
}

// errorEnvelope is the §6.10 structured error a registry returns on a
// non-2xx response.
type errorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// fetchServerRecords reads the caller's effective view from a server-source
// registry (§7.5) and returns adapter-ready records. It mirrors the MCP
// server's server-source delivery (§2.2): the served frontmatter becomes
// ARTIFACT.md, a skill's body is appended for SKILL.md, and bundled
// resources are decoded inline or fetched from their §7.2 presigned URLs.
func fetchServerRecords(ctx context.Context, opts Options) ([]materialRecord, error) {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultServerTimeout}
	}
	base := strings.TrimRight(opts.RegistryPath, "/")

	var list syncManifestResponse
	if err := httpGetJSON(ctx, client, base+"/v1/sync/manifest", &list); err != nil {
		return nil, fmt.Errorf("sync manifest: %w", err)
	}

	out := make([]materialRecord, 0, len(list.Artifacts))
	for _, entry := range list.Artifacts {
		rec, err := fetchServerRecord(ctx, client, base, entry.ID, entry.Layer)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// fetchServerRecord loads one artifact over HTTP and assembles its record.
func fetchServerRecord(ctx context.Context, client *http.Client, base, id, layerID string) (materialRecord, error) {
	var resp serverLoadResponse
	loadURL := base + "/v1/load_artifact?id=" + url.QueryEscape(id)
	if err := httpGetJSON(ctx, client, loadURL, &resp); err != nil {
		return materialRecord{}, fmt.Errorf("load_artifact %s: %w", id, err)
	}
	if resp.Layer != "" {
		layerID = resp.Layer
	}

	resources, err := decodeInlineResources(resp.Resources, resp.ResourcesB64)
	if err != nil {
		return materialRecord{}, fmt.Errorf("load_artifact %s: %w", id, err)
	}
	// §7.2 large resources travel as presigned URLs; fetch each so the
	// materialized package is complete on disk.
	for path, link := range resp.LargeResources {
		if link.URL == "" {
			return materialRecord{}, fmt.Errorf("load_artifact %s: large resource %q missing presigned URL", id, path)
		}
		body, ferr := fetchBytes(ctx, client, link.URL)
		if ferr != nil {
			return materialRecord{}, fmt.Errorf("load_artifact %s: fetch resource %q: %w", id, path, ferr)
		}
		resources[path] = body
	}

	rec := materialRecord{
		ID:            id,
		LayerID:       layerID,
		ArtifactBytes: []byte(resp.Frontmatter),
		Resources:     resources,
	}
	// Parse the served frontmatter so the §4.3 target_harnesses gate runs.
	// A parse failure leaves Artifact nil; the artifact then materializes
	// for every harness (the gate only excludes opt-outs).
	if a, perr := manifest.ParseArtifact([]byte(resp.Frontmatter)); perr == nil {
		rec.Artifact = a
	}
	// spec: §4.3.4 / §11 — a skill's SKILL.md is delivered verbatim so the
	// materialized file is byte-identical to the filesystem-source consumer.
	// The authored SKILL.md frontmatter (name, description, compatibility,
	// allowed-tools, …) cannot be reconstructed from ARTIFACT.md frontmatter
	// plus body, so the registry ships the original bytes in skill_raw.
	if resp.Type == "skill" {
		rec.SkillBytes = []byte(resp.SkillRaw)
	}
	return rec, nil
}

// decodeInlineResources copies the inline resource map to bytes, decoding
// base64 when the registry flagged resources_base64 (F-6.6.8). Sorted-key
// iteration keeps the result deterministic.
func decodeInlineResources(in map[string]string, b64 bool) (map[string][]byte, error) {
	out := map[string][]byte{}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !b64 {
			out[k] = []byte(in[k])
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(in[k])
		if err != nil {
			return nil, fmt.Errorf("decode resource %q: %w", k, err)
		}
		out[k] = raw
	}
	return out, nil
}

// httpGetJSON issues a bounded GET and decodes a JSON body, mapping a
// non-2xx response to the registry's §6.10 error envelope.
func httpGetJSON(ctx context.Context, client *http.Client, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var env errorEnvelope
		if json.Unmarshal(body, &env) == nil && env.Code != "" {
			return fmt.Errorf("%s: %s", env.Code, env.Message)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

// fetchBytes downloads a presigned large-resource URL with a bounded read.
func fetchBytes(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}
