package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// defaultServerTimeout bounds every server-source HTTP request so a sync
// against an unresponsive registry fails rather than hanging.
const defaultServerTimeout = 30 * time.Second

// serverUnreachableError wraps a transport-level failure reaching a
// server-source registry (dial, DNS, timeout, connection reset), distinct from
// a structured >=400 response which means the registry answered and refused.
// It drives the §7.4 offline-first degraded-network behavior in Run: an
// unreachable server in offline-first mode is tolerated, while a structured
// rejection still fails the sync.
type serverUnreachableError struct{ err error }

func (e *serverUnreachableError) Error() string { return e.err.Error() }
func (e *serverUnreachableError) Unwrap() error { return e.err }

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
// Every record passes check before it is returned.
func fetchServerRecords(ctx context.Context, opts Options, check *sign.DeliveryCheck) ([]materialRecord, error) {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultServerTimeout}
	}
	base := strings.TrimRight(opts.RegistryPath, "/")

	var list syncManifestResponse
	if err := httpGetJSON(ctx, client, base+"/v1/sync/manifest", opts.Token, &list); err != nil {
		return nil, fmt.Errorf("sync manifest: %w", err)
	}

	f := serverFetcher{client: client, base: base, token: opts.Token, check: check}
	out := make([]materialRecord, 0, len(list.Artifacts))
	for _, entry := range list.Artifacts {
		rec, err := f.fetchServerRecord(ctx, entry.ID, entry.Layer)
		if err != nil {
			return nil, fmt.Errorf("load_artifact %s: %w", entry.ID, err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// serverFetcher holds what every load_artifact request of one server-source
// load shares: the client, the registry base URL, the caller credential, and
// the resolved delivery check.
type serverFetcher struct {
	client *http.Client
	base   string
	token  string
	check  *sign.DeliveryCheck
}

// fetchServerRecord loads one artifact over HTTP, verifies it by the §4.7.10
// procedure, and assembles its record. The body is read only through
// version.ParseLoadResponse and its exact-name member maps, so sync refuses
// exactly the bodies the MCP server and the SDKs refuse.
//
// Spec: §4.7.10, §6.6 step 2, §7.5
func (f serverFetcher) fetchServerRecord(ctx context.Context, id, layerID string) (materialRecord, error) {
	body, err := httpGetBody(ctx, f.client, f.base+"/v1/load_artifact?id="+url.QueryEscape(id), f.token)
	if err != nil {
		return materialRecord{}, err
	}
	served, err := version.ParseLoadResponse(body)
	if err != nil {
		return materialRecord{}, fmt.Errorf("materialize.content_hash_mismatch: %w", err)
	}
	if layer, err := servedLayer(served.Members); err != nil {
		return materialRecord{}, err
	} else if layer != "" {
		layerID = layer
	}
	if err := f.restoreManifestDocument(ctx, &served); err != nil {
		return materialRecord{}, err
	}
	resources, err := f.fetchLargeResources(ctx, served)
	if err != nil {
		return materialRecord{}, err
	}
	if err := f.check.Verify(ctx, served.Record, served.Hash, served.Signature); err != nil {
		return materialRecord{}, err
	}
	return recordFromServed(served.Record, id, layerID, resources), nil
}

// servedLayer reads the layer member by exact name. An absent member or a
// null reads as empty, and a value of another JSON type is refused. layer sits
// outside the delivery record, so a case variant or an earlier repeated
// occurrence of the name is never read (§4.7.10 steps 1 and 2).
func servedLayer(members map[string]json.RawMessage) (string, error) {
	raw, ok := members["layer"]
	if !ok {
		return "", nil
	}
	var layer string
	if err := json.Unmarshal(raw, &layer); err != nil {
		return "", fmt.Errorf("materialize.content_hash_mismatch: layer: %w", err)
	}
	return layer, nil
}

// restoreManifestDocument follows a manifest_body_url, checks the fetched
// document against the link's content hash (§4.7.10 step 6), and completes
// the record with the document and the body manifest.ManifestBodyOf derives
// from it. A no-op when the document arrived inline.
//
// Spec: §4.7.10, §6.6, §13.12
func (f serverFetcher) restoreManifestDocument(ctx context.Context, served *version.Served) error {
	link := served.ManifestLink
	if link == nil {
		return nil
	}
	doc, err := fetchBytes(ctx, f.client, link.URL, f.token)
	if err != nil {
		return fmt.Errorf("fetch manifest body: %w", err)
	}
	if err := version.CheckLinked(doc, link.ContentHash); err != nil {
		return fmt.Errorf("materialize.content_hash_mismatch: manifest body: %w", err)
	}
	body, err := manifest.ManifestBodyOf(doc)
	if err != nil {
		// ManifestBodyOf returns no error today; the branch keeps a future
		// failure from framing an empty body, and no test can reach it.
		return fmt.Errorf("materialize.content_hash_mismatch: manifest body: %w", err)
	}
	served.PlaceManifestDocument(doc, body)
	return nil
}

// fetchLargeResources returns the inline bodies together with each §7.2
// large resource fetched from its presigned URL and checked against the
// link's content hash (§4.7.10 step 6), so the materialized package is
// complete on disk. Paths are fetched in sorted order.
func (f serverFetcher) fetchLargeResources(ctx context.Context, served version.Served) (map[string][]byte, error) {
	resources := make(map[string][]byte, len(served.Inline)+len(served.Links))
	for path, body := range served.Inline {
		resources[path] = body
	}
	paths := make([]string, 0, len(served.Links))
	for path := range served.Links {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		link := served.Links[path]
		body, err := fetchBytes(ctx, f.client, link.URL, f.token)
		if err != nil {
			return nil, fmt.Errorf("fetch resource %q: %w", path, err)
		}
		if err := version.CheckLinked(body, link.ContentHash); err != nil {
			return nil, fmt.Errorf("materialize.content_hash_mismatch: large resource %s: %w", path, err)
		}
		resources[path] = body
	}
	return resources, nil
}

// recordFromServed builds the adapter-ready record of a verified delivery
// record.
func recordFromServed(rec version.DeliveryRecord, id, layerID string, resources map[string][]byte) materialRecord {
	out := materialRecord{
		ID:      id,
		LayerID: layerID,
		// ContentHash is the registry's authoritative §6.6 content hash for
		// the resolved (id, version) pair. The lock pins it verbatim so the
		// committed (id, version, content_hash) triple is the registry's
		// system-of-record value rather than a digest recomputed from the
		// served bytes (§14.11).
		ContentHash:   rec.ContentHash,
		ArtifactBytes: []byte(rec.Frontmatter),
		AuthoredBytes: []byte(rec.Frontmatter),
		Resources:     resources,
	}
	// Parse the served frontmatter so the §4.3 target_harnesses gate runs.
	// A parse failure leaves Artifact nil; the artifact then materializes
	// for every harness (the gate only excludes opt-outs).
	if a, perr := manifest.ParseArtifact([]byte(rec.Frontmatter)); perr == nil {
		out.Artifact = a
	}
	// spec: §4.3.4 / §11 — a skill's SKILL.md is delivered verbatim so the
	// materialized file is byte-identical to the filesystem-source consumer.
	// The authored SKILL.md frontmatter (name, description, compatibility,
	// allowed-tools, …) cannot be reconstructed from ARTIFACT.md frontmatter
	// plus body, so the registry ships the original bytes in skill_raw.
	if rec.Type == string(manifest.TypeSkill) {
		out.SkillBytes = []byte(rec.SkillRaw)
	}
	return out
}

// httpGetJSON issues a bounded GET through httpGetBody and decodes the JSON
// body into out.
func httpGetJSON(ctx context.Context, client *http.Client, rawURL, token string, out any) error {
	body, err := httpGetBody(ctx, client, rawURL, token)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// httpGetBody issues a bounded GET and returns the body bytes, mapping a
// non-2xx response to the registry's §6.10 error envelope. When token is
// non-empty it is attached as Authorization: Bearer so the registry resolves
// the caller's identity (§6.3.2 / §14.11).
func httpGetBody(ctx context.Context, client *http.Client, rawURL, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		// A transport-level failure (the registry could not be reached) is the
		// §7.4 degraded-network condition; tag it so Run can apply the cache
		// mode. A non-2xx status below is a structured rejection, not this.
		return nil, &serverUnreachableError{err}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var env errorEnvelope
		if json.Unmarshal(body, &env) == nil && env.Code != "" {
			return nil, fmt.Errorf("%s: %s", env.Code, env.Message)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// fetchBytes downloads a large-resource URL with a bounded read. The §13.11
// authentication mechanism differs by backend, so the caller credential is
// attached selectively:
//
//   - Filesystem backend: the URL is the registry's token-bound
//     /objects/{content_hash} route, which has no embedded signature. The token
//     is attached as Authorization: Bearer so the registry validates it and
//     confirms visibility before serving (the consumer sends the same session
//     token it used for load_artifact).
//   - S3 backend: the URL is presigned with AWS Signature V4 and is
//     self-validating; "consumers do not send credentials when following the
//     URL." An Authorization header alongside the SigV4 query makes S3 reject
//     the request as "multiple authentication types" (HTTP 400), so the token
//     MUST be withheld. objectstore.PresignedSigV4 detects this case by the
//     SigV4 query.
func fetchBytes(ctx context.Context, client *http.Client, rawURL, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" && !objectstore.PresignedSigV4(rawURL) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &serverUnreachableError{err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}
