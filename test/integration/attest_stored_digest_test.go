package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// corpusRegistry ingests the attestation corpus into a memory store and
// serves it over HTTP from a public layer.
func corpusRegistry(t *testing.T) (*store.Memory, *httptest.Server) {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(ctx, st, ingest.Request{TenantID: "t", LayerID: "corpus", Files: os.DirFS(corpusDir(t))}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	reg := core.New(st, "t", []layer.Layer{{ID: "corpus", Precedence: 1, Visibility: layer.Visibility{Public: true}}})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)
	return st, ts
}

// fetchJSON issues method against url with body and decodes the JSON
// response into a generic value, failing on a status other than 200.
func fetchJSON(t *testing.T, method, url string, body []byte) any {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s %s = %d: %s", method, url, resp.StatusCode, raw)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

// Spec: §7.2, §4.7.6, §13.4 — ingest stores the package digest the walks
// compute for every package of the corpus it stores, and a load_artifact
// response serves it as package_digest, equal to the pinned corpus digest.
// The member is no part of a batch entry (§7.6.2).
func TestAttestStoredDigest_LoadServesTheStoredDigest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, ts := corpusRegistry(t)
	served := 0
	for _, c := range corpusPackages {
		if !c.resolved || c.lintFails {
			continue
		}
		rows, err := st.ListManifests(ctx, "t")
		if err != nil {
			t.Fatalf("ListManifests: %v", err)
		}
		var stored string
		for _, r := range rows {
			if r.ArtifactID == c.id {
				stored = r.PackageDigest
			}
		}
		body, _ := fetchJSON(t, http.MethodGet, ts.URL+"/v1/load_artifact?id="+c.id, nil).(map[string]any)
		if stored != c.digest || body["package_digest"] != c.digest {
			t.Errorf("%s: stored %q, served %v, want %q", c.id, stored, body["package_digest"], c.digest)
		}
		served++
	}
	if served == 0 {
		t.Fatalf("the corpus served no package")
	}

	req, _ := json.Marshal(map[string]any{"ids": []string{"forms/anchor"}})
	entries, _ := fetchJSON(t, http.MethodPost, ts.URL+"/v1/artifacts:batchLoad", req).([]any)
	entry, _ := entries[0].(map[string]any)
	if _, carried := entry["package_digest"]; carried || entry["status"] != "ok" {
		t.Errorf("batch entry = %v, want an ok entry without package_digest", entry)
	}
}

// Spec: §7.2, §13.4 — a version stored without a package digest is served
// without the member rather than with an empty string.
func TestAttestStoredDigest_RowWithoutDigestOmitsTheMember(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, ts := corpusRegistry(t)
	row, err := st.GetManifest(ctx, "t", "forms/anchor", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if err := st.SetPackageDigest(ctx, "t", row.ArtifactID, row.Version, row.ContentHash, ""); err != nil {
		t.Fatalf("SetPackageDigest: %v", err)
	}
	body, _ := fetchJSON(t, http.MethodGet, ts.URL+"/v1/load_artifact?id=forms/anchor", nil).(map[string]any)
	if _, carried := body["package_digest"]; carried {
		t.Errorf("load of a row without a digest carries package_digest %v", body["package_digest"])
	}
}
