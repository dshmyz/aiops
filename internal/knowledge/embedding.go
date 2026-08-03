package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Embedder turns a text string into a dense vector.
type Embedder interface {
	// Embed returns the embedding vector for the given text. The implementation
	// may batch or cache internally.
	Embed(ctx context.Context, text string) ([]float32, error)
}

// OpenAIEmbedder calls an OpenAI-compatible /v1/embeddings endpoint.
type OpenAIEmbedder struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

// NewOpenAIEmbedder creates an embedder targeting the given OpenAI-compatible
// endpoint. BaseURL should be the scheme+host (e.g. https://api.openai.com).
func NewOpenAIEmbedder(apiKey, baseURL, model string) *OpenAIEmbedder {
	return &OpenAIEmbedder{
		APIKey:     apiKey,
		BaseURL:    baseURL,
		Model:      model,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if e.BaseURL == "" || e.Model == "" {
		return nil, fmt.Errorf("OpenAIEmbedder BaseURL and Model are required")
	}
	url := e.BaseURL + "/v1/embeddings"
	reqBody, err := json.Marshal(map[string]any{
		"input": text,
		"model": e.Model,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}

	resp, err := e.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embeddings returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("embeddings response contained no data")
	}
	return result.Data[0].Embedding, nil
}
