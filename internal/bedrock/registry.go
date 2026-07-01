package bedrock

// ModelRole identifies the purpose a model serves in the system.
type ModelRole string

const (
	// RolePrimary is the main agent conversation model (Opus).
	RolePrimary ModelRole = "primary"

	// RoleAuxiliary is for cheap background tasks: summaries, titles, classification.
	RoleAuxiliary ModelRole = "auxiliary"

	// RolePlanning is for complex-but-not-frontier tasks (Sonnet tier).
	RolePlanning ModelRole = "planning"
)

// ModelInfo describes a model's capabilities and pricing.
type ModelInfo struct {
	ModelID        string
	DisplayName    string  // Short human-readable name (e.g. "haiku-4-5")
	ContextWindow  int32   // Max input tokens
	MaxOutput      int32   // Max output tokens
	InputCost      float64 // $/MTok input
	OutputCost     float64 // $/MTok output
	CacheReadCost  float64 // $/MTok cache read
	CacheWriteCost float64 // $/MTok cache write
	SupportsCache  bool    // Whether this model supports prompt caching
	SupportsTools  bool    // Whether this model supports tool use
}

// knownModels is the static registry of Bedrock model capabilities and pricing.
var knownModels = map[string]ModelInfo{
	"us.anthropic.claude-opus-4-8": {
		ModelID:        "us.anthropic.claude-opus-4-8",
		DisplayName:    "opus-4-8",
		ContextWindow:  1_000_000,
		MaxOutput:      128_000,
		InputCost:      5.00,
		OutputCost:     25.00,
		CacheReadCost:  0.50,
		CacheWriteCost: 6.25,
		SupportsCache:  true,
		SupportsTools:  true,
	},
	"us.anthropic.claude-opus-4-6-v1": {
		ModelID:        "us.anthropic.claude-opus-4-6-v1",
		DisplayName:    "opus-4-6",
		ContextWindow:  1_000_000,
		MaxOutput:      128_000,
		InputCost:      5.00,
		OutputCost:     25.00,
		CacheReadCost:  0.50,
		CacheWriteCost: 6.25,
		SupportsCache:  true,
		SupportsTools:  true,
	},
	"us.anthropic.claude-sonnet-4-6": {
		ModelID:        "us.anthropic.claude-sonnet-4-6",
		DisplayName:    "sonnet-4-6",
		ContextWindow:  1_000_000,
		MaxOutput:      64_000,
		InputCost:      3.00,
		OutputCost:     15.00,
		CacheReadCost:  0.30,
		CacheWriteCost: 3.75,
		SupportsCache:  true,
		SupportsTools:  true,
	},
	"us.anthropic.claude-haiku-4-5-20251001-v1:0": {
		ModelID:        "us.anthropic.claude-haiku-4-5-20251001-v1:0",
		DisplayName:    "haiku-4-5",
		ContextWindow:  200_000,
		MaxOutput:      64_000,
		InputCost:      1.00,
		OutputCost:     5.00,
		CacheReadCost:  0.10,
		CacheWriteCost: 1.25,
		SupportsCache:  true,
		SupportsTools:  true,
	},
	"anthropic.claude-fable-5": {
		ModelID:        "anthropic.claude-fable-5",
		DisplayName:    "fable-5",
		ContextWindow:  1_000_000,
		MaxOutput:      128_000,
		InputCost:      10.00,
		OutputCost:     50.00,
		CacheReadCost:  1.00,
		CacheWriteCost: 12.50,
		SupportsCache:  true,
		SupportsTools:  true,
	},
}

// modelAliases maps short names to full Bedrock model IDs.
var modelAliases = map[string]string{
	"opus":       "us.anthropic.claude-opus-4-6-v1",
	"opus-4-6":   "us.anthropic.claude-opus-4-6-v1",
	"opus-4-8":   "us.anthropic.claude-opus-4-8",
	"sonnet":     "us.anthropic.claude-sonnet-4-6",
	"sonnet-4-6": "us.anthropic.claude-sonnet-4-6",
	"haiku":      "us.anthropic.claude-haiku-4-5-20251001-v1:0",
	"haiku-4-5":  "us.anthropic.claude-haiku-4-5-20251001-v1:0",
	"fable":      "anthropic.claude-fable-5",
	"fable-5":    "anthropic.claude-fable-5",
}

// ResolveModelID resolves a model alias or full ID to a canonical model ID.
// If the input is a known alias, it returns the full ID. Otherwise returns as-is.
func ResolveModelID(idOrAlias string) string {
	if full, ok := modelAliases[idOrAlias]; ok {
		return full
	}
	return idOrAlias
}

// LookupModel returns the known ModelInfo for a model ID.
// For unknown models, returns a conservative default with just the ModelID set.
func LookupModel(modelID string) ModelInfo {
	if info, ok := knownModels[modelID]; ok {
		return info
	}
	// Unknown model — assume expensive (safe default)
	return ModelInfo{
		ModelID:       modelID,
		DisplayName:   modelID,
		ContextWindow: 200_000,
		MaxOutput:     8_192,
		InputCost:     5.00,
		OutputCost:    25.00,
		SupportsCache: false,
		SupportsTools: true,
	}
}
