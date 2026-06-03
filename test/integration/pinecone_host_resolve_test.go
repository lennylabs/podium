package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

// Spec: §13.12 (F-13.12.3) — PODIUM_PINECONE_HOST defaults to "auto-resolved
// from index name". This drives the full index-only path: the shared
// OpenBuiltin factory queries a mock Pinecone control plane to resolve the
// data-plane host from the index name, then the constructed backend upserts and
// queries against that resolved host. An index-only deployment is functional as
// the spec advertises rather than failing with a missing-host error.
func TestPineconeIndexOnly_ResolvesHostThenRoundTrips(t *testing.T) {
	t.Parallel()

	// Mock data plane: records upserts and echoes them back on /query.
	stored := map[string][]float32{}
	data := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vectors/upsert":
			var body struct {
				Vectors []struct {
					ID     string    `json:"id"`
					Values []float32 `json:"values"`
				} `json:"vectors"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			for _, v := range body.Vectors {
				stored[v.ID] = v.Values
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"upsertedCount": len(body.Vectors)})
		case "/query":
			var body struct {
				TopK int `json:"topK"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			matches := []map[string]any{}
			for id := range stored {
				matches = append(matches, map[string]any{
					"id":       id,
					"score":    0.95,
					"metadata": map[string]string{"artifact_id": "a", "version": "1.0.0"},
				})
				if len(matches) >= body.TopK {
					break
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"matches": matches})
		default:
			http.Error(w, "unhandled path", http.StatusNotFound)
		}
	}))
	t.Cleanup(data.Close)

	// Mock control plane: resolves the index name to the data-plane host.
	var resolvedIndex string
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolvedIndex = r.URL.Path // /indexes/{name}
		_ = json.NewEncoder(w).Encode(map[string]any{"host": data.URL})
	}))
	t.Cleanup(control.Close)

	// Index-only config (no PineconeHost): OpenBuiltin must resolve it.
	v, err := vector.OpenBuiltin("pinecone", vector.BackendConfig{
		PineconeKey:          "k",
		PineconeIndex:        "acme-prod",
		PineconeControlPlane: control.URL,
	}, 4)
	if err != nil {
		t.Fatalf("OpenBuiltin(index-only pinecone) = %v, want a resolved backend", err)
	}
	if resolvedIndex != "/indexes/acme-prod" {
		t.Errorf("control-plane describe-index path = %q, want /indexes/acme-prod", resolvedIndex)
	}

	ctx := context.Background()
	if err := v.Put(ctx, "t1", "a", "1.0.0", []float32{0.1, 0.2, 0.3, 0.4}); err != nil {
		t.Fatalf("Put against the resolved host: %v", err)
	}
	matches, err := v.Query(ctx, "t1", []float32{0.1, 0.2, 0.3, 0.4}, 5)
	if err != nil {
		t.Fatalf("Query against the resolved host: %v", err)
	}
	if len(matches) != 1 || matches[0].ArtifactID != "a" {
		t.Errorf("matches = %v, want one match for artifact a", matches)
	}
}
