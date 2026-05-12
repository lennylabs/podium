package embedding_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/embedding"
)

func TestProviderIDs(t *testing.T) {
	t.Parallel()
	cases := map[string]interface{ ID() string }{
		"openai": embedding.OpenAI{},
		"cohere": embedding.Cohere{},
		"voyage": embedding.Voyage{},
		"ollama": embedding.Ollama{},
	}
	for want, p := range cases {
		if got := p.ID(); got != want {
			t.Errorf("%T.ID() = %q, want %q", p, got, want)
		}
	}
}

func TestProviderDimensionsDefaults(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		p    interface{ Dimensions() int }
		want int
	}{
		{"openai default", embedding.OpenAI{}, 1536},
		{"openai large", embedding.OpenAI{Model_: "text-embedding-3-large"}, 3072},
		{"openai override", embedding.OpenAI{Dim: 99}, 99},
		{"cohere default", embedding.Cohere{}, 1024},
		{"cohere light", embedding.Cohere{Model_: "embed-english-light-v3.0"}, 384},
		{"cohere override", embedding.Cohere{Dim: 77}, 77},
		{"voyage default", embedding.Voyage{}, 1024},
		{"voyage large-2", embedding.Voyage{Model_: "voyage-large-2"}, 1536},
		{"voyage code-3", embedding.Voyage{Model_: "voyage-code-3"}, 1024},
		{"voyage override", embedding.Voyage{Dim: 55}, 55},
		{"ollama default", embedding.Ollama{}, 768},
		{"ollama mxbai", embedding.Ollama{Model_: "mxbai-embed-large"}, 1024},
		{"ollama minilm", embedding.Ollama{Model_: "all-minilm"}, 384},
		{"ollama override", embedding.Ollama{Dim: 333}, 333},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.Dimensions(); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestOpenAI_EmbedHappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		// Return embeddings out of order to exercise the index reorder.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 1, "embedding": []float32{0.4, 0.5, 0.6}},
				{"index": 0, "embedding": []float32{0.1, 0.2, 0.3}},
			},
		})
	}))
	defer srv.Close()
	p := embedding.OpenAI{APIKey: "k", BaseURL: srv.URL, Org: "o", Client: srv.Client()}
	vecs, err := p.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || vecs[0][0] != 0.1 || vecs[1][2] != 0.6 {
		t.Errorf("Embed = %+v", vecs)
	}
}

func TestOpenAI_EmptyTextsErrors(t *testing.T) {
	t.Parallel()
	if _, err := (embedding.OpenAI{}).Embed(context.Background(), nil); !errors.Is(err, embedding.ErrEmptyTexts) {
		t.Errorf("Embed(nil) err = %v, want ErrEmptyTexts", err)
	}
}

func TestOpenAI_UnreachableEndpoint(t *testing.T) {
	t.Parallel()
	// 127.0.0.1:1 is reliably unreachable.
	p := embedding.OpenAI{APIKey: "k", BaseURL: "http://127.0.0.1:1", Client: &http.Client{}}
	if _, err := p.Embed(context.Background(), []string{"a"}); !errors.Is(err, embedding.ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}

func TestOpenAI_NonOKReturnsClassifiedError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate-limited"}`))
	}))
	defer srv.Close()
	p := embedding.OpenAI{BaseURL: srv.URL, Client: srv.Client()}
	_, err := p.Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Errorf("expected an error")
	}
}

func TestCohere_EmbedNewShape(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": map[string]any{
				"float": [][]float64{{0.1, 0.2}, {0.3, 0.4}},
			},
		})
	}))
	defer srv.Close()
	p := embedding.Cohere{BaseURL: srv.URL, Client: srv.Client()}
	vecs, err := p.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || vecs[1][0] != 0.3 {
		t.Errorf("Embed = %+v", vecs)
	}
}

func TestCohere_EmbedLegacyShape(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float64{{0.5, 0.6}},
		})
	}))
	defer srv.Close()
	p := embedding.Cohere{BaseURL: srv.URL, Client: srv.Client()}
	vecs, err := p.Embed(context.Background(), []string{"a"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if vecs[0][1] != 0.6 {
		t.Errorf("vecs[0] = %v", vecs[0])
	}
}

func TestCohere_EmptyTextsErrors(t *testing.T) {
	t.Parallel()
	if _, err := (embedding.Cohere{}).Embed(context.Background(), nil); !errors.Is(err, embedding.ErrEmptyTexts) {
		t.Errorf("err = %v", err)
	}
}

func TestVoyage_EmbedHappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 0, "embedding": []float32{0.1}},
				{"index": 1, "embedding": []float32{0.2}},
			},
		})
	}))
	defer srv.Close()
	p := embedding.Voyage{APIKey: "k", BaseURL: srv.URL, Client: srv.Client()}
	vecs, err := p.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || vecs[1][0] != 0.2 {
		t.Errorf("vecs = %+v", vecs)
	}
}

func TestVoyage_EmptyTextsErrors(t *testing.T) {
	t.Parallel()
	if _, err := (embedding.Voyage{}).Embed(context.Background(), nil); !errors.Is(err, embedding.ErrEmptyTexts) {
		t.Errorf("err = %v", err)
	}
}

func TestVoyage_MissingIndexErrors(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 0, "embedding": []float32{0.1}},
			},
		})
	}))
	defer srv.Close()
	p := embedding.Voyage{BaseURL: srv.URL, Client: srv.Client()}
	if _, err := p.Embed(context.Background(), []string{"a", "b"}); err == nil ||
		!strings.Contains(err.Error(), "missing embedding") {
		t.Errorf("err = %v", err)
	}
}

func TestOllama_EmbedHappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding": []float32{0.7, 0.8, 0.9},
		})
	}))
	defer srv.Close()
	p := embedding.Ollama{BaseURL: srv.URL, Client: srv.Client()}
	vecs, err := p.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || vecs[1][2] != 0.9 {
		t.Errorf("vecs = %+v", vecs)
	}
}

func TestOllama_EmptyVectorErrors(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": []float32{}})
	}))
	defer srv.Close()
	p := embedding.Ollama{BaseURL: srv.URL, Client: srv.Client()}
	if _, err := p.Embed(context.Background(), []string{"a"}); err == nil ||
		!strings.Contains(err.Error(), "empty embedding") {
		t.Errorf("err = %v", err)
	}
}

func TestOllama_EmptyTextsErrors(t *testing.T) {
	t.Parallel()
	if _, err := (embedding.Ollama{}).Embed(context.Background(), nil); !errors.Is(err, embedding.ErrEmptyTexts) {
		t.Errorf("err = %v", err)
	}
}
