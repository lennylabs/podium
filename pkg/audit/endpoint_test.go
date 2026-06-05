package audit_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
)

// spec: §6.2 / §8.3 / §9 — PODIUM_AUDIT_SINK can name an external endpoint;
// the EndpointSink forwards each meta-tool event to it as JSON, carrying
// the §8.6 hash-chain fields.
func TestEndpointSink_ForwardsJSON(t *testing.T) {
	t.Parallel()
	var (
		mu     sync.Mutex
		bodies [][]byte
		ctypes []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, b)
		ctypes = append(ctypes, r.Header.Get("Content-Type"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink, err := audit.NewEndpointSink(srv.URL)
	if err != nil {
		t.Fatalf("NewEndpointSink: %v", err)
	}
	for _, target := range []string{"a", "b"} {
		if err := sink.Append(context.Background(), audit.Event{
			Type: audit.EventArtifactLoaded, Caller: "alice", Target: target,
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("got %d forwarded events, want 2", len(bodies))
	}
	if ctypes[0] != "application/json" {
		t.Errorf("content-type = %q, want application/json", ctypes[0])
	}
	type wire struct {
		Type     string `json:"type"`
		Hash     string `json:"hash"`
		PrevHash string `json:"prev_hash"`
	}
	var first, second wire
	if err := json.Unmarshal(bodies[0], &first); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}
	if err := json.Unmarshal(bodies[1], &second); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if first.Type != "artifact.loaded" {
		t.Errorf("type = %q, want artifact.loaded", first.Type)
	}
	if first.Hash == "" {
		t.Errorf("first event missing hash")
	}
	// §8.6 chain: the second event's prev_hash links to the first's hash.
	if second.PrevHash != first.Hash {
		t.Errorf("prev_hash = %q, want %q (chain link)", second.PrevHash, first.Hash)
	}
}

// spec: §6.2 — a non-http value is a filesystem path, not an endpoint;
// NewEndpointSink rejects it so a path is never POSTed somewhere.
func TestEndpointSink_RejectsNonHTTPScheme(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"/Users/alice/.podium/audit.log", "file:///tmp/x", "ftp://host/x"} {
		if _, err := audit.NewEndpointSink(v); err == nil {
			t.Errorf("NewEndpointSink(%q) = nil error, want rejection", v)
		}
	}
}

// spec: §8.3 — integrity of the forwarded stream is the aggregator's;
// Verify is a no-op on the local side.
func TestEndpointSink_VerifyNoop(t *testing.T) {
	t.Parallel()
	sink, err := audit.NewEndpointSink("https://siem.acme.com/ingest")
	if err != nil {
		t.Fatalf("NewEndpointSink: %v", err)
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Errorf("Verify = %v, want nil", err)
	}
}

// A non-2xx response surfaces as an error so callers can log a forwarding
// failure (the MCP server swallows it; the registry stream stays
// authoritative).
func TestEndpointSink_Non2xxReturnsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	sink, err := audit.NewEndpointSink(srv.URL)
	if err != nil {
		t.Fatalf("NewEndpointSink: %v", err)
	}
	err = sink.Append(context.Background(), audit.Event{Type: audit.EventDomainLoaded})
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Errorf("Append err = %v, want status 500", err)
	}
}
