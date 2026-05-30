package vector_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

// Spec: §13.12 (F-13.12.6) — vector.SelfEmbeds reports server-side embedding
// only for a backend configured with an inference model / vectorizer. Nil and
// the non-self-embedding backends report false.
func TestSelfEmbeds_Helper(t *testing.T) {
	t.Parallel()
	if vector.SelfEmbeds(nil) {
		t.Error("SelfEmbeds(nil) = true, want false")
	}
	if vector.SelfEmbeds(vector.NewMemory(8)) {
		t.Error("SelfEmbeds(memory) = true, want false (memory cannot self-embed)")
	}
	storageOnly, _ := vector.NewPinecone(vector.PineconeConfig{APIKey: "k", Host: "https://h", Dimensions: 4})
	if vector.SelfEmbeds(storageOnly) {
		t.Error("SelfEmbeds(pinecone storage-only) = true, want false")
	}
	selfEmbed, _ := vector.NewPinecone(vector.PineconeConfig{APIKey: "k", Host: "https://h", InferenceModel: "multilingual-e5-large"})
	if !vector.SelfEmbeds(selfEmbed) {
		t.Error("SelfEmbeds(pinecone + inference model) = false, want true")
	}
}

// Spec: §13.12 (F-13.12.6) — with Integrated Inference / a vectorizer the
// hosted model fixes the dimension, so a self-embedding backend constructs
// with Dimensions 0; a storage-only backend still requires a positive value.
func TestSelfEmbedding_DimensionOptional(t *testing.T) {
	t.Parallel()
	if _, err := vector.NewPinecone(vector.PineconeConfig{APIKey: "k", Host: "https://h", InferenceModel: "m"}); err != nil {
		t.Errorf("NewPinecone(dim=0, inference) = %v, want nil", err)
	}
	if _, err := vector.NewPinecone(vector.PineconeConfig{APIKey: "k", Host: "https://h"}); err == nil {
		t.Error("NewPinecone(dim=0, storage-only) = nil, want dimension error")
	}
	if _, err := vector.NewWeaviate(vector.WeaviateConfig{URL: "https://h", Collection: "c", Vectorizer: "text2vec-weaviate"}); err != nil {
		t.Errorf("NewWeaviate(dim=0, vectorizer) = %v, want nil", err)
	}
	if _, err := vector.NewWeaviate(vector.WeaviateConfig{URL: "https://h", Collection: "c"}); err == nil {
		t.Error("NewWeaviate(dim=0, storage-only) = nil, want dimension error")
	}
	if _, err := vector.NewQdrant(vector.QdrantConfig{URL: "https://h", Collection: "c", InferenceModel: "m"}); err != nil {
		t.Errorf("NewQdrant(dim=0, inference) = %v, want nil", err)
	}
	if _, err := vector.NewQdrant(vector.QdrantConfig{URL: "https://h", Collection: "c"}); err == nil {
		t.Error("NewQdrant(dim=0, storage-only) = nil, want dimension error")
	}
}

// Spec: §13.12 (F-13.12.6) — Pinecone Integrated Inference: PutText posts raw
// text (chunk_text, no vector) to the records upsert endpoint, and QueryText
// posts a text query to the records search endpoint and unpacks the hits.
func TestPinecone_PutTextAndQueryText(t *testing.T) {
	t.Parallel()
	var upsertBody, searchBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/upsert"):
			upsertBody = string(raw)
			_ = json.NewEncoder(w).Encode(map[string]any{"upsertedCount": 1})
		case strings.HasSuffix(r.URL.Path, "/search"):
			searchBody = string(raw)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"hits": []map[string]any{{
					"_id":    "a@1.0.0",
					"_score": 0.95,
					"fields": map[string]string{"artifact_id": "a", "version": "1.0.0"},
				}}},
			})
		default:
			http.Error(w, "unhandled path: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	p, err := vector.NewPinecone(vector.PineconeConfig{
		APIKey: "k", Host: srv.URL, InferenceModel: "multilingual-e5-large",
	})
	if err != nil {
		t.Fatalf("NewPinecone: %v", err)
	}
	p.Client = srv.Client()
	if err := p.PutText(context.Background(), "t1", "a", "1.0.0", "hello world"); err != nil {
		t.Fatalf("PutText: %v", err)
	}
	// The records upsert carries the raw text in chunk_text and no vector.
	if !strings.Contains(upsertBody, "hello world") || !strings.Contains(upsertBody, "chunk_text") {
		t.Errorf("upsert body missing chunk_text/text: %s", upsertBody)
	}
	if strings.Contains(upsertBody, "values") {
		t.Errorf("self-embedding upsert must not send a vector: %s", upsertBody)
	}
	matches, err := p.QueryText(context.Background(), "t1", "find it", 5)
	if err != nil {
		t.Fatalf("QueryText: %v", err)
	}
	if !strings.Contains(searchBody, "find it") {
		t.Errorf("search body missing query text: %s", searchBody)
	}
	if len(matches) != 1 || matches[0].ArtifactID != "a" {
		t.Fatalf("got %v, want one match for a", matches)
	}
	if matches[0].Distance < 0.04 || matches[0].Distance > 0.06 {
		t.Errorf("distance = %v, want ~0.05 (1 - 0.95)", matches[0].Distance)
	}
}

// Spec: §13.12 (F-13.12.6) — Weaviate vectorizer: PutText writes an object
// with the text in the vectorized `content` property and no explicit vector;
// QueryText runs a nearText GraphQL search.
func TestWeaviate_PutTextAndQueryText(t *testing.T) {
	t.Parallel()
	var putBody, graphqlBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.Method == http.MethodPut {
			putBody = string(raw)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "ack"})
			return
		}
		if r.URL.Path == "/v1/graphql" {
			graphqlBody = string(raw)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"Get": map[string][]map[string]any{
					"ArtifactVec": {{
						"artifactId":  "a",
						"version":     "1.0.0",
						"_additional": map[string]any{"distance": 0.07},
					}},
				}},
			})
			return
		}
		http.Error(w, "unhandled path", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	wv, err := vector.NewWeaviate(vector.WeaviateConfig{
		URL: srv.URL, Collection: "ArtifactVec", Vectorizer: "text2vec-weaviate",
	})
	if err != nil {
		t.Fatalf("NewWeaviate: %v", err)
	}
	wv.Client = srv.Client()
	if err := wv.PutText(context.Background(), "t1", "a", "1.0.0", "hello world"); err != nil {
		t.Fatalf("PutText: %v", err)
	}
	if !strings.Contains(putBody, "content") || !strings.Contains(putBody, "hello world") {
		t.Errorf("put body missing content text: %s", putBody)
	}
	if strings.Contains(putBody, "\"vector\"") {
		t.Errorf("self-embedding put must not send a vector: %s", putBody)
	}
	matches, err := wv.QueryText(context.Background(), "t1", "find it", 5)
	if err != nil {
		t.Fatalf("QueryText: %v", err)
	}
	if !strings.Contains(graphqlBody, "nearText") || !strings.Contains(graphqlBody, "find it") {
		t.Errorf("graphql body should use nearText with the query: %s", graphqlBody)
	}
	if len(matches) != 1 || matches[0].ArtifactID != "a" || matches[0].Distance != 0.07 {
		t.Errorf("got %v, want one match for a at distance 0.07", matches)
	}
}

// Spec: §13.12 (F-13.12.6) — Qdrant Cloud Inference: PutText upserts a point
// whose vector is a {text, model} document; QueryText posts a document query
// to the Query API and unpacks result.points.
func TestQdrant_PutTextAndQueryText(t *testing.T) {
	t.Parallel()
	var putBody, queryBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPut:
			putBody = string(raw)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case strings.HasSuffix(r.URL.Path, "/points/query"):
			queryBody = string(raw)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"points": []map[string]any{{
					"score":   0.92,
					"payload": map[string]any{"artifact_id": "a", "version": "1.0.0"},
				}}},
			})
		default:
			http.Error(w, "unhandled path: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	qd, err := vector.NewQdrant(vector.QdrantConfig{
		URL: srv.URL, Collection: "c", InferenceModel: "bge-small-en",
	})
	if err != nil {
		t.Fatalf("NewQdrant: %v", err)
	}
	qd.Client = srv.Client()
	if err := qd.PutText(context.Background(), "t1", "a", "1.0.0", "hello world"); err != nil {
		t.Fatalf("PutText: %v", err)
	}
	if !strings.Contains(putBody, "hello world") || !strings.Contains(putBody, "bge-small-en") {
		t.Errorf("put body missing document text/model: %s", putBody)
	}
	matches, err := qd.QueryText(context.Background(), "t1", "find it", 5)
	if err != nil {
		t.Fatalf("QueryText: %v", err)
	}
	if !strings.Contains(queryBody, "find it") || !strings.Contains(queryBody, "bge-small-en") {
		t.Errorf("query body missing document text/model: %s", queryBody)
	}
	if len(matches) != 1 || matches[0].ArtifactID != "a" {
		t.Fatalf("got %v, want one match for a", matches)
	}
	if matches[0].Distance < 0.07 || matches[0].Distance > 0.09 {
		t.Errorf("distance = %v, want ~0.08 (1 - 0.92)", matches[0].Distance)
	}
}

// Spec: §13.12 (F-13.12.6) — a self-embedding backend rejects malformed input
// (empty tenant / non-positive top_k) the same way the vector path does.
func TestSelfEmbedding_InvalidArgument(t *testing.T) {
	t.Parallel()
	p, _ := vector.NewPinecone(vector.PineconeConfig{APIKey: "k", Host: "https://h", InferenceModel: "m"})
	if err := p.PutText(context.Background(), "", "a", "1", "x"); err != vector.ErrInvalidArgument {
		t.Errorf("PutText(empty tenant) = %v, want ErrInvalidArgument", err)
	}
	if _, err := p.QueryText(context.Background(), "t1", "x", 0); err != vector.ErrInvalidArgument {
		t.Errorf("QueryText(top_k=0) = %v, want ErrInvalidArgument", err)
	}
}
