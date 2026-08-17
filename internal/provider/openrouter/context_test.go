package openrouter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// modelListResponse represents the schema returned by GET https://openrouter.ai/api/v1/models
type modelListResponse struct {
	Data []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ContextLength int    `json:"context_length"`
		Description   string `json:"description,omitempty"`
		TopProvider   struct {
			ContextLength       int  `json:"context_length"`
			MaxCompletionTokens int  `json:"max_completion_tokens"`
			IsModerated         bool `json:"is_moderated"`
		} `json:"top_provider"`
	} `json:"data"`
}

func TestOpenRouterModels_MockJSON(t *testing.T) {
	testDataPath := filepath.Join("..", "..", "..", "testdata", "openrouter_models.json")
	data, err := os.ReadFile(testDataPath)
	if err != nil {
		t.Fatalf("failed to read testdata %s: %v", testDataPath, err)
	}

	var resp modelListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("failed to unmarshal openrouter models json: %v", err)
	}

	if len(resp.Data) == 0 {
		t.Fatal("expected non-empty model list in testdata")
	}

	expectedContextLengths := map[string]int{
		"openai/gpt-4o":               128000,
		"anthropic/claude-3.5-sonnet": 200000,
		"qwen/qwen3.8-max":            32768,
		"google/gemini-2.0-flash-001": 1048576,
	}

	found := make(map[string]int)
	for _, m := range resp.Data {
		found[m.ID] = m.ContextLength
	}

	for id, wantLen := range expectedContextLengths {
		gotLen, ok := found[id]
		if !ok {
			t.Errorf("model %q missing from mock data", id)
			continue
		}
		if gotLen != wantLen {
			t.Errorf("model %q context length = %d, want %d", id, gotLen, wantLen)
		}
	}
}
