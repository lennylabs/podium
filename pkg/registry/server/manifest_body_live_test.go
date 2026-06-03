package server_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// liveS3Store reads PODIUM_S3_* and returns a real S3-backed object store, or
// skips when the environment is unconfigured or unreachable. Mirrors the
// resolution in objectstore's own live smoke (kept local to avoid exporting a
// cross-package test helper).
func liveS3Store(t *testing.T) *objectstore.S3 {
	t.Helper()
	endpoint := os.Getenv("PODIUM_S3_ENDPOINT")
	bucket := os.Getenv("PODIUM_S3_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("PODIUM_S3_ENDPOINT/BUCKET unset; skipping live object-store manifest-body smoke")
	}
	host, useSSL := objectstore.ParseS3Endpoint(endpoint)
	region := os.Getenv("PODIUM_S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	s3, err := objectstore.NewS3(objectstore.S3Config{
		Endpoint:        host,
		Bucket:          bucket,
		Region:          region,
		AccessKeyID:     os.Getenv("PODIUM_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("PODIUM_S3_SECRET_ACCESS_KEY"),
		UseSSL:          useSSL,
	})
	if err != nil {
		t.Skipf("NewS3: %v", err)
	}
	return s3
}

// Spec: §6.6/§7.2 — the presigned manifest-body channel over a real
// S3-compatible object store (MinIO). load_artifact uploads an above-cutoff
// ARTIFACT.md to the data plane and returns an AWS Signature V4 presigned URL;
// the consumer fetches the body directly from object storage without sending
// credentials, exactly as it does for a large bundled resource.
func TestManifestBody_LivePresignedOverS3(t *testing.T) {
	t.Parallel()
	s3 := liveS3Store(t)

	body := strings.Repeat("glossary line\n", objectstore.InlineCutoff/14+200)
	doc := []byte("---\ntype: context\nversion: 1.0.0\ndescription: Big glossary.\n---\n\n" + body)
	sum := sha256.Sum256(doc)
	key := hex.EncodeToString(sum[:])
	t.Cleanup(func() { _ = s3.Delete(t.Context(), key) })

	st := store.NewMemory()
	if err := st.CreateTenant(t.Context(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(t.Context(), store.ManifestRecord{
		TenantID: "default", ArtifactID: "finance/glossary", Version: "1.0.0",
		ContentHash: "sha256:c", Type: "context", Layer: "L",
		Frontmatter: doc, Body: []byte(body),
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	// S3 presigns its own URLs, so the baseURL is unused.
	srv := server.New(reg, server.WithObjectStore(s3, "", time.Hour))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	parsed := mbGetLoadArtifact(t, ts, "finance/glossary")
	if parsed.ManifestBodyURL == nil {
		t.Fatalf("an above-cutoff body must presign against S3: %+v", parsed)
	}
	if parsed.Frontmatter != "" || parsed.ManifestBody != "" {
		t.Errorf("inline body fields must be cleared when presigned")
	}
	if parsed.ManifestBodyURL.ContentHash != "sha256:"+key {
		t.Errorf("content_hash = %q, want %q", parsed.ManifestBodyURL.ContentHash, "sha256:"+key)
	}
	if !strings.Contains(parsed.ManifestBodyURL.URL, "X-Amz-Signature=") {
		t.Errorf("S3 presigned URL missing AWS signature: %s", parsed.ManifestBodyURL.URL)
	}

	// Fetch the body straight from object storage, credential-free.
	resp, err := http.Get(parsed.ManifestBodyURL.URL)
	if err != nil {
		t.Fatalf("GET presigned body: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned body fetch = HTTP %d", resp.StatusCode)
	}
	if string(got) != string(doc) {
		t.Errorf("presigned body served %d bytes, want %d", len(got), len(doc))
	}
}
