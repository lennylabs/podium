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

// Cohere is the §4.7 Cohere embeddings provider. Defaults to
// `embed-v4` (1024 dim); set Model to override.
//
// Wire format: POST <BaseURL>/embed with body
// `{"model":"<model>","texts":[...],"input_type":"search_document"}`.
type Cohere struct {
	APIKey  string
	Model_  string
	Dim     int
	BaseURL string
	Client  *http.Client
}

// ID returns "cohere".
func (Cohere) ID() string { return "cohere" }

// Model returns the configured model, defaulting to embed-v4.
func (p Cohere) Model() string {
	if p.Model_ != "" {
		return p.Model_
	}
	return "embed-v4"
}

// Dimensions returns the configured dimension; defaults vary by
// model (embed-v4 = 1024).
func (p Cohere) Dimensions() int {
	if p.Dim > 0 {
		return p.Dim
	}
	switch p.Model() {
	case "embed-english-light-v3.0", "embed-multilingual-light-v3.0":
		return 384
	default:
		return 1024
	}
}

func (p Cohere) baseURL() string {
	if p.BaseURL != "" {
		return strings.TrimRight(p.BaseURL, "/")
	}
	return "https://api.cohere.com/v1"
}

func (p Cohere) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return httpClient
}

// Embed sends a batch of texts and returns vectors in input order.
func (p Cohere) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, ErrEmptyTexts
	}
	body, err := json.Marshal(map[string]any{
		"model":      p.Model(),
		"texts":      texts,
		"input_type": "search_document",
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL()+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		buf, _ := io.ReadAll(resp.Body)
		return nil, classify(resp.StatusCode, string(buf))
	}
	// Cohere v1 returns embeddings as a top-level array (or under
	// embeddings.float in newer responses). Handle both shapes.
	var parsed struct {
		Embeddings any `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("cohere: decode: %w", err)
	}
	switch v := parsed.Embeddings.(type) {
	case []any:
		out := make([][]float32, 0, len(v))
		for _, row := range v {
			arr, ok := row.([]any)
			if !ok {
				return nil, fmt.Errorf("cohere: unexpected embeddings shape")
			}
			vec := make([]float32, len(arr))
			for i, x := range arr {
				f, ok := x.(float64)
				if !ok {
					return nil, fmt.Errorf("cohere: non-float in embedding")
				}
				vec[i] = float32(f)
			}
			out = append(out, vec)
		}
		if len(out) != len(texts) {
			return nil, fmt.Errorf("cohere: got %d embeddings, want %d", len(out), len(texts))
		}
		return out, nil
	case map[string]any:
		raw, ok := v["float"].([]any)
		if !ok {
			return nil, fmt.Errorf("cohere: missing embeddings.float")
		}
		out := make([][]float32, 0, len(raw))
		for _, row := range raw {
			arr, ok := row.([]any)
			if !ok {
				return nil, fmt.Errorf("cohere: unexpected embeddings.float shape")
			}
			vec := make([]float32, len(arr))
			for i, x := range arr {
				f, ok := x.(float64)
				if !ok {
					return nil, fmt.Errorf("cohere: non-float in embedding")
				}
				vec[i] = float32(f)
			}
			out = append(out, vec)
		}
		if len(out) != len(texts) {
			return nil, fmt.Errorf("cohere: got %d embeddings, want %d", len(out), len(texts))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("cohere: unexpected embeddings type %T", v)
	}
}
