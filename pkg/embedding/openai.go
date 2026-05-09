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

// OpenAI is the §4.7 OpenAI embeddings provider. Defaults to
// `text-embedding-3-small` (1536 dim); set Model to override.
//
// Wire format: POST <BaseURL>/embeddings with body
// `{"model":"<model>","input":[<texts>]}`. Response carries one
// `{embedding: [...float...], index: int}` per input.
type OpenAI struct {
	APIKey  string
	Model_  string
	Dim     int
	BaseURL string
	Org     string
	Client  *http.Client
}

// ID returns "openai".
func (OpenAI) ID() string { return "openai" }

// Model returns the configured model, defaulting to text-embedding-3-small.
func (p OpenAI) Model() string {
	if p.Model_ != "" {
		return p.Model_
	}
	return "text-embedding-3-small"
}

// Dimensions returns the configured dimension; defaults to 1536
// for text-embedding-3-small.
func (p OpenAI) Dimensions() int {
	if p.Dim > 0 {
		return p.Dim
	}
	switch p.Model() {
	case "text-embedding-3-large":
		return 3072
	default:
		return 1536
	}
}

func (p OpenAI) baseURL() string {
	if p.BaseURL != "" {
		return strings.TrimRight(p.BaseURL, "/")
	}
	return "https://api.openai.com/v1"
}

func (p OpenAI) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return httpClient
}

// Embed sends a batch of texts and returns the corresponding vectors
// in input order.
func (p OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, ErrEmptyTexts
	}
	body, err := json.Marshal(map[string]any{
		"model": p.Model(),
		"input": texts,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL()+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	if p.Org != "" {
		req.Header.Set("OpenAI-Organization", p.Org)
	}
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
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("openai: decode: %w", err)
	}
	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("openai: index out of range: %d", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("openai: missing embedding for index %d", i)
		}
	}
	return out, nil
}
