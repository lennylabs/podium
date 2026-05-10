package vector_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

// Spec: §4.7 — Pinecone REST wire format: /vectors/upsert and /query
// roundtrip a vector preserving artifact_id and version metadata.
// Tier 1 mocks the upstream; Tier 2 against a live index lives in
// pinecone_live_test.go (env-gated).
func TestPinecone_PutAndQuery(t *testing.T) {
	t.Parallel()
	stored := map[string][]float32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					"id":    id,
					"score": 0.95,
					"metadata": map[string]string{
						"artifact_id": "a",
						"version":     "1.0.0",
					},
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
	t.Cleanup(srv.Close)
	p, err := vector.NewPinecone(vector.PineconeConfig{
		APIKey: "k", Host: srv.URL, Dimensions: 4,
	})
	if err != nil {
		t.Fatalf("NewPinecone: %v", err)
	}
	p.Client = srv.Client()
	if err := p.Put(context.Background(), "t1", "a", "1.0.0", []float32{0.1, 0.2, 0.3, 0.4}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	matches, err := p.Query(context.Background(), "t1", []float32{0.1, 0.2, 0.3, 0.4}, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) != 1 || matches[0].ArtifactID != "a" {
		t.Errorf("got %v, want one match for a", matches)
	}
	// Pinecone returns similarity 0.95; the SPI converts to distance 0.05.
	if matches[0].Distance < 0.04 || matches[0].Distance > 0.06 {
		t.Errorf("distance = %v, want ~0.05", matches[0].Distance)
	}
}

// Spec: §4.7 — Weaviate GraphQL nearVector roundtrip preserves
// artifactId/version metadata and converts the GraphQL distance
// directly to SPI distance (Weaviate already returns cosine
// distance, not similarity).
func TestWeaviate_PutAndQuery(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			// Object upsert; just acknowledge.
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "ack"})
			return
		}
		if r.URL.Path == "/v1/graphql" {
			resp := map[string]any{
				"data": map[string]any{
					"Get": map[string][]map[string]any{
						"ArtifactVec": {{
							"artifactId":  "a",
							"version":     "1.0.0",
							"_additional": map[string]any{"distance": 0.07},
						}},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.Error(w, "unhandled path", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	wv, err := vector.NewWeaviate(vector.WeaviateConfig{
		URL: srv.URL, Collection: "ArtifactVec", Dimensions: 4,
	})
	if err != nil {
		t.Fatalf("NewWeaviate: %v", err)
	}
	wv.Client = srv.Client()
	if err := wv.Put(context.Background(), "t1", "a", "1.0.0", []float32{0.1, 0.2, 0.3, 0.4}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	matches, err := wv.Query(context.Background(), "t1", []float32{0.1, 0.2, 0.3, 0.4}, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) != 1 || matches[0].ArtifactID != "a" {
		t.Errorf("got %v, want one match for a", matches)
	}
	if matches[0].Distance != 0.07 {
		t.Errorf("distance = %v, want 0.07", matches[0].Distance)
	}
}

// Spec: §4.7 — Qdrant REST upsert + search roundtrip preserving
// payload metadata. Qdrant returns cosine similarity; SPI converts.
func TestQdrant_PutAndQuery(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut:
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case r.URL.Path == "/collections/c/points/search":
			resp := map[string]any{
				"result": []map[string]any{{
					"score": 0.92,
					"payload": map[string]any{
						"artifact_id": "a",
						"version":     "1.0.0",
					},
				}},
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.Error(w, "unhandled path", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	qd, err := vector.NewQdrant(vector.QdrantConfig{
		URL: srv.URL, Collection: "c", Dimensions: 4,
	})
	if err != nil {
		t.Fatalf("NewQdrant: %v", err)
	}
	qd.Client = srv.Client()
	if err := qd.Put(context.Background(), "t1", "a", "1.0.0", []float32{0.1, 0.2, 0.3, 0.4}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	matches, err := qd.Query(context.Background(), "t1", []float32{0.1, 0.2, 0.3, 0.4}, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) != 1 || matches[0].ArtifactID != "a" {
		t.Errorf("got %v, want one match for a", matches)
	}
	// Qdrant similarity 0.92 → distance 0.08.
	if matches[0].Distance < 0.07 || matches[0].Distance > 0.09 {
		t.Errorf("distance = %v, want ~0.08", matches[0].Distance)
	}
}
