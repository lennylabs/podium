package vector_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

func TestMemory_BasicGettersAndClose(t *testing.T) {
	t.Parallel()
	m := vector.NewMemory(8)
	if m.ID() != "memory" {
		t.Errorf("ID = %q", m.ID())
	}
	if m.Dimensions() != 8 {
		t.Errorf("Dimensions = %d", m.Dimensions())
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestPinecone_GettersAndConstructorValidation(t *testing.T) {
	t.Parallel()
	if _, err := vector.NewPinecone(vector.PineconeConfig{}); err == nil ||
		!strings.Contains(err.Error(), "APIKey") {
		t.Errorf("missing APIKey: err = %v", err)
	}
	if _, err := vector.NewPinecone(vector.PineconeConfig{APIKey: "k"}); err == nil ||
		!strings.Contains(err.Error(), "Host") {
		t.Errorf("missing Host: err = %v", err)
	}
	if _, err := vector.NewPinecone(vector.PineconeConfig{APIKey: "k", Host: "https://h"}); err == nil ||
		!strings.Contains(err.Error(), "Dimensions") {
		t.Errorf("missing Dimensions: err = %v", err)
	}
	p, err := vector.NewPinecone(vector.PineconeConfig{APIKey: "k", Host: "https://h/", Dimensions: 4})
	if err != nil {
		t.Fatalf("NewPinecone: %v", err)
	}
	if p.ID() != "pinecone" {
		t.Errorf("ID = %q", p.ID())
	}
	if p.Dimensions() != 4 {
		t.Errorf("Dimensions = %d", p.Dimensions())
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestQdrant_GettersAndConstructorValidation(t *testing.T) {
	t.Parallel()
	if _, err := vector.NewQdrant(vector.QdrantConfig{}); err == nil ||
		!strings.Contains(err.Error(), "URL") {
		t.Errorf("missing URL: err = %v", err)
	}
	if _, err := vector.NewQdrant(vector.QdrantConfig{URL: "https://h"}); err == nil ||
		!strings.Contains(err.Error(), "Collection") {
		t.Errorf("missing Collection: err = %v", err)
	}
	if _, err := vector.NewQdrant(vector.QdrantConfig{URL: "https://h", Collection: "c"}); err == nil ||
		!strings.Contains(err.Error(), "Dimensions") {
		t.Errorf("missing Dimensions: err = %v", err)
	}
	q, err := vector.NewQdrant(vector.QdrantConfig{URL: "https://h/", Collection: "c", Dimensions: 4})
	if err != nil {
		t.Fatalf("NewQdrant: %v", err)
	}
	if q.ID() != "qdrant-cloud" {
		t.Errorf("ID = %q", q.ID())
	}
	if q.Dimensions() != 4 {
		t.Errorf("Dimensions = %d", q.Dimensions())
	}
	if err := q.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestWeaviate_GettersAndConstructorValidation(t *testing.T) {
	t.Parallel()
	if _, err := vector.NewWeaviate(vector.WeaviateConfig{}); err == nil ||
		!strings.Contains(err.Error(), "URL") {
		t.Errorf("missing URL: err = %v", err)
	}
	if _, err := vector.NewWeaviate(vector.WeaviateConfig{URL: "https://h"}); err == nil ||
		!strings.Contains(err.Error(), "Collection") {
		t.Errorf("missing Collection: err = %v", err)
	}
	if _, err := vector.NewWeaviate(vector.WeaviateConfig{URL: "https://h", Collection: "c"}); err == nil ||
		!strings.Contains(err.Error(), "Dimensions") {
		t.Errorf("missing Dimensions: err = %v", err)
	}
	w, err := vector.NewWeaviate(vector.WeaviateConfig{URL: "https://h/", Collection: "c", Dimensions: 4})
	if err != nil {
		t.Fatalf("NewWeaviate: %v", err)
	}
	if w.ID() != "weaviate-cloud" {
		t.Errorf("ID = %q", w.ID())
	}
	if w.Dimensions() != 4 {
		t.Errorf("Dimensions = %d", w.Dimensions())
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestPinecone_DeleteIssuesPostToVectorsDelete(t *testing.T) {
	t.Parallel()
	gotPath := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	p, _ := vector.NewPinecone(vector.PineconeConfig{
		APIKey: "k", Host: srv.URL, Dimensions: 4, Namespace: "global",
	})
	p.Client = srv.Client()
	if err := p.Delete(context.Background(), "tenant-1", "art", "1.0.0"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if !strings.Contains(gotPath, "/vectors/delete") {
		t.Errorf("path = %q", gotPath)
	}
}

func TestQdrant_DeleteIssuesPostWithFilter(t *testing.T) {
	t.Parallel()
	gotPath := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	q, _ := vector.NewQdrant(vector.QdrantConfig{
		URL: srv.URL, Collection: "podium", Dimensions: 4,
	})
	q.Client = srv.Client()
	if err := q.Delete(context.Background(), "tenant-1", "art", "1.0.0"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if !strings.Contains(gotPath, "podium") {
		t.Errorf("path = %q", gotPath)
	}
}

func TestWeaviate_DeleteIssuesPostWithFilter(t *testing.T) {
	t.Parallel()
	gotPath := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	w, _ := vector.NewWeaviate(vector.WeaviateConfig{
		URL: srv.URL, Collection: "Podium", Dimensions: 4,
	})
	w.Client = srv.Client()
	if err := w.Delete(context.Background(), "tenant-1", "art", "1.0.0"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if gotPath == "" {
		t.Errorf("Delete did not issue a request")
	}
}
