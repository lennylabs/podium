package core_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
	"github.com/lennylabs/podium/pkg/vector"
)

// The retired-row fixtures. Each ID names the fixture it belongs to in the
// TEST-4 list: (a) C beside its parent P, (b) the same-ID overlay X, (c) the
// all-retired R, (d) D with a retired delegating version and its target T,
// (e) the escaped key E, (f) the tagged key F, and (g) the UTF-16LE key U. G
// is a plain artifact with two versions that the vector case adds.
const (
	rtTenant = "t"
	rtP      = "ops/parent"
	rtC      = "ops/child"
	rtX      = "ops/overlay"
	rtR      = "ops/retired"
	rtD      = "ops/delegator"
	rtT      = "ops/target"
	rtE      = "ops/escaped"
	rtF      = "ops/tagged"
	rtU      = "ops/utf16"
	rtG      = "ops/plain-g"
)

// rtRetiredIDs are the IDs whose every row the §4.6 no-inheritance rule
// retires, so no read path returns them.
var rtRetiredIDs = []string{rtR, rtE, rtF, rtU}

// rtServedIDs are the IDs with a servable row in fixtures (a) to (d).
var rtServedIDs = []string{rtP, rtC, rtX, rtD, rtT}

// rtMixedIDs are the served IDs that also hold a retired 2.0.0 row, so only
// their servable 1.0.0 row may answer a read.
var rtMixedIDs = []string{rtC, rtX, rtD}

// rtEpoch orders the fixture rows' ingest times, so a retired row is always
// the most recently ingested row of its ID and would be `latest` if it served.
var rtEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// rtDoc builds an ARTIFACT.md from frontmatter lines and a body.
func rtDoc(frontmatter, body string) []byte {
	return []byte("---\n" + frontmatter + "---\n\n" + body)
}

// rtUTF16LEDoc builds an ARTIFACT.md whose frontmatter block is text in
// UTF-16LE behind its byte order mark, between ASCII "---" lines, so the
// stored bytes hold none of the substring extends, a backslash, and "!".
func rtUTF16LEDoc(text string) []byte {
	doc := append([]byte("---\n"), 0xff, 0xfe)
	for _, u := range utf16.Encode([]rune(text)) {
		doc = binary.LittleEndian.AppendUint16(doc, u)
	}
	return append(doc, "\n---\n\nUTF-16 body.\n"...)
}

// rtRow builds one fixture row. The record carries no pin column, which is
// the empty-pin case: retirement reads the stored bytes alone.
func rtRow(id, ver, lay string, hour int, doc []byte, resources ...store.ResourceRef) store.ManifestRecord {
	return store.ManifestRecord{
		TenantID: rtTenant, ArtifactID: id, Version: ver, Layer: lay, Type: "context",
		Description: "Fixture " + id + " " + ver + ".",
		Frontmatter: doc, Body: []byte("Body of " + id + "@" + ver + ".\n"),
		IngestedAt: rtEpoch.Add(time.Duration(hour) * time.Hour),
		Resources:  resources,
	}
}

// rtPlain builds a plain context row whose manifest parses and admits.
func rtPlain(id, ver, lay string, hour int, resources ...store.ResourceRef) store.ManifestRecord {
	doc := rtDoc("type: context\nversion: "+ver+"\ndescription: Fixture "+id+" "+ver+".\n", "Body of "+id+"@"+ver+".\n")
	return rtRow(id, ver, lay, hour, doc, resources...)
}

// rtResource is an inline resource whose bytes the object route also serves.
func rtResource(body string) store.ResourceRef {
	return store.ResourceRef{Path: "notes.md", ContentType: "text/markdown", Inline: []byte(body)}
}

// rtFixtures returns the rows of each fixture, keyed by its TEST-4 letter,
// and "g" for the plain two-version G.
func rtFixtures() map[string][]store.ManifestRecord {
	return map[string][]store.ManifestRecord{
		"a": {
			rtPlain(rtP, "1.0.0", "base", 1, rtResource("parent resource\n")),
			rtPlain(rtC, "1.0.0", "base", 2),
			rtRow(rtC, "2.0.0", "base", 3, rtDoc("type: context\nversion: 2.0.0\nextends: "+rtP+"\n", "Child body.\n")),
		},
		"b": {
			rtPlain(rtX, "1.0.0", "base", 4),
			rtRow(rtX, "2.0.0", "team", 5, rtDoc("type: context\nversion: 2.0.0\nextends: "+rtX+"\n", "Overlay body.\n")),
		},
		"c": {
			rtRow(rtR, "1.0.0", "team", 6, rtDoc("type: context\nversion: 1.0.0\nextends: "+rtP+"\n", "Retired body.\n"), rtResource("retired resource\n")),
		},
		"d": {
			rtPlain(rtT, "1.0.0", "base", 7),
			rtPlain(rtD, "1.0.0", "base", 8),
			rtRow(rtD, "2.0.0", "base", 9, rtDoc("type: context\nversion: 2.0.0\nextends: "+rtP+"\ndelegates_to: ["+rtT+"]\n", "Delegator body.\n")),
		},
		"e": {
			rtRow(rtE, "1.0.0", "team", 10, rtDoc("type: context\nversion: 1.0.0\n\"\\x65xtends\": "+rtP+"\n", "Escaped body.\n")),
		},
		"f": {
			rtRow(rtF, "1.0.0", "team", 11, rtDoc("type: context\nversion: 1.0.0\n!!binary ZXh0ZW5kcw==: "+rtP+"\n", "Tagged body.\n")),
		},
		"g": {
			rtPlain(rtG, "1.0.0", "base", 12),
			rtPlain(rtG, "1.1.0", "base", 13),
		},
		"u": {
			rtRow(rtU, "1.0.0", "team", 14, rtUTF16LEDoc("type: context\nversion: 1.0.0\nextends: "+rtP+"\n")),
		},
	}
}

// rtLayers are the two public registry-side layers the fixtures live in.
var rtLayers = []layer.Layer{
	{ID: "base", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	{ID: "team", Precedence: 2, Visibility: layer.Visibility{Public: true}},
}

// rtSeed stores the sealed rows of the named fixtures and returns them by
// "id@version". Fixture (d) also stores the D→T delegates_to edge, and (c)
// stores an R→T edge, which no dependents_of answer returns because R has no
// servable row.
func rtSeed(t *testing.T, st store.Store, letters ...string) map[string]store.ManifestRecord {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: rtTenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	fixtures := rtFixtures()
	out := map[string]store.ManifestRecord{}
	for _, letter := range letters {
		for _, rec := range fixtures[letter] {
			sealed := rtPut(t, st, rec)
			out[rec.ArtifactID+"@"+rec.Version] = sealed
		}
	}
	edges := map[string]store.DependencyEdge{
		"c": {From: rtR, To: rtT, Kind: "delegates_to"},
		"d": {From: rtD, To: rtT, Kind: "delegates_to"},
	}
	for _, letter := range letters {
		if e, ok := edges[letter]; ok {
			if err := st.PutDependency(ctx, rtTenant, e); err != nil {
				t.Fatalf("PutDependency %s->%s: %v", e.From, e.To, err)
			}
		}
	}
	return out
}

// rtPut seals rec with the framed content hash and stores it.
func rtPut(t testing.TB, st store.Store, rec store.ManifestRecord) store.ManifestRecord {
	t.Helper()
	sealed := storetest.Seal(t, rec, nil, nil)
	if err := st.PutManifest(context.Background(), sealed); err != nil {
		t.Fatalf("PutManifest %s@%s: %v", rec.ArtifactID, rec.Version, err)
	}
	return sealed
}

// rtAllLetters names every fixture.
var rtAllLetters = []string{"a", "b", "c", "d", "e", "f", "g", "u"}

// rtRegistry builds a registry over a store holding every fixture, with an
// audit recorder, and returns the seeded rows.
func rtRegistry(t *testing.T) (*core.Registry, *store.Memory, *recorder, map[string]store.ManifestRecord) {
	t.Helper()
	st := store.NewMemory()
	rows := rtSeed(t, st, rtAllLetters...)
	rec := &recorder{}
	return core.New(st, rtTenant, rtLayers).WithAudit(rec.emit), st, rec, rows
}

// rtSearchIDs returns the IDs and versions an empty-query search returns.
func rtSearchIDs(t *testing.T, reg *core.Registry, id layer.Identity, asAdmin bool) map[string]string {
	t.Helper()
	res, err := reg.SearchArtifacts(context.Background(), id, core.SearchArtifactsOptions{TopK: 50, AsAdmin: asAdmin})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	out := map[string]string{}
	for _, d := range res.Results {
		out[d.ID] = d.Version
	}
	return out
}

// rtWantNotFound fails unless err is the core not-found sentinel.
func rtWantNotFound(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("%s: err = %v, want registry.not_found", what, err)
	}
}

// Spec: §4.6, §13.4 — a stored row whose ARTIFACT.md declares extends: is
// retired: version resolution falls back to the newest other row of its ID, an
// exact or content-hash load of it is not found, and an ID whose every row is
// retired is absent from every core read path, with no visibility.denied
// event, because the rule reads the stored bytes and never a pin. The quota
// read still counts the retired bytes.
func TestRetiredRows_CoreReadPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg, st, rec, rows := rtRegistry(t)

	// (a) latest falls back to C@1.0.0; the retired C@2.0.0 is not found by
	// exact version or by content hash.
	if res, err := reg.LoadArtifact(ctx, publicID, rtC, core.LoadArtifactOptions{}); err != nil || res.Version != "1.0.0" {
		t.Errorf("latest %s = %+v, %v; want 1.0.0", rtC, res, err)
	}
	_, err := reg.LoadArtifact(ctx, publicID, rtC, core.LoadArtifactOptions{Version: "2.0.0"})
	rtWantNotFound(t, rtC+"@2.0.0", err)
	_, err = reg.LoadArtifact(ctx, publicID, rtC, core.LoadArtifactOptions{Version: rows[rtC+"@2.0.0"].ContentHash})
	rtWantNotFound(t, rtC+" by the retired hash", err)

	// (b) the lower layer's copy of a same-ID overlay serves.
	if res, err := reg.LoadArtifact(ctx, publicID, rtX, core.LoadArtifactOptions{}); err != nil || res.Version != "1.0.0" || res.Layer != "base" {
		t.Errorf("latest %s = %+v, %v; want 1.0.0 from base", rtX, res, err)
	}

	// (c), (e), (f), and (g): an all-retired ID is not found on a load, on an
	// admin diagnostic load, and on a load the Revalidate path would answer.
	admin := layer.Identity{Sub: "alice", IsAuthenticated: true}
	if err := st.GrantAdmin(ctx, store.AdminGrant{UserID: "alice", OrgID: rtTenant}); err != nil {
		t.Fatalf("GrantAdmin: %v", err)
	}
	for _, id := range rtRetiredIDs {
		_, err := reg.LoadArtifact(ctx, publicID, id, core.LoadArtifactOptions{})
		rtWantNotFound(t, "load "+id, err)
		_, err = reg.LoadArtifact(ctx, admin, id, core.LoadArtifactOptions{AsAdmin: true})
		rtWantNotFound(t, "admin load "+id, err)
		_, err = reg.LoadArtifact(ctx, publicID, id, core.LoadArtifactOptions{
			Revalidate: func(*core.LoadArtifactResult) bool { return true },
		})
		rtWantNotFound(t, "revalidating load "+id, err)
	}
	for _, e := range rec.snapshot() {
		if e.Type == "visibility.denied" {
			t.Errorf("a load of a retired row emitted visibility.denied for %s", e.Target)
		}
	}

	// search_artifacts, its admin form, load_domain, and the catalog list no
	// retired ID and list every servable one at its served version.
	for name, found := range map[string]map[string]string{
		"search":       rtSearchIDs(t, reg, publicID, false),
		"admin search": rtSearchIDs(t, reg, admin, true),
	} {
		for _, id := range rtRetiredIDs {
			if _, ok := found[id]; ok {
				t.Errorf("%s returned the retired %s", name, id)
			}
		}
		for _, id := range rtServedIDs {
			if _, ok := found[id]; !ok {
				t.Errorf("%s omitted the servable %s: %v", name, id, found)
			}
		}
		if found[rtC] != "1.0.0" || found[rtX] != "1.0.0" || found[rtD] != "1.0.0" {
			t.Errorf("%s versions = %v, want C, X, and D at 1.0.0", name, found)
		}
	}
	dom, err := reg.LoadDomain(ctx, publicID, "ops", core.LoadDomainOptions{NotableCount: 50})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	notable := map[string]string{}
	for _, d := range dom.Notable {
		notable[d.ID] = d.Version
	}
	catalog, err := reg.Catalog(ctx, publicID, "ops")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	listed := map[string]bool{}
	for _, c := range catalog {
		listed[c.ID] = true
	}
	for _, id := range rtRetiredIDs {
		if _, ok := notable[id]; ok {
			t.Errorf("load_domain notable lists the retired %s", id)
		}
		if listed[id] {
			t.Errorf("the catalog lists the retired %s", id)
		}
	}
	if notable[rtC] != "1.0.0" || !listed[rtC] {
		t.Errorf("load_domain notable %v and catalog %v must list %s at 1.0.0", notable, listed, rtC)
	}

	// (d) the D→T edge survives because D has a servable row; R's edge does
	// not, because R has none.
	edges, err := reg.DependentsOf(ctx, publicID, rtT)
	if err != nil {
		t.Fatalf("DependentsOf: %v", err)
	}
	if len(edges) != 1 || edges[0].From != rtD || edges[0].Kind != "delegates_to" {
		t.Errorf("dependents_of(%s) = %+v, want the single %s delegates_to edge", rtT, edges, rtD)
	}

	// The §4.7.8 quota read counts every stored row, retired ones included.
	q, err := reg.Quota(ctx)
	if err != nil {
		t.Fatalf("Quota: %v", err)
	}
	var want int64
	for _, m := range rows {
		want += int64(len(m.Frontmatter)) + int64(len(m.Body))
	}
	r := rows[rtR+"@1.0.0"]
	if q.StorageBytes != want || len(r.Frontmatter)+len(r.Body) == 0 {
		t.Errorf("StorageBytes = %d, want %d, which includes the %d bytes of %s", q.StorageBytes, want, len(r.Frontmatter)+len(r.Body), rtR)
	}
}

// rtHTTP issues one request against the server and returns its status, its
// ETag header, and its body.
func rtHTTP(t *testing.T, method, url, ifNoneMatch string, body []byte) (int, string, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, resp.Header.Get("ETag"), raw
}

// rtETag formats a stored row's content hash as its load_artifact ETag.
func rtETag(rec store.ManifestRecord) string {
	return `"` + rec.ContentHash + `"`
}

// rtServer serves a registry over every fixture through the HTTP handler,
// with a filesystem object store holding the bytes of each fixture resource.
func rtServer(t *testing.T) (string, map[string]store.ManifestRecord) {
	t.Helper()
	reg, _, _, rows := rtRegistry(t)
	objs, err := objectstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("objectstore.Open: %v", err)
	}
	for _, m := range rows {
		for _, ref := range m.Resources {
			if err := objs.Put(context.Background(), strings.TrimPrefix(ref.ContentHash, "sha256:"), ref.Inline, ref.ContentType); err != nil {
				t.Fatalf("Put %s: %v", ref.Path, err)
			}
		}
	}
	srv := server.New(reg, server.WithObjectStore(objs, "placeholder", time.Hour))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	objs.BaseURL = ts.URL
	return ts.URL, rows
}

// Spec: §4.6, §13.4, §7.2 — over HTTP a retired row is answered 404, never
// 304, on a HEAD and on a conditional GET by exact version and by content
// hash, a latest GET that carries the retired row's ETag gets a full 200 for
// the served row, and an all-retired ID is absent from HEAD, the batch load,
// and the object route.
func TestRetiredRows_HTTPValidatorsAndRoutes(t *testing.T) {
	t.Parallel()
	base, rows := rtServer(t)
	retired := rows[rtC+"@2.0.0"]
	served := rows[rtC+"@1.0.0"]
	load := base + "/v1/load_artifact?id=" + rtC

	for _, version := range []string{"2.0.0", retired.ContentHash} {
		url := load + "&version=" + version
		for _, c := range []struct {
			method, inm string
		}{
			{http.MethodGet, ""},
			{http.MethodHead, ""},
			{http.MethodGet, rtETag(retired)},
			{http.MethodHead, rtETag(retired)},
		} {
			status, _, body := rtHTTP(t, c.method, url, c.inm, nil)
			if status != http.StatusNotFound {
				t.Errorf("%s %s If-None-Match=%q = %d, want 404", c.method, url, c.inm, status)
			}
			if c.method == http.MethodGet && !strings.Contains(string(body), "registry.not_found") {
				t.Errorf("%s %s body = %s, want registry.not_found", c.method, url, body)
			}
		}
	}

	status, etag, body := rtHTTP(t, http.MethodGet, load, rtETag(retired), nil)
	var got server.LoadArtifactResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("latest body: %v (%s)", err, body)
	}
	if status != http.StatusOK || got.Version != "1.0.0" || etag != rtETag(served) {
		t.Errorf("latest with the retired ETag = %d version %q ETag %s, want 200 1.0.0 %s", status, got.Version, etag, rtETag(served))
	}

	for _, id := range rtRetiredIDs {
		if status, _, _ := rtHTTP(t, http.MethodHead, base+"/v1/load_artifact?id="+id, "", nil); status != http.StatusNotFound {
			t.Errorf("HEAD %s = %d, want 404", id, status)
		}
	}

	// §7.6.2 answers an item the caller cannot load with the envelope a
	// nonexistent ID gets, so a retired ID must come back as that envelope.
	const missing = "ops/does-not-exist"
	req, err := json.Marshal(server.BatchLoadRequest{IDs: append([]string{rtC, missing}, rtRetiredIDs...)})
	if err != nil {
		t.Fatalf("marshal batch request: %v", err)
	}
	status, _, body = rtHTTP(t, http.MethodPost, base+"/v1/artifacts:batchLoad", "", req)
	var batch []server.BatchLoadEnvelope
	if err := json.Unmarshal(body, &batch); err != nil || status != http.StatusOK || len(batch) != 2+len(rtRetiredIDs) {
		t.Fatalf("batchLoad = %d %s: %v", status, body, err)
	}
	if batch[0].Status != "ok" || batch[0].Version != "1.0.0" {
		t.Errorf("batch %s = %s@%s, want ok@1.0.0", batch[0].ID, batch[0].Status, batch[0].Version)
	}
	absent := batch[1]
	if absent.Status != "error" || absent.Error == nil || absent.Error.Code != "visibility.denied" {
		t.Fatalf("batch %s = %s %+v, want a visibility.denied error", missing, absent.Status, absent.Error)
	}
	for _, env := range batch[2:] {
		if env.Status != "error" || env.Error == nil || env.Error.Code != absent.Error.Code || env.Error.Message != absent.Error.Message {
			t.Errorf("batch %s = %s %+v, want the envelope of a nonexistent ID %+v", env.ID, env.Status, env.Error, absent.Error)
		}
	}

	key := func(rec store.ManifestRecord) string {
		return strings.TrimPrefix(rec.Resources[0].ContentHash, "sha256:")
	}
	if status, _, _ := rtHTTP(t, http.MethodGet, base+"/objects/"+key(rows[rtR+"@1.0.0"]), "", nil); status != http.StatusNotFound {
		t.Errorf("object route for the retired %s resource = %d, want 404", rtR, status)
	}
	if status, _, _ := rtHTTP(t, http.MethodGet, base+"/objects/"+key(rows[rtP+"@1.0.0"]), "", nil); status != http.StatusOK {
		t.Errorf("object route for the %s resource = %d, want 200", rtP, status)
	}
}

// rtPutConcurrent seals and stores rec from a goroutine other than the test's
// own, so a failure is reported with Errorf rather than ending the test.
func rtPutConcurrent(t *testing.T, st store.Store, rec store.ManifestRecord) {
	if err := st.PutManifest(context.Background(), storetest.Seal(t, rec, nil, nil)); err != nil {
		t.Errorf("PutManifest %s@%s: %v", rec.ArtifactID, rec.Version, err)
	}
}

// Spec: §4.6 — the memoized verdict is stable across listings, and it is
// keyed by the content hash, so a row stored under a purged and reused
// (id, version) with other bytes is judged afresh in both directions.
func TestRetiredRows_MemoKeyedByContentHash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg, st, _, _ := rtRegistry(t)

	first := rtSearchIDs(t, reg, publicID, false)
	second := rtSearchIDs(t, reg, publicID, false)
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Errorf("second listing = %v, want the first %v", second, first)
	}

	// A plain row is served, then a retired row under the same key is not.
	const toRetired, toPlain = "ops/reuse-retired", "ops/reuse-plain"
	plain := rtPlain(toRetired, "1.0.0", "base", 20)
	if _, err := reg.LoadArtifact(ctx, publicID, toRetired, core.LoadArtifactOptions{}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("load before the first store = %v, want not found", err)
	}
	plain.Deprecated = true
	rtPut(t, st, plain)
	if res, err := reg.LoadArtifact(ctx, publicID, toRetired, core.LoadArtifactOptions{}); err != nil || res.Version != "1.0.0" {
		t.Fatalf("load of the plain row = %+v, %v; want 1.0.0", res, err)
	}
	if _, err := st.PurgeDeprecatedManifests(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("PurgeDeprecatedManifests: %v", err)
	}
	rtPut(t, st, rtRow(toRetired, "1.0.0", "base", 21, rtDoc("type: context\nversion: 1.0.0\nextends: "+rtP+"\n", "Reused body.\n")))
	_, err := reg.LoadArtifact(ctx, publicID, toRetired, core.LoadArtifactOptions{})
	rtWantNotFound(t, "the retired row under a reused key", err)

	// A retired row is not served, then a plain row under the same key is.
	retired := rtRow(toPlain, "1.0.0", "base", 22, rtDoc("type: context\nversion: 1.0.0\nextends: "+rtP+"\n", "Retired first.\n"))
	retired.Deprecated = true
	rtPut(t, st, retired)
	_, err = reg.LoadArtifact(ctx, publicID, toPlain, core.LoadArtifactOptions{})
	rtWantNotFound(t, "the retired row before reuse", err)
	if _, err := st.PurgeDeprecatedManifests(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("PurgeDeprecatedManifests: %v", err)
	}
	stored := rtPut(t, st, rtPlain(toPlain, "1.0.0", "base", 23))
	if res, err := reg.LoadArtifact(ctx, publicID, toPlain, core.LoadArtifactOptions{}); err != nil || res.ContentHash != stored.ContentHash {
		t.Errorf("load of the plain row under a reused key = %+v, %v; want %s", res, err, stored.ContentHash)
	}
}

// Spec: §4.6, §4.7 — the vector index still holds the vector a 0.5.x registry
// built for a retired row, and the vector ranker keeps a match only when its
// (id, version) names a servable row. A query that only the retired rows'
// vectors and an older servable version's vector match returns G at its
// served version and neither C nor X.
func TestSearchArtifacts_RetiredRowVectorsDoNotSurface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	rtSeed(t, st, "a", "b", "g")
	const dim = 8
	query := basis(dim, 0)
	vec := vector.NewMemory(dim)
	for _, row := range [][2]string{{rtC, "2.0.0"}, {rtX, "2.0.0"}, {rtG, "1.0.0"}} {
		if err := vec.Put(ctx, rtTenant, row[0], row[1], query); err != nil {
			t.Fatalf("vector Put %s@%s: %v", row[0], row[1], err)
		}
	}
	reg := core.New(st, rtTenant, rtLayers).
		WithVectorSearch(vec, scriptedEmbedder{dim: dim, vecs: map[string][]float32{"xyzzy": query}})

	res, err := reg.SearchArtifacts(ctx, publicID, core.SearchArtifactsOptions{Query: "xyzzy", TopK: 10})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if res.Degraded {
		t.Errorf("Degraded = true, want the hybrid path")
	}
	if len(res.Results) != 1 || res.Results[0].ID != rtG || res.Results[0].Version != "1.1.0" || res.TotalMatched != 1 {
		t.Errorf("results = %+v (total %d), want only %s@1.1.0 and TotalMatched 1", res.Results, res.TotalMatched, rtG)
	}
}

// Spec: §4.6 — the retired-row memo is written on the read path of concurrent
// requests, so this test must pass under -race (the S11 command runs it with
// -race). Readers issue SearchArtifacts, LoadArtifact, and DependentsOf while
// a writer stores new plain and retired rows with distinct content hashes,
// which covers first-time memo population. The plain ops/reused row is stored
// before the readers start, and every reader lists it before the writes
// begin, so the memo holds its plain verdict when the writer purges it and
// stores a retired row under the same (id, version) while the readers are
// mid-listing. A memo keyed by (id, version) instead of the content hash
// would keep serving that key. No answer ever carries a retired row, and
// every plain row a reader expects appears.
func TestServableRows_ConcurrentListingsAndIngest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg, st, _, _ := rtRegistry(t)
	const reused = "ops/reused"
	reusedPlain := rtPlain(reused, "1.0.0", "base", 30)
	reusedPlain.Deprecated = true
	reusedPlainHash := rtPut(t, st, reusedPlain).ContentHash

	const writes, readers = 40, 4
	done := make(chan struct{})
	var ready, wg sync.WaitGroup
	ready.Add(readers)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		// The writes start once every reader has answered once, so they
		// overlap the readers' listings and every reader has memoized the
		// plain verdict for ops/reused before its key is reused.
		ready.Wait()
		for i := range writes {
			rtPutConcurrent(t, st, rtPlain(fmt.Sprintf("ops/new-plain-%d", i), "1.0.0", "base", 40+i))
			rtPutConcurrent(t, st, rtRow(fmt.Sprintf("ops/new-retired-%d", i), "1.0.0", "team", 40+i,
				rtDoc(fmt.Sprintf("type: context\nversion: 1.0.0\nextends: %s\nnote: %d\n", rtP, i), "New retired.\n")))
			if i == writes/2 {
				if _, err := st.PurgeDeprecatedManifests(ctx, time.Now().Add(time.Hour)); err != nil {
					t.Errorf("PurgeDeprecatedManifests: %v", err)
				}
				rtPutConcurrent(t, st, rtRow(reused, "1.0.0", "base", 31,
					rtDoc("type: context\nversion: 1.0.0\nextends: "+rtP+"\n", "Reused retired.\n")))
			}
		}
	}()

	// check runs one round of reads and reports whether the search listed
	// ops/reused, which only the plain row may answer.
	check := func() (sawReused bool) {
		res, err := reg.SearchArtifacts(ctx, publicID, core.SearchArtifactsOptions{TopK: 50, IncludeDeprecated: true})
		if err != nil {
			t.Errorf("SearchArtifacts: %v", err)
			return false
		}
		found := make([]string, 0, len(res.Results))
		for _, d := range res.Results {
			found = append(found, d.ID)
			if d.ID == reused {
				sawReused = true
				if strings.Contains(d.Frontmatter, "extends:") {
					t.Errorf("search returned the retired row under the reused key %s", reused)
				}
			}
			if strings.HasPrefix(d.ID, "ops/new-retired-") || slices.Contains(rtRetiredIDs, d.ID) {
				t.Errorf("search returned the retired %s", d.ID)
			}
			// C, X, and D each keep a retired 2.0.0 row beside the served
			// 1.0.0 row, so a memo race on a mixed ID surfaces as a result
			// at any version other than 1.0.0.
			if slices.Contains(rtMixedIDs, d.ID) && d.Version != "1.0.0" {
				t.Errorf("search returned %s@%s, want only the servable 1.0.0", d.ID, d.Version)
			}
		}
		for _, id := range rtServedIDs {
			if !slices.Contains(found, id) {
				t.Errorf("search omitted the servable %s", id)
			}
		}
		if l, err := reg.LoadArtifact(ctx, publicID, rtC, core.LoadArtifactOptions{}); err != nil || l.Version != "1.0.0" {
			t.Errorf("latest %s = %+v, %v; want 1.0.0", rtC, l, err)
		}
		if l, err := reg.LoadArtifact(ctx, publicID, reused, core.LoadArtifactOptions{}); err == nil && l.ContentHash != reusedPlainHash {
			t.Errorf("%s served %s, want only the plain row's %s", reused, l.ContentHash, reusedPlainHash)
		}
		if edges, err := reg.DependentsOf(ctx, publicID, rtT); err != nil || len(edges) != 1 || edges[0].From != rtD {
			t.Errorf("dependents_of(%s) = %+v, %v; want only %s", rtT, edges, err, rtD)
		}
		return sawReused
	}
	reader := func() {
		defer wg.Done()
		if !check() {
			t.Errorf("the first listing omitted %s, so its plain verdict was not memoized before the reuse", reused)
		}
		ready.Done()
		for {
			select {
			case <-done:
				return
			default:
				check()
			}
		}
	}
	for range readers {
		wg.Add(1)
		go reader()
	}
	wg.Wait()

	found := rtSearchIDs(t, reg, publicID, false)
	for i := range writes {
		if _, ok := found[fmt.Sprintf("ops/new-plain-%d", i)]; !ok {
			t.Errorf("search omitted ops/new-plain-%d", i)
		}
		_, err := reg.LoadArtifact(ctx, publicID, fmt.Sprintf("ops/new-retired-%d", i), core.LoadArtifactOptions{})
		rtWantNotFound(t, fmt.Sprintf("ops/new-retired-%d", i), err)
		if _, err := reg.LoadArtifact(ctx, publicID, fmt.Sprintf("ops/new-plain-%d", i), core.LoadArtifactOptions{}); err != nil {
			t.Errorf("load ops/new-plain-%d: %v", i, err)
		}
	}
	_, err := reg.LoadArtifact(ctx, publicID, reused, core.LoadArtifactOptions{})
	rtWantNotFound(t, "the retired row under the reused key", err)
}
