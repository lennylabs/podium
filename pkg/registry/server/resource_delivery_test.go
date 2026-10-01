package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// noStoreFixture boots a server.New() registry with NO object store
// configured (the §13.11 standalone-without-storage mode). Ingest in that
// mode keeps every resource inline regardless of size, so the store record
// carries inline bytes for a small text resource, a small binary resource,
// and a large binary resource above the inline cutoff.
func noStoreFixture(t *testing.T, refs []store.ResourceRef) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(context.Background(), storetest.Seal(t, store.ManifestRecord{
		TenantID: "default", ArtifactID: "finance/run", Version: "1.0.0",
		ContentHash: "sha256:c", Type: "skill", Layer: "L",
		Resources: refs,
	}, nil, nil)); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	srv := server.New(reg) // no WithObjectStore
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func inlineRef(path string, body []byte) store.ResourceRef {
	h := sha256.Sum256(body)
	return store.ResourceRef{
		Path:        path,
		ContentHash: "sha256:" + hex.EncodeToString(h[:]),
		Size:        int64(len(body)),
		ContentType: "application/octet-stream",
		Inline:      body,
	}
}

func binaryPayload(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i % 256) // includes 0xff/0xfe, not valid UTF-8 as a run
	}
	// Guarantee an invalid-UTF-8 sequence regardless of length.
	if n >= 2 {
		out[0] = 0xff
		out[1] = 0xfe
	}
	return out
}

func getLoadArtifact(t *testing.T, ts *httptest.Server) (server.LoadArtifactResponse, []byte) {
	t.Helper()
	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=finance/run")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, raw)
	}
	var parsed server.LoadArtifactResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	return parsed, raw
}

// Spec: §4.1/§7.2 — a binary bundled resource at or below the
// inline cutoff is base64-encoded on the wire (resources_base64) so
// encoding/json does not replace its non-UTF-8 bytes with U+FFFD. Without
// the fix the value is a corrupted UTF-8 string that no longer decodes to
// the original bytes.
func TestLoadArtifact_BinaryInlineResourceBase64RoundTrips(t *testing.T) {
	t.Parallel()
	binary := binaryPayload(64)
	text := []byte("print('hi')\n")
	ts := noStoreFixture(t, []store.ResourceRef{
		inlineRef("data/blob.bin", binary),
		inlineRef("scripts/run.py", text),
	})
	parsed, raw := getLoadArtifact(t, ts)

	if !parsed.ResourcesB64 {
		t.Fatalf("resources_base64 should be set for a binary inline resource:\n%s", raw)
	}
	// The whole inline set is base64 (response-wide flag): both decode.
	gotBin, err := base64.StdEncoding.DecodeString(parsed.Resources["data/blob.bin"])
	if err != nil {
		t.Fatalf("decode binary: %v", err)
	}
	if !bytes.Equal(gotBin, binary) {
		t.Errorf("binary resource corrupted: got %x, want %x", gotBin, binary)
	}
	gotText, err := base64.StdEncoding.DecodeString(parsed.Resources["scripts/run.py"])
	if err != nil {
		t.Fatalf("decode text: %v", err)
	}
	if !bytes.Equal(gotText, text) {
		t.Errorf("text resource corrupted: got %q, want %q", gotText, text)
	}
}

// Spec: §7.2 — an all-text inline set is delivered as literal strings with
// no resources_base64 flag, so the common case is unchanged and resources
// stay human-readable on the wire.
func TestLoadArtifact_TextInlineResourcesStayPlain(t *testing.T) {
	t.Parallel()
	ts := noStoreFixture(t, []store.ResourceRef{
		inlineRef("scripts/run.py", []byte("print('hi')\n")),
		inlineRef("data/table.csv", []byte("1,2,3\n")),
	})
	parsed, raw := getLoadArtifact(t, ts)
	if parsed.ResourcesB64 {
		t.Errorf("resources_base64 must stay unset for all-text resources:\n%s", raw)
	}
	if parsed.Resources["scripts/run.py"] != "print('hi')\n" {
		t.Errorf("text resource = %q", parsed.Resources["scripts/run.py"])
	}
}

// Spec: §7.2 — a resource above the inline cutoff that ingest kept
// inline (no object store configured) serves inline rather than failing with
// registry.unavailable. Without the fix attachResources routes it to
// presignResource on size alone and returns HTTP 500.
func TestLoadArtifact_LargeInlineResourceServedWithoutObjectStore(t *testing.T) {
	t.Parallel()
	large := binaryPayload(objectstore.InlineCutoff + 2048)
	ts := noStoreFixture(t, []store.ResourceRef{
		inlineRef("data/big.bin", large),
	})
	parsed, raw := getLoadArtifact(t, ts)

	if len(parsed.LargeResources) != 0 {
		t.Errorf("no object store: nothing should presign, got %+v", parsed.LargeResources)
	}
	if !parsed.ResourcesB64 {
		t.Fatalf("large binary inline resource should be base64:\n%s", raw)
	}
	got, err := base64.StdEncoding.DecodeString(parsed.Resources["data/big.bin"])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, large) {
		t.Errorf("large resource corrupted on inline delivery (%d vs %d bytes)", len(got), len(large))
	}
}

// Spec: §7.6.2 — the batch path delivers inline resources when no
// object store is configured, rather than silently dropping them. A binary
// resource carries inline_base64 so the bytes survive JSON transport; a text
// resource carries the literal string.
func TestBatchLoad_DeliversInlineResourcesWithoutObjectStore(t *testing.T) {
	t.Parallel()
	binary := binaryPayload(48)
	text := []byte("print('hi')\n")
	large := binaryPayload(objectstore.InlineCutoff + 1024)
	ts := noStoreFixture(t, []store.ResourceRef{
		inlineRef("data/blob.bin", binary),
		inlineRef("scripts/run.py", text),
		inlineRef("data/big.bin", large),
	})

	reqBody, _ := json.Marshal(map[string]any{"ids": []string{"finance/run"}})
	resp, err := http.Post(ts.URL+"/v1/artifacts:batchLoad", "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var envs []server.BatchLoadEnvelope
	if err := json.Unmarshal(raw, &envs); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if len(envs) != 1 || envs[0].Status != "ok" {
		t.Fatalf("envelopes = %+v, want one ok item", envs)
	}
	got := map[string]server.BatchResource{}
	for _, r := range envs[0].Resources {
		got[r.Path] = r
	}
	if len(got) != 3 {
		t.Fatalf("batch resources = %d, want 3 (none dropped): %+v", len(got), envs[0].Resources)
	}
	for _, r := range got {
		if r.PresignedURL != "" {
			t.Errorf("%s: no object store, presigned_url should be empty, got %q", r.Path, r.PresignedURL)
		}
	}
	// Text resource: literal inline, no base64.
	if r := got["scripts/run.py"]; r.InlineBase64 || r.Inline != "print('hi')\n" {
		t.Errorf("text resource = %+v, want literal inline", r)
	}
	// Binary resources: base64-flagged and decode back to the original bytes.
	for path, want := range map[string][]byte{"data/blob.bin": binary, "data/big.bin": large} {
		r := got[path]
		if !r.InlineBase64 {
			t.Errorf("%s: inline_base64 should be set", path)
			continue
		}
		dec, err := base64.StdEncoding.DecodeString(r.Inline)
		if err != nil {
			t.Errorf("%s: decode: %v", path, err)
			continue
		}
		if !bytes.Equal(dec, want) {
			t.Errorf("%s: inline bytes corrupted (%d vs %d)", path, len(dec), len(want))
		}
	}
}

// Spec: §13.4, §7.2, §7.6.2 — both serve paths choose a resource's source from
// the admitted ref's Inline rather than the server's object store: a ref held
// inline above the cutoff, as an ingest with no object store writes it, is
// served inline on the single load and the batch item, and never presigned
// at an object nobody wrote.
func TestResourceDelivery_InlineRefAboveCutoffServedInlineWithObjectStore(t *testing.T) {
	t.Parallel()
	big := bytes.Repeat([]byte("q"), objectstore.InlineCutoff+64)
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(context.Background(), storetest.Seal(t, store.ManifestRecord{
		TenantID: "default", ArtifactID: "finance/run", Version: "1.0.0", Type: "context", Layer: "L",
		Resources: []store.ResourceRef{{Path: "data/big.txt", Inline: big}},
	}, nil, nil)); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	objects := objectstore.NewMemory()
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	}).WithAdmission(nil, objects, objectstore.DefaultReadTimeout)
	ts := httptest.NewServer(server.New(reg, server.WithObjectStore(objects, "placeholder", 0)).Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=finance/run")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var single server.LoadArtifactResponse
	if err := json.NewDecoder(resp.Body).Decode(&single); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if single.Resources["data/big.txt"] != string(big) {
		t.Errorf("inline ref above the cutoff not served inline")
	}
	if _, ok := single.LargeResources["data/big.txt"]; ok {
		t.Errorf("inline ref presigned: %+v", single.LargeResources)
	}

	resp, err = http.Post(ts.URL+"/v1/artifacts:batchLoad", "application/json", strings.NewReader(`{"ids":["finance/run"]}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var batch []server.BatchLoadEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&batch); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if len(batch) != 1 || len(batch[0].Resources) != 1 {
		t.Fatalf("batch = %+v, want one item with one resource", batch)
	}
	if r := batch[0].Resources[0]; r.Inline != string(big) || r.PresignedURL != "" {
		t.Errorf("batch resource presigned_url = %q, inline %d bytes; want inline only", r.PresignedURL, len(r.Inline))
	}
}

// Spec: §4.1, §7.2 — the delivery split is inclusive at the inline cutoff:
// an object-held resource of exactly objectstore.InlineCutoff bytes is
// delivered inline, and one byte more is delivered as a large-resource link.
// The ref is seeded with Inline nil rather than driven through ingest,
// because ingest keeps a ref at the cutoff inline and attachResources serves
// any ref with Inline set before it compares sizes, which would pass against
// an exclusive comparison as well.
func TestResourceDelivery_InlineCutoffBoundary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		size   int
		inline bool
	}{
		{"at cutoff", objectstore.InlineCutoff, true},
		{"above cutoff", objectstore.InlineCutoff + 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// A valid UTF-8 body keeps resources_base64 unset, so the inline
			// value compares directly against the seeded bytes.
			body := bytes.Repeat([]byte("q"), tc.size)
			ts := objectHeldFixture(t, "data/edge.txt", body)
			parsed, _ := getLoadArtifact(t, ts)

			got, inInline := parsed.Resources["data/edge.txt"]
			_, inLarge := parsed.LargeResources["data/edge.txt"]
			if tc.inline {
				if !inInline || inLarge {
					t.Fatalf("size %d: inline=%v large=%v, want inline only", tc.size, inInline, inLarge)
				}
				if got != string(body) {
					t.Errorf("size %d: inline bytes differ (%d vs %d)", tc.size, len(got), len(body))
				}
				return
			}
			if inInline || !inLarge {
				t.Fatalf("size %d: inline=%v large=%v, want large only", tc.size, inInline, inLarge)
			}
		})
	}
}

// objectHeldFixture boots a registry and server sharing one memory object
// store that holds body under its bare hex digest, with a sealed manifest
// whose single resource ref points at that object and carries no inline copy.
func objectHeldFixture(t *testing.T, path string, body []byte) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	objects := objectstore.NewMemory()
	if err := objects.Put(context.Background(), digest, body, "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(context.Background(), storetest.Seal(t, store.ManifestRecord{
		TenantID: "default", ArtifactID: "finance/run", Version: "1.0.0", Type: "context", Layer: "L",
		Resources: []store.ResourceRef{{
			Path:        path,
			ContentHash: "sha256:" + digest,
			Size:        int64(len(body)),
		}},
	}, objects, nil)); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	}).WithAdmission(nil, objects, objectstore.DefaultReadTimeout)
	ts := httptest.NewServer(server.New(reg, server.WithObjectStore(objects, "placeholder", 0)).Handler())
	t.Cleanup(ts.Close)
	return ts
}
