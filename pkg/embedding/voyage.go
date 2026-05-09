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

// Voyage is the §4.7 Voyage AI embeddings provider. Defaults to
// `voyage-3` (1024 dim); set Model to override.
//
// Wire format: POST <BaseURL>/embeddings with body
// `{"model":"<model>","input":[<texts>],"input_type":"document"}`.
type Voyage struct {
	APIKey  string
	Model_  string
	Dim     int
	BaseURL string
	Client  *http.Client
}

// ID returns "voyage".
func (Voyage) ID() string { return "voyage" }

// Model returns the configured model, defaulting to voyage-3.
func (p Voyage) Model() string {
	if p.Model_ != "" {
		return p.Model_
	}
	return "voyage-3"
}

// Dimensions returns the configured dimension; defaults vary by
// model (voyage-3 = 1024).
func (p Voyage) Dimensions() int {
	if p.Dim > 0 {
		return p.Dim
	}
	switch p.Model() {
	case "voyage-large-2", "voyage-large-2-instruct":
		return 1536
	case "voyage-code-3":
		return 1024
	default:
		return 1024
	}
}

func (p Voyage) baseURL() string {
	if p.BaseURL != "" {
		return strings.TrimRight(p.BaseURL, "/")
	}
	return "https://api.voyageai.com/v1"
}

func (p Voyage) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return httpClient
}

// Embed sends a batch of texts and returns vectors in input order.
func (p Voyage) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, ErrEmptyTexts
	}
	body, err := json.Marshal(map[string]any{
		"model":      p.Model(),
		"input":      texts,
		"input_type": "document",
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
		return nil, fmt.Errorf("voyage: decode: %w", err)
	}
	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("voyage: index out of range: %d", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("voyage: missing embedding for index %d", i)
		}
	}
	return out, nil
}
