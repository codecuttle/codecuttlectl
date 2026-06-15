package googleprov

import (
	"context"
	"strings"

	"google.golang.org/genai"
)

// NewClientForListing creates a bare genai.Client suitable for listing models.
// Uses GEMINI_API_KEY from environment or Application Default Credentials.
func NewClientForListing(ctx context.Context) (*genai.Client, error) {
	return genai.NewClient(ctx, nil)
}

// ListModels returns a list of available model names from the Google AI API.
// Filters to only include generative models (gemini-*).
func ListModels(ctx context.Context, client *genai.Client) ([]string, error) {
	page, err := client.Models.List(ctx, nil)
	if err != nil {
		return nil, err
	}

	var models []string
	for _, m := range page.Items {
		name := m.Name
		// Strip "models/" prefix if present
		name = strings.TrimPrefix(name, "models/")
		// Only include gemini models (skip embedding-only, etc.)
		if strings.HasPrefix(name, "gemini-") {
			models = append(models, name)
		}
	}
	return models, nil
}

// ModelAliases returns the current alias map for display purposes.
func ModelAliases() map[string]string {
	// Return a copy
	result := make(map[string]string, len(modelAliases))
	for k, v := range modelAliases {
		result[k] = v
	}
	return result
}
