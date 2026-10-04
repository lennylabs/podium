package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
	podiumsync "github.com/lennylabs/podium/pkg/sync"
	"github.com/lennylabs/podium/pkg/version"
)

// The delivery-attestation fixture holds a parent layer only alice can see
// and a child layer alice and bob can see. carol sees neither. Every request
// names its identity in the daUserHeader header.
const (
	daUserHeader = "X-Test-User"
	daParentPin  = "base/parent@1.0.0"
)

var (
	// daBigBody exceeds the inline cutoff and ends without a trailing
	// newline, so the merged document normalizes it (CODE-5).
	daBigBody = strings.Repeat("big line\n", objectstore.InlineCutoff/9+100) + "last line"
	daRecords = []store.ManifestRecord{
		{ArtifactID: "base/parent", Layer: "base", Type: "context",
			Frontmatter: []byte("---\ntype: context\nversion: 1.0.0\ndescription: Parent.\nsensitivity: low\n---\n\nparent prose\n")},
		{ArtifactID: "team/child", Layer: "team", Type: "context", ExtendsPin: daParentPin,
			Frontmatter: []byte("---\ntype: context\nversion: 1.0.0\ndescription: Child.\nextends: " + daParentPin + "\n---\n\nchild body\n")},
		{ArtifactID: "team/big", Layer: "team", Type: "context", ExtendsPin: daParentPin,
			Frontmatter: []byte("---\ntype: context\nversion: 1.0.0\ndescription: Big.\nextends: " + daParentPin + "\n---\n\n" + daBigBody)},
		{ArtifactID: "team/plain", Layer: "team", Type: "context", Sensitivity: "low",
			Frontmatter: []byte("---\ntype: context\nversion: 1.0.0\ndescription: Plain.\nsensitivity: low\n---\n\nplain body\n")},
		{ArtifactID: "team/withres", Layer: "team", Type: "context",
			Frontmatter: []byte("---\ntype: context\nversion: 1.0.0\ndescription: With resources.\n---\n\nresource body\n"),
			Resources:   []store.ResourceRef{{Path: "ref.md", Inline: []byte("reference\n")}, {Path: "a.md", Inline: []byte("a\n")}}},
		{ArtifactID: "team/skill", Layer: "team", Type: "skill",
			Frontmatter: []byte("---\ntype: skill\nversion: 1.0.0\ndescription: A skill.\n---\n"),
			SkillRaw:    []byte("---\nname: skill\ndescription: A skill.\n---\n\nskill prose\n")},
		{ArtifactID: "base/skillparent", Layer: "base", Type: "skill",
			Frontmatter: []byte("---\ntype: skill\nversion: 1.0.0\ndescription: Parent skill.\n---\n"),
			SkillRaw:    []byte("---\nname: skillparent\ndescription: Parent skill.\n---\n\nparent skill prose\n")},
		{ArtifactID: "team/bigskill", Layer: "team", Type: "skill", ExtendsPin: "base/skillparent@1.0.0",
			Frontmatter: []byte("---\ntype: skill\nversion: 1.0.0\ndescription: Big skill.\nextends: base/skillparent@1.0.0\n---\n"),
			SkillRaw:    []byte("---\nname: bigskill\ndescription: Big skill.\n---\n\n" + daBigBody)},
	}
)

// daFixture is a registry over a memory store and a filesystem object store,
// signing delivery hashes with a registry-managed key.
type daFixture struct {
	st      *store.Memory
	objects *objectstore.Filesystem
	ts      *httptest.Server
	pub     ed25519.PublicKey
	stored  map[string]store.ManifestRecord
}

func newDAFixture(t *testing.T) *daFixture {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	rlvLayer(t, st, store.LayerConfig{TenantID: "default", ID: "base", Order: 1, Users: []string{"alice"}})
	rlvLayer(t, st, store.LayerConfig{TenantID: "default", ID: "team", Order: 2, Users: []string{"alice", "bob"}})
	f := &daFixture{st: st, stored: map[string]store.ManifestRecord{}}
	for _, r := range daRecords {
		r.TenantID, r.Version = "default", "1.0.0"
		sealed := storetest.Seal(t, r, nil, nil)
		if err := st.PutManifest(ctx, sealed); err != nil {
			t.Fatalf("PutManifest %s: %v", r.ArtifactID, err)
		}
		f.stored[r.ArtifactID] = sealed
	}
	objects, err := objectstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("objectstore.Open: %v", err)
	}
	f.objects = objects
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	f.pub = pub
	reg := core.New(st, "default", nil).WithAdmission(nil, objects, objectstore.DefaultReadTimeout)
	srv := server.New(reg,
		server.WithObjectStore(objects, "placeholder", time.Hour),
		server.WithDeliverySigner(sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub}),
		server.WithIdentityResolver(func(r *http.Request) layer.Identity {
			sub := r.Header.Get(daUserHeader)
			return layer.Identity{Sub: sub, IsAuthenticated: sub != ""}
		}))
	f.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(f.ts.Close)
	objects.BaseURL = f.ts.URL
	return f
}

// do issues method against path as user with the given extra headers and
// returns the status, the response headers, and the raw body.
func (f *daFixture) do(t *testing.T, method, path, user string, body []byte, headers ...string) (int, http.Header, []byte) {
	t.Helper()
	target := path
	if !strings.HasPrefix(path, "http") {
		target = f.ts.URL + path
	}
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(daUserHeader, user)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

// load issues GET /v1/load_artifact for id as user and returns the decoded
// response and the raw body.
func (f *daFixture) load(t *testing.T, id, user string) (server.LoadArtifactResponse, []byte) {
	t.Helper()
	status, _, raw := f.do(t, http.MethodGet, "/v1/load_artifact?id="+id, user, nil)
	if status != http.StatusOK {
		t.Fatalf("load %s as %q = %d: %s", id, user, status, raw)
	}
	var resp server.LoadArtifactResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode load: %v", err)
	}
	return resp, raw
}

// batch issues POST /v1/artifacts:batchLoad for id as user and returns the
// decoded entry and the raw body.
func (f *daFixture) batch(t *testing.T, id, user string) (server.BatchLoadEnvelope, []byte) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"ids": []string{id}})
	status, _, raw := f.do(t, http.MethodPost, "/v1/artifacts:batchLoad", user, body)
	if status != http.StatusOK {
		t.Fatalf("batchLoad %s = %d: %s", id, status, raw)
	}
	var envs []server.BatchLoadEnvelope
	if err := json.Unmarshal(raw, &envs); err != nil || len(envs) != 1 || envs[0].Status != "ok" {
		t.Fatalf("batchLoad %s: %v %s", id, err, raw)
	}
	return envs[0], raw
}

// fetchObject follows a manifest_body_url as user, checks the bytes against
// the link's content hash, and returns them.
func (f *daFixture) fetchObject(t *testing.T, link *server.LargeResourceLink, user string) []byte {
	t.Helper()
	status, _, doc := f.do(t, http.MethodGet, link.URL, user, nil)
	if status != http.StatusOK {
		t.Fatalf("fetch %s as %q = %d: %s", link.URL, user, status, doc)
	}
	if got := daDigest(doc); got != link.ContentHash {
		t.Fatalf("fetched document hashes to %s, link names %s", got, link.ContentHash)
	}
	return doc
}

func daDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// singleRecord recomputes the delivery record from a load_artifact response's
// raw body through version.ParseLoadResponse, following manifest_body_url
// through /objects as user and placing the document with the body
// manifest.ManifestBodyOf derives, as every Go consumer does.
func (f *daFixture) singleRecord(t *testing.T, raw []byte, user string) version.DeliveryRecord {
	t.Helper()
	served, err := version.ParseLoadResponse(raw)
	if err != nil {
		t.Fatalf("ParseLoadResponse: %v", err)
	}
	if link := served.ManifestLink; link != nil {
		doc := f.fetchObject(t, &server.LargeResourceLink{URL: link.URL, ContentHash: link.ContentHash}, user)
		body, err := manifest.ManifestBodyOf(doc)
		if err != nil {
			t.Fatalf("ManifestBodyOf: %v", err)
		}
		served.PlaceManifestDocument(doc, body)
	}
	return served.Record
}

// batchRecord recomputes the delivery record of the single ok entry of a raw
// batch body through version.ParseBatchResponse, which frames the
// sensitivity the entry itself serves.
func batchRecord(t *testing.T, raw []byte) version.DeliveryRecord {
	t.Helper()
	entries, err := version.ParseBatchResponse(raw)
	if err != nil || len(entries) != 1 || entries[0].Err != nil {
		t.Fatalf("ParseBatchResponse = %+v, %v", entries, err)
	}
	return entries[0].Served.Record
}

// Spec: §4.7.10 — the single-load response and the batch envelope serve one
// delivery hash per artifact, both signatures verify under the registry key,
// and a recomputation from each path's own served bytes reproduces it, the
// batch entry's from its own sensitivity: a plain artifact with resources, a
// classified artifact, a merged child below the cutoff, a merged child
// whose merged document the single-load path serves by manifest_body_url, and
// a skill.
func TestLoadArtifact_SingleAndBatchAgreeOnTheDeliveryHash(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	verifier := sign.RegistryManagedKey{PublicKey: f.pub}
	for _, id := range []string{"team/withres", "team/plain", "team/child", "team/big", "team/skill"} {
		t.Run(id, func(t *testing.T) {
			single, singleRaw := f.load(t, id, "alice")
			env, batchRaw := f.batch(t, id, "alice")
			if single.DeliveryHash == "" || single.DeliveryHash != env.DeliveryHash {
				t.Fatalf("delivery hashes: single %q, batch %q; want equal and non-empty", single.DeliveryHash, env.DeliveryHash)
			}
			if single.ArtifactRevision == "" || single.ArtifactRevision != env.ArtifactRevision {
				t.Errorf("artifact_revision: single %q, batch %q; want equal and non-empty", single.ArtifactRevision, env.ArtifactRevision)
			}
			for path, sig := range map[string]string{"single": single.DeliverySignature, "batch": env.DeliverySignature} {
				if err := verifier.Verify(context.Background(), single.DeliveryHash, sig); err != nil {
					t.Errorf("%s delivery signature does not verify: %v", path, err)
				}
			}
			if id == "team/big" && single.ManifestBodyURL == nil {
				t.Error("the above-cutoff merged document was served inline on the single-load path")
			}
			if got := version.DeliveryHash(f.singleRecord(t, singleRaw, "alice")); got != single.DeliveryHash {
				t.Errorf("single-load recomputation = %s, served %s", got, single.DeliveryHash)
			}
			if id == "team/plain" && env.Sensitivity != "low" {
				t.Errorf("batch sensitivity = %q, want low", env.Sensitivity)
			}
			if env.Sensitivity != single.Sensitivity {
				t.Errorf("sensitivity: single %q, batch %q; want equal", single.Sensitivity, env.Sensitivity)
			}
			if got := version.DeliveryHash(batchRecord(t, batchRaw)); got != env.DeliveryHash {
				t.Errorf("batch recomputation = %s, served %s", got, env.DeliveryHash)
			}
		})
	}
}

// Spec: §4.6 hidden parents (withheld) — the raw response bytes of a merged
// child loaded by an identity that cannot see the parent layer name neither
// the parent nor carry a raw_frontmatter, manifest_merged, or signature key.
func TestLoadArtifact_MergedResponseCarriesNoParentReference(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	_, raw := f.load(t, "team/child", "bob")
	for _, needle := range []string{"base/parent", `"raw_frontmatter"`, `"manifest_merged"`, `"signature"`, `"extends_pin"`} {
		if bytes.Contains(raw, []byte(needle)) {
			t.Errorf("response carries %s:\n%s", needle, raw)
		}
	}
}

// daKeys returns the sorted top-level keys of a JSON object.
func daKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Spec: §4.6 hidden parents (withheld) — a merged and an unmerged response
// carry equal key sets, so no field's presence marks a merge.
func TestLoadArtifact_MergedAndUnmergedResponseKeysMatch(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	_, merged := f.load(t, "team/child", "bob")
	_, plain := f.load(t, "team/plain", "bob")
	if a, b := strings.Join(daKeys(t, merged), ","), strings.Join(daKeys(t, plain), ","); a != b {
		t.Errorf("merged keys %s, plain keys %s", a, b)
	}
}

// Spec: §4.6 hidden parents (withheld), §6.6, §13.12 — an above-cutoff merged
// manifest is served by manifest_body_url, the loading identity fetches it
// through /objects, and the body the fetched document splits to is the body
// the delivery hash frames, for a child body with no trailing newline.
func TestLoadArtifact_LargeMergedManifestUsesPresignedURL(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	resp, respRaw := f.load(t, "team/big", "bob")
	if resp.ManifestBodyURL == nil {
		t.Fatal("the above-cutoff merged manifest was served inline")
	}
	if resp.Frontmatter != "" || resp.ManifestBody != "" {
		t.Error("the inline document fields were not cleared")
	}
	rec := f.singleRecord(t, respRaw, "bob")
	if !strings.HasSuffix(rec.ManifestBody, "last line\n") {
		t.Errorf("re-derived body ends %q, want the normalized trailing newline", rec.ManifestBody[len(rec.ManifestBody)-12:])
	}
	if got := version.DeliveryHash(rec); got != resp.DeliveryHash {
		t.Errorf("recomputed delivery hash %s, served %s", got, resp.DeliveryHash)
	}
}

// Spec: §4.6 hidden parents (withheld), §7.2 — a parent-visible identity's raw
// response body carries the pinned parent in extends_pin.
func TestLoadArtifact_ExtendsPinFieldReachesTheWire(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	_, raw := f.load(t, "team/child", "alice")
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wire["extends_pin"] != daParentPin {
		t.Errorf("extends_pin = %v, want %s", wire["extends_pin"], daParentPin)
	}
}

// Spec: §4.6 hidden parents (withheld), §13.4 — a conditional GET carrying
// one identity's ETag, sent by an identity with the other parent visibility,
// answers 200 with the sender's own extends_pin, or none, and a different
// ETag, in both directions.
func TestLoadArtifact_ConditionalGetAcrossIdentitiesServesOwnExtendsPin(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	const path = "/v1/load_artifact?id=team/child"
	_, aliceHeaders, _ := f.do(t, http.MethodGet, path, "alice", nil)
	_, bobHeaders, _ := f.do(t, http.MethodGet, path, "bob", nil)
	aliceTag, bobTag := aliceHeaders.Get("ETag"), bobHeaders.Get("ETag")
	if aliceTag == "" || aliceTag == bobTag {
		t.Fatalf("ETags alice %q bob %q, want distinct", aliceTag, bobTag)
	}
	for _, tc := range []struct {
		user, sentTag, wantTag string
		wantPin                bool
	}{
		{user: "bob", sentTag: aliceTag, wantTag: bobTag, wantPin: false},
		{user: "alice", sentTag: bobTag, wantTag: aliceTag, wantPin: true},
	} {
		status, headers, raw := f.do(t, http.MethodGet, path, tc.user, nil, "If-None-Match", tc.sentTag)
		if status != http.StatusOK {
			t.Errorf("%s with the other identity's ETag = %d, want 200", tc.user, status)
			continue
		}
		if got := headers.Get("ETag"); got != tc.wantTag {
			t.Errorf("%s ETag = %q, want %q", tc.user, got, tc.wantTag)
		}
		if has := bytes.Contains(raw, []byte(`"extends_pin"`)); has != tc.wantPin {
			t.Errorf("%s response extends_pin present = %v, want %v", tc.user, has, tc.wantPin)
		}
	}
	if status, _, _ := f.do(t, http.MethodGet, path, "alice", nil, "If-None-Match", aliceTag); status != http.StatusNotModified {
		t.Errorf("alice with her own ETag = %d, want 304", status)
	}
}

// Spec: §4.6 hidden parents (withheld), §13.4 — each identity's HEAD ETag
// equals its own full-GET ETag, and only the parent-visible identity's differs
// from the content-hash ETag.
func TestLoadArtifact_HeadETagMatchesFullGetETagPerIdentity(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	const path = "/v1/load_artifact?id=team/child"
	contentTag := `"` + f.stored["team/child"].ContentHash + `"`
	for user, wantContentTag := range map[string]bool{"alice": false, "bob": true} {
		_, getHeaders, _ := f.do(t, http.MethodGet, path, user, nil)
		_, headHeaders, _ := f.do(t, http.MethodHead, path, user, nil)
		if headHeaders.Get("ETag") != getHeaders.Get("ETag") {
			t.Errorf("%s HEAD ETag %q, GET ETag %q", user, headHeaders.Get("ETag"), getHeaders.Get("ETag"))
		}
		if isContent := getHeaders.Get("ETag") == contentTag; isContent != wantContentTag {
			t.Errorf("%s ETag %q equals the content-hash ETag = %v, want %v", user, getHeaders.Get("ETag"), isContent, wantContentTag)
		}
	}
}

// randomKeyResponse fetches a random 64-hex /objects key as user, the answer
// every unauthorized key must match byte for byte.
func (f *daFixture) randomKeyResponse(t *testing.T, user string) (int, []byte) {
	t.Helper()
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	status, _, raw := f.do(t, http.MethodGet, "/objects/"+hex.EncodeToString(buf), user, nil)
	return status, raw
}

// wantNonexistent fails unless key answers user exactly as a random key does.
func (f *daFixture) wantNonexistent(t *testing.T, key, user string) {
	t.Helper()
	wantStatus, wantBody := f.randomKeyResponse(t, user)
	status, _, raw := f.do(t, http.MethodGet, "/objects/"+key, user, nil)
	if status != wantStatus || !bytes.Equal(raw, wantBody) {
		t.Errorf("key %s as %q = %d %s, want the random-key answer %d %s", key, user, status, raw, wantStatus, wantBody)
	}
}

// bigLink loads team/big as alice and returns its manifest_body_url.
func (f *daFixture) bigLink(t *testing.T) *server.LargeResourceLink {
	t.Helper()
	resp, _ := f.load(t, "team/big", "alice")
	if resp.ManifestBodyURL == nil {
		t.Fatal("team/big was served inline")
	}
	return resp.ManifestBodyURL
}

// Spec: §13.12 — an identity that can see the merged child fetches its
// manifest_body_url key through /objects.
func TestObjects_MergedManifestKeyServedToVisibleCaller(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	link := f.bigLink(t)
	for _, user := range []string{"alice", "bob"} {
		f.fetchObject(t, link, user)
	}
}

// Spec: §13.12 — an identity that cannot see the child receives the
// random-key 404 for the merged document's key.
func TestObjects_MergedManifestKeyInvisibleAnswersAsNonexistent(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	key := strings.TrimPrefix(f.bigLink(t).ContentHash, "sha256:")
	f.wantNonexistent(t, key, "carol")
}

// Spec: §13.12 — the stored pre-merge ARTIFACT.md of a pinned non-skill child
// is not a served document, so its key answers as nonexistent to an identity
// that can see the child, even when an object is stored under it.
func TestObjects_UnservedStoredDocumentKeyAnswersAsNonexistent(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	doc := f.stored["team/big"].Frontmatter
	key := core.ManifestBodyKey(doc)
	if err := f.objects.Put(context.Background(), key, doc, "text/markdown"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	f.wantNonexistent(t, key, "alice")
}

// Spec: §13.12 — after the parent is soft-deleted the recorded merged key
// authorizes nothing: it answers as nonexistent to an identity that can still
// see the child. Deleting the parent's layer tombstones the parent row, which
// resolveExtendsChain then cannot read.
func TestObjects_MergedManifestKeyWithBrokenChainAnswersAsNonexistent(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	key := strings.TrimPrefix(f.bigLink(t).ContentHash, "sha256:")
	if err := f.st.DeleteLayerConfig(context.Background(), "default", "base"); err != nil {
		t.Fatalf("DeleteLayerConfig: %v", err)
	}
	f.wantNonexistent(t, key, "alice")
}

// Spec: §13.12 — a merged skill serves its own stored SKILL.md, so that key
// stays owned whatever the skill's pin.
func TestObjects_MergedSkillStoredSkillMDServed(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	resp, _ := f.load(t, "team/bigskill", "alice")
	if resp.ManifestBodyURL == nil {
		t.Fatal("the above-cutoff SKILL.md was served inline")
	}
	if want := daDigest(f.stored["team/bigskill"].SkillRaw); resp.ManifestBodyURL.ContentHash != want {
		t.Errorf("link names %s, want the stored SKILL.md %s", resp.ManifestBodyURL.ContentHash, want)
	}
	f.fetchObject(t, resp.ManifestBodyURL, "alice")
}

// daUserTransport names user on every request, as the fixture's identity
// resolver reads it.
type daUserTransport struct{ user string }

func (u daUserTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(daUserHeader, u.user)
	return http.DefaultTransport.RoundTrip(r)
}

// syncAs runs a server-source sync of the fixture as user under check.
func (f *daFixture) syncAs(t *testing.T, user string, check *sign.DeliveryCheck) (string, error) {
	t.Helper()
	target := t.TempDir()
	_, err := podiumsync.Run(podiumsync.Options{
		RegistryPath: f.ts.URL,
		Target:       target,
		AdapterID:    "none",
		HTTPClient:   &http.Client{Transport: daUserTransport{user: user}, Timeout: 30 * time.Second},
		Delivery:     func() (*sign.DeliveryCheck, error) { return check, nil },
	})
	return target, err
}

// Spec: §4.7.10, §7.5 — a server-source sync against a registry that signs
// its delivery hashes verifies every record under always with the registry's
// key, including a merged child served by manifest_body_url, and materializes.
func TestServerSync_SignedAlwaysVerifies(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	check := &sign.DeliveryCheck{Policy: sign.PolicyAlways, Verifier: sign.RegistryManagedKey{Trusted: []ed25519.PublicKey{f.pub}}}
	target, err := f.syncAs(t, "alice", check)
	if err != nil {
		t.Fatalf("signed sync: %v", err)
	}
	lock, err := podiumsync.ReadLock(target)
	if err != nil || lock == nil || len(lock.Artifacts) == 0 {
		t.Fatalf("ReadLock = %+v, %v; want the materialized view", lock, err)
	}
}

// Spec: §4.7.10, §7.5 — a sync that trusts an unrelated key refuses with
// materialize.signature_invalid and writes nothing.
func TestServerSync_WrongKeyRefuses(t *testing.T) {
	t.Parallel()
	f := newDAFixture(t)
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	check := &sign.DeliveryCheck{Policy: sign.PolicyAlways, Verifier: sign.RegistryManagedKey{Trusted: []ed25519.PublicKey{other}}}
	target, err := f.syncAs(t, "alice", check)
	if err == nil || !strings.Contains(err.Error(), "materialize.signature_invalid") {
		t.Fatalf("wrong-key sync = %v, want materialize.signature_invalid", err)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("refused sync wrote %d entries under the target", len(entries))
	}
}
