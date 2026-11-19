package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// newDeprecationServer stores team/a at 1.0.0 and, ingested later, a
// deprecated 2.0.0 that names team/b as its upgrade target. It returns the
// server and the strong entity tag of 1.0.0.
func newDeprecationServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	live := putVersion(t, st, "team/a", "1.0.0", now.Add(-time.Hour))
	rec := storetest.Seal(t, store.ManifestRecord{
		TenantID: "default", ArtifactID: "team/a", Version: "2.0.0",
		Type: "skill", Layer: "L", IngestedAt: now, Deprecated: true, ReplacedBy: "team/b",
	}, nil, nil)
	if err := st.PutManifest(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	reg := core.New(st, "default", []layer.Layer{{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}}})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)
	return ts, `"` + live + `"`
}

func doLoad(t *testing.T, method, url, ifNoneMatch string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp, body
}

// Spec: §7.2 (Deprecation members), §4.7.4 — a latest load whose newest
// version is deprecated returns deprecation_warning and replaced_by beside the
// served 1.0.0; a matching If-None-Match is answered with the full response
// rather than 304; and a HEAD carries the warning in
// X-Podium-Deprecation-Warning.
func TestLoadArtifact_DeprecationMembersAndRevalidation(t *testing.T) {
	t.Parallel()
	ts, etag := newDeprecationServer(t)
	url := ts.URL + "/v1/load_artifact?id=team/a"
	const want = "version 2.0.0 of team/a is deprecated; replaced_by team/b; this load serves version 1.0.0"

	resp, body := doLoad(t, http.MethodGet, url, etag)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET with a matching If-None-Match: status %d, want the full 200 response", resp.StatusCode)
	}
	if body["version"] != "1.0.0" || body["deprecation_warning"] != want || body["replaced_by"] != "team/b" || body["deprecated"] != nil {
		t.Errorf("body members: version=%v deprecated=%v replaced_by=%v warning=%v", body["version"], body["deprecated"], body["replaced_by"], body["deprecation_warning"])
	}

	head, _ := doLoad(t, http.MethodHead, url, "")
	if got := head.Header.Get(server.DeprecationWarningHeader); got != want {
		t.Errorf("HEAD %s = %q, want %q", server.DeprecationWarningHeader, got, want)
	}
}

// Spec: §7.2 (Deprecation members), §4.7.4, §13.4 — on a SQLite store, which
// keeps no replaced_by column, a HEAD revalidation of a load of a deprecated
// version carries the same warning text, upgrade target included, as the GET
// of the same load.
func TestLoadArtifact_HeadWarningMatchesGetOnSQLite(t *testing.T) {
	t.Parallel()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "podium.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatal(err)
	}
	fm := []byte("---\ntype: rule\nversion: 1.0.0\ndescription: d\ndeprecated: true\nreplaced_by: team/y\n---\n")
	rec := storetest.Seal(t, store.ManifestRecord{
		TenantID: "default", ArtifactID: "team/x", Version: "1.0.0", Type: "rule", Layer: "L",
		Frontmatter: fm, Body: []byte("body\n"), Deprecated: true, ReplacedBy: "team/y",
	}, nil, nil)
	if err := st.PutManifest(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	reg := core.New(st, "default", []layer.Layer{{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}}})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)

	url := ts.URL + "/v1/load_artifact?id=team/x"
	_, body := doLoad(t, http.MethodGet, url, "")
	head, _ := doLoad(t, http.MethodHead, url, "")
	got := head.Header.Get(server.DeprecationWarningHeader)
	if body["replaced_by"] != "team/y" || got == "" || got != body["deprecation_warning"] {
		t.Errorf("GET replaced_by=%v warning=%v; HEAD %s=%q; want team/y and the same warning text",
			body["replaced_by"], body["deprecation_warning"], server.DeprecationWarningHeader, got)
	}
}

// Spec: §7.2 (Deprecation members) — a load pinned to the live version
// carries no warning, so a matching If-None-Match still gets 304 and a HEAD
// carries no warning header; a batch entry for the latest load carries the
// members.
func TestLoadArtifact_PinnedLoadKeeps304AndBatchCarriesMembers(t *testing.T) {
	t.Parallel()
	ts, etag := newDeprecationServer(t)
	pinned := ts.URL + "/v1/load_artifact?id=team/a&version=1.0.0"
	if resp, _ := doLoad(t, http.MethodGet, pinned, etag); resp.StatusCode != http.StatusNotModified {
		t.Errorf("pinned GET with a matching If-None-Match: status %d, want 304", resp.StatusCode)
	}
	if head, _ := doLoad(t, http.MethodHead, pinned, ""); head.Header.Get(server.DeprecationWarningHeader) != "" {
		t.Errorf("pinned HEAD carries %q", head.Header.Get(server.DeprecationWarningHeader))
	}

	raw, _ := json.Marshal(map[string]any{"ids": []string{"team/a"}})
	resp, err := http.Post(ts.URL+"/v1/artifacts:batchLoad", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var entries []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatalf("batch body: %v", err)
	}
	if len(entries) != 1 || entries[0]["deprecation_warning"] == nil || entries[0]["replaced_by"] != "team/b" {
		t.Errorf("batch entries = %v, want one carrying the deprecation members", entries)
	}
}
