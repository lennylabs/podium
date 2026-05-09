package embedding_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/embedding"
)

// indexedEmbeddingMock returns a server that replays a deterministic
// embedding for each text. The shape matches OpenAI / Voyage's
// {data: [{embedding, index}]} wire format.
func indexedEmbeddingMock(t *testing.T, dim int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out := struct {
			Data []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			} `json:"data"`
		}{}
		for i := range req.Input {
			vec := make([]float32, dim)
			vec[0] = float32(i + 1)
			out.Data = append(out.Data, struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{vec, i})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
}

// Spec: §4.7 — OpenAI provider returns one vector per input,
// preserving order via the response's `index` field.
// Phase: 5
func TestOpenAI_EmbedRoundTrip(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	srv := indexedEmbeddingMock(t, 8)
	t.Cleanup(srv.Close)
	p := embedding.OpenAI{
		APIKey: "sk-test", BaseURL: srv.URL, Dim: 8, Client: srv.Client(),
	}
	vecs, err := p.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("got %d vecs, want 3", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 8 {
			t.Errorf("[%d] dim = %d, want 8", i, len(v))
		}
		if v[0] != float32(i+1) {
			t.Errorf("[%d] index marker = %v, want %d", i, v[0], i+1)
		}
	}
}

// Spec: §6.10 — auth failures map to ErrAuth so callers can
// distinguish between "provider unreachable" and "bad credentials."
// Phase: 5
func TestOpenAI_AuthFailureMapsToErrAuth(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad token", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	p := embedding.OpenAI{
		APIKey: "bad", BaseURL: srv.URL, Client: srv.Client(),
	}
	_, err := p.Embed(context.Background(), []string{"x"})
	if !errors.Is(err, embedding.ErrAuth) {
		t.Fatalf("got %v, want ErrAuth", err)
	}
}

// Spec: §4.7 — quota / rate-limit responses map to ErrQuota.
// Phase: 5
func TestOpenAI_QuotaMapsToErrQuota(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	p := embedding.OpenAI{APIKey: "k", BaseURL: srv.URL, Client: srv.Client()}
	_, err := p.Embed(context.Background(), []string{"x"})
	if !errors.Is(err, embedding.ErrQuota) {
		t.Fatalf("got %v, want ErrQuota", err)
	}
}

// Spec: §4.7 — Voyage uses the same response shape; the same
// indexed-mock fixture exercises the parsing path.
// Phase: 5
func TestVoyage_EmbedRoundTrip(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	srv := indexedEmbeddingMock(t, 4)
	t.Cleanup(srv.Close)
	p := embedding.Voyage{
		APIKey: "voyage-test", BaseURL: srv.URL, Dim: 4, Client: srv.Client(),
	}
	vecs, err := p.Embed(context.Background(), []string{"x", "y"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 4 {
		t.Errorf("unexpected shape: %v", vecs)
	}
}

// Spec: §4.7 — Cohere accepts both the legacy top-level array shape
// and the newer `embeddings.float` shape; the provider parses both.
// Phase: 5
func TestCohere_EmbedHandlesBothResponseShapes(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	for _, shape := range []string{"legacy", "v2"} {
		shape := shape
		t.Run(shape, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Texts []string `json:"texts"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				vec := []float64{0.1, 0.2, 0.3, 0.4}
				rows := make([]any, len(req.Texts))
				for i := range req.Texts {
					rows[i] = vec
				}
				if shape == "legacy" {
					_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": rows})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": map[string]any{"float": rows}})
				}
			}))
			t.Cleanup(srv.Close)
			p := embedding.Cohere{APIKey: "k", BaseURL: srv.URL, Dim: 4, Client: srv.Client()}
			vecs, err := p.Embed(context.Background(), []string{"a", "b"})
			if err != nil {
				t.Fatalf("Embed: %v", err)
			}
			if len(vecs) != 2 || len(vecs[0]) != 4 {
				t.Errorf("unexpected shape: %v", vecs)
			}
		})
	}
}

// Spec: §4.7 — Ollama serializes one text per call; the provider
// issues N HTTP calls for a batch of N texts and stitches results.
// Phase: 5
func TestOllama_EmbedRoundTrip(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding": []float32{float32(calls), 2.0, 3.0},
		})
	}))
	t.Cleanup(srv.Close)
	p := embedding.Ollama{BaseURL: srv.URL, Dim: 3, Client: srv.Client()}
	vecs, err := p.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 3 || len(vecs[0]) != 3 {
		t.Errorf("unexpected shape: %v", vecs)
	}
	if calls != 3 {
		t.Errorf("HTTP calls = %d, want 3", calls)
	}
}

// Spec: §4.7 — empty input returns ErrEmptyTexts before any HTTP
// call is made; useful for the ingest path's "no document text"
// short-circuit.
// Phase: 5
func TestProviders_RejectEmptyInput(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	for _, p := range []embedding.Provider{
		embedding.OpenAI{}, embedding.Voyage{},
		embedding.Cohere{}, embedding.Ollama{},
	} {
		_, err := p.Embed(context.Background(), nil)
		if !errors.Is(err, embedding.ErrEmptyTexts) {
			t.Errorf("%s: got %v, want ErrEmptyTexts", p.ID(), err)
		}
	}
}
