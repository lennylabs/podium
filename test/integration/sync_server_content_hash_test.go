package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sync"
)

// Spec: §14.11 / §7.5.3 — a server-source `podium sync` pins the
// registry's authoritative content_hash into the committed lock, not a digest
// recomputed from the served bytes. The real registry runs in-process (the same
// shared bootstrap the standalone server uses), the sync materializes against
// it, then the test re-reads each artifact's content_hash directly from
// /v1/load_artifact and asserts the lock matches it byte-for-byte. The test
// owns the server lifecycle via httptest and never blocks.
func TestSyncServerSource_LockPinsRegistryContentHash(t *testing.T) {
	t.Parallel()
	dir := referenceRegistryPath(t)
	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	target := t.TempDir()
	if _, err := sync.Run(sync.Options{RegistryPath: ts.URL, Target: target, AdapterID: "none"}); err != nil {
		t.Fatalf("server sync.Run: %v", err)
	}
	lock, err := sync.ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock == nil || len(lock.Artifacts) == 0 {
		t.Fatalf("lock missing or empty: %+v", lock)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	want := map[string]string{} // id -> registry content_hash, fetched once
	checked := 0
	for _, la := range lock.Artifacts {
		h, ok := want[la.ID]
		if !ok {
			h = registryContentHash(t, client, ts.URL, la.ID)
			want[la.ID] = h
		}
		if h == "" {
			t.Fatalf("registry returned empty content_hash for %s", la.ID)
		}
		if la.ContentHash != h {
			t.Errorf("lock %s content_hash = %q, want the registry value %q", la.ID, la.ContentHash, h)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no lock artifacts checked")
	}
}

// registryContentHash fetches one artifact's authoritative content_hash from
// the registry's /v1/load_artifact endpoint with a bounded client.
func registryContentHash(t *testing.T, client *http.Client, baseURL, id string) string {
	t.Helper()
	resp, err := client.Get(baseURL + "/v1/load_artifact?id=" + url.QueryEscape(id))
	if err != nil {
		t.Fatalf("load_artifact %s: %v", id, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("load_artifact %s: HTTP %d: %s", id, resp.StatusCode, body)
	}
	var out struct {
		ContentHash string `json:"content_hash"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode load_artifact %s: %v", id, err)
	}
	return out.ContentHash
}
