package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/objectstore"
)

// manifestBodyFetchTimeout bounds the fetch of a presigned manifest document.
const manifestBodyFetchTimeout = 30 * time.Second

// manifestBodyLink is the §6.6 manifest_body_url reference on a load_artifact
// response.
type manifestBodyLink struct {
	URL         string `json:"presigned_url"`
	ContentHash string `json:"content_hash"`
}

// followManifestBody resolves a load_artifact response whose manifest document
// travelled by manifest_body_url back into its inline fields. It fetches the
// link, refuses a document whose sha256 differs from the link's content hash,
// restores skill_raw for a skill and frontmatter otherwise, and re-derives
// manifest_body with manifest.ParseSkill or manifest.ParseArtifact, the split
// ingest applies, so the printers see the body the inline path serves. A
// response with no link is returned unchanged.
//
// The caller's token is sent unless the URL is SigV4 presigned: the
// filesystem backend's /objects route authorizes the read against the caller,
// and S3 rejects a request that carries both kinds of authentication.
//
// Spec: §6.6, §7.6.1, §13.12.
func followManifestBody(ctx context.Context, body []byte, token string) ([]byte, error) {
	var head struct {
		Type string            `json:"type"`
		Link *manifestBodyLink `json:"manifest_body_url"`
	}
	if err := json.Unmarshal(body, &head); err != nil || head.Link == nil {
		return body, nil
	}
	doc, err := fetchManifestDocument(ctx, head.Link.URL, token)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(doc)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != head.Link.ContentHash {
		return nil, fmt.Errorf("content hash mismatch: got %s want %s", got, head.Link.ContentHash)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	if err := restoreManifestFields(fields, head.Type, doc); err != nil {
		return nil, err
	}
	delete(fields, "manifest_body_url")
	return json.Marshal(fields)
}

// restoreManifestFields writes the fetched document and the body it splits to
// into fields.
func restoreManifestFields(fields map[string]any, typ string, doc []byte) error {
	if typ == string(manifest.TypeSkill) {
		sk, err := manifest.ParseSkill(doc)
		if err != nil {
			return fmt.Errorf("parse SKILL.md: %w", err)
		}
		fields["skill_raw"], fields["manifest_body"] = string(doc), sk.Body
		return nil
	}
	a, err := manifest.ParseArtifact(doc)
	if err != nil {
		return fmt.Errorf("parse ARTIFACT.md: %w", err)
	}
	fields["frontmatter"], fields["manifest_body"] = string(doc), a.Body
	return nil
}

// fetchManifestDocument GETs a presigned manifest document under a deadline.
func fetchManifestDocument(ctx context.Context, rawURL, token string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, manifestBodyFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" && !objectstore.PresignedSigV4(rawURL) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
