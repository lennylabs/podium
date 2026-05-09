package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Ollama is the §4.7 self-hosted embeddings provider. Points at any
// Ollama endpoint; defaults to `http://localhost:11434` with model
// `nomic-embed-text` (768 dim). Recommended for offline / air-gapped
// deployments where no cloud provider is acceptable.
//
// Wire format: POST <BaseURL>/api/embeddings with body
// `{"model":"<model>","prompt":"<text>"}`. Ollama processes one text
// per call; the provider issues N HTTP calls for a batch of N texts.
// This is ergonomically slower than batch APIs but keeps the wire
// format simple and matches what Ollama itself supports.
type Ollama struct {
	BaseURL string
	Model_  string
	Dim     int
	Client  *http.Client
}

// ID returns "ollama".
func (Ollama) ID() string { return "ollama" }

// Model returns the configured model, defaulting to nomic-embed-text.
func (p Ollama) Model() string {
	if p.Model_ != "" {
		return p.Model_
	}
	return "nomic-embed-text"
}

// Dimensions returns the configured dimension; defaults vary by
// model (nomic-embed-text = 768, mxbai-embed-large = 1024).
func (p Ollama) Dimensions() int {
	if p.Dim > 0 {
		return p.Dim
	}
	switch p.Model() {
	case "mxbai-embed-large":
		return 1024
	case "all-minilm":
		return 384
	default:
		return 768
	}
}

func (p Ollama) baseURL() string {
	if p.BaseURL != "" {
		return strings.TrimRight(p.BaseURL, "/")
	}
	return "http://localhost:11434"
}

func (p Ollama) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return httpClient
}

// Embed serially calls the Ollama endpoint once per text. Errors
// short-circuit the loop so the caller never sees a partial result.
func (p Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, ErrEmptyTexts
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := p.embedOne(ctx, text)
		if err != nil {
			return nil, err
		}
		out[i] = vec
	}
	return out, nil
}

func (p Ollama) embedOne(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(map[string]any{
		"model":  p.Model(),
		"prompt": text,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL()+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		buf, _ := io.ReadAll(resp.Body)
		return nil, classify(resp.StatusCode, string(buf))
	}
	var parsed struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("ollama: decode: %w", err)
	}
	if len(parsed.Embedding) == 0 {
		return nil, fmt.Errorf("ollama: empty embedding")
	}
	return parsed.Embedding, nil
}
