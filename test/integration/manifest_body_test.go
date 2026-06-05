package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §6.6/§7.2 — the presigned manifest-body channel over the SQLite
// metadata store and a filesystem object store. A context artifact whose
// ARTIFACT.md exceeds the inline cutoff is persisted with its full
// frontmatter, and load_artifact lazily uploads that document to the object
// store and returns a presigned manifest_body_url instead of the inline body.
// The URL serves the verbatim ARTIFACT.md from the /objects data plane.
//
// The record is written directly because the §4.1 manifest token cap
// (lint.manifest_size) rejects an above-cutoff manifest at ingest, so the
// channel is exercised by seeding the store as a forward-compatible registry
// would.
func TestManifestBody_PresignedRoundTripSQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	body := strings.Repeat("glossary line\n", objectstore.InlineCutoff/14+200)
	doc := []byte("---\ntype: context\nversion: 1.0.0\ndescription: Big glossary.\n---\n\n" + body)
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID: "default", ArtifactID: "finance/glossary", Version: "1.0.0",
		ContentHash: "sha256:c", Type: "context", Layer: "L",
		Frontmatter: doc, Body: []byte(body),
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	objStore, err := objectstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("objectstore.Open: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	srv := server.New(reg, server.WithObjectStore(objStore, "placeholder", time.Hour))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	objStore.BaseURL = ts.URL

	sum := sha256.Sum256(doc)
	key := hex.EncodeToString(sum[:])

	// The object does not exist before the first load; the serve path uploads
	// it lazily.
	if _, serr := objStore.Stat(ctx, key); serr == nil {
		t.Fatalf("manifest-body object should not exist before the first load")
	}

	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=finance/glossary")
	if err != nil {
		t.Fatalf("GET load_artifact: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var parsed server.LoadArtifactResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if parsed.ManifestBodyURL == nil {
		t.Fatalf("an above-cutoff body must presign after the SQLite round-trip: %+v", parsed)
	}
	if parsed.Frontmatter != "" || parsed.ManifestBody != "" {
		t.Errorf("inline body fields must be cleared when presigned")
	}
	if parsed.ManifestBodyURL.ContentHash != "sha256:"+key {
		t.Errorf("content_hash = %q, want %q", parsed.ManifestBodyURL.ContentHash, "sha256:"+key)
	}

	// The serve path uploaded the document; the presigned URL streams it back.
	if _, serr := objStore.Stat(ctx, key); serr != nil {
		t.Errorf("manifest-body object should exist after the load: %v", serr)
	}
	objResp, err := http.Get(parsed.ManifestBodyURL.URL)
	if err != nil {
		t.Fatalf("GET presigned manifest body: %v", err)
	}
	objBody, _ := io.ReadAll(objResp.Body)
	objResp.Body.Close()
	if objResp.StatusCode != http.StatusOK {
		t.Fatalf("presigned manifest-body fetch = HTTP %d", objResp.StatusCode)
	}
	if string(objBody) != string(doc) {
		t.Errorf("presigned manifest body served %d bytes, want %d", len(objBody), len(doc))
	}
}
