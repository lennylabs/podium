package vector

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §13.12 — PODIUM_PINECONE_HOST defaults to "auto-resolved
// from index name". ResolvePineconeHost queries the control-plane describe-index
// endpoint (GET /indexes/{name}, Api-Key header) and returns the `host` field.
func TestResolvePineconeHost_Success(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("Api-Key")
		w.Header().Set("Content-Type", "application/json")
		// Pinecone returns a bare host without a scheme.
		_, _ = w.Write([]byte(`{"name":"acme-prod","host":"acme-prod-abc123.svc.aped-4627-b74a.pinecone.io"}`))
	}))
	defer srv.Close()

	host, err := ResolvePineconeHost(context.Background(), srv.URL, "pcn-key", "acme-prod", srv.Client())
	if err != nil {
		t.Fatalf("ResolvePineconeHost: %v", err)
	}
	if gotPath != "/indexes/acme-prod" {
		t.Errorf("describe-index path = %q, want /indexes/acme-prod", gotPath)
	}
	if gotKey != "pcn-key" {
		t.Errorf("Api-Key header = %q, want pcn-key", gotKey)
	}
	// A bare host is normalized to an https URL for the data plane.
	if host != "https://acme-prod-abc123.svc.aped-4627-b74a.pinecone.io" {
		t.Errorf("host = %q, want the https-normalized data-plane host", host)
	}
}

// spec: §13.12 — a host that already carries a scheme is returned unchanged.
func TestResolvePineconeHost_SchemePreserved(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"host":"https://acme.pinecone.io"}`))
	}))
	defer srv.Close()
	host, err := ResolvePineconeHost(context.Background(), srv.URL, "k", "i", srv.Client())
	if err != nil || host != "https://acme.pinecone.io" {
		t.Fatalf("host = %q, err = %v; want https://acme.pinecone.io", host, err)
	}
}

// spec: §13.12 — a non-2xx control-plane response (e.g. unknown index / bad
// key) is wrapped with ErrUnreachable so the caller degrades to BM25.
func TestResolvePineconeHost_ControlPlaneError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"index not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := ResolvePineconeHost(context.Background(), srv.URL, "k", "missing", srv.Client())
	if err == nil {
		t.Fatal("want error for a 404 control-plane response")
	}
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}

// spec: §13.12 — a 2xx response that omits the host is an error rather than a
// silently empty host that NewPinecone would later reject.
func TestResolvePineconeHost_EmptyHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"acme-prod"}`))
	}))
	defer srv.Close()
	_, err := ResolvePineconeHost(context.Background(), srv.URL, "k", "acme-prod", srv.Client())
	if err == nil || !strings.Contains(err.Error(), "no host") {
		t.Errorf("err = %v, want a 'no host' error", err)
	}
}

// spec: §13.12 — the key and index are required inputs; missing either is an
// argument error before any request is made.
func TestResolvePineconeHost_RequiredInputs(t *testing.T) {
	if _, err := ResolvePineconeHost(context.Background(), "", "", "idx", nil); err == nil {
		t.Error("want error for missing API key")
	}
	if _, err := ResolvePineconeHost(context.Background(), "", "k", "", nil); err == nil {
		t.Error("want error for missing index")
	}
}

// spec: §6.4.1 / §13.12 — OpenBuiltin resolves the data-plane host
// from the index name when PineconeHost is empty, so an index-only deployment
// is functional as the spec advertises.
func TestOpenBuiltin_PineconeResolvesHostFromIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/indexes/acme-prod" {
			t.Errorf("path = %q, want /indexes/acme-prod", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"host":"https://acme-prod.svc.pinecone.io"}`))
	}))
	defer srv.Close()

	p, err := OpenBuiltin("pinecone", BackendConfig{
		PineconeKey:          "k",
		PineconeIndex:        "acme-prod",
		PineconeControlPlane: srv.URL,
	}, 4)
	if err != nil {
		t.Fatalf("OpenBuiltin(pinecone, index-only) = %v, want a resolved backend", err)
	}
	if p == nil || p.ID() != "pinecone" {
		t.Fatalf("provider = %v, want pinecone", p)
	}
	if pc, ok := p.(*Pinecone); !ok || pc.Host != "https://acme-prod.svc.pinecone.io" {
		t.Errorf("resolved Host = %v, want https://acme-prod.svc.pinecone.io", p)
	}
}

// spec: §13.12 — when the control plane cannot resolve the index,
// OpenBuiltin returns an error naming PODIUM_PINECONE_HOST so Run degrades to
// BM25 rather than starting with a half-configured backend.
func TestOpenBuiltin_PineconeIndexResolveFailureErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := OpenBuiltin("pinecone", BackendConfig{
		PineconeKey:          "k",
		PineconeIndex:        "missing",
		PineconeControlPlane: srv.URL,
	}, 4)
	if err == nil || !strings.Contains(err.Error(), "PODIUM_PINECONE_HOST") {
		t.Errorf("err = %v, want a PODIUM_PINECONE_HOST resolution error", err)
	}
}
