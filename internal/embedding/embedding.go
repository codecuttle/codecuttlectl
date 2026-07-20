package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/codecuttle/codecuttlectl/internal/keyring"
)

// Generate creates a vector embedding for the given text.
// Currently uses Google's gemini-embedding-2 via direct HTTP (768 dimensions projected).
func Generate(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}

	// Ensure we have the API key
	if err := keyring.EnsureGeminiAPIKey(); err != nil {
		return nil, fmt.Errorf("failed to get Gemini API key: %w", err)
	}
	apiKey := os.Getenv("GEMINI_API_KEY")

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-2:embedContent?key=%s", apiKey)

	payload := map[string]interface{}{
		"model": "models/gemini-embedding-2",
		"outputDimensionality": 768,
		"content": map[string]interface{}{
			"parts": []map[string]interface{}{
				{
					"text": text,
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Embedding.Values) == 0 {
		return nil, fmt.Errorf("received empty embedding")
	}

	return result.Embedding.Values, nil
}
