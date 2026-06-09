package bedrock

import (
	"encoding/json"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// Cache Strategy: 3-tier incremental extension
//
// Bedrock evaluates payload in order: tools → system → messages.
// Any change in an earlier section invalidates all subsequent cached sections.
// Cache checkpoints require exact byte-for-byte prefix match to hit.
//
// Tier 1: Tools (~12k tokens, 100% stable per session)
//   - Cache checkpoint at end of tools array
//   - Never changes during a session → always hits
//
// Tier 2: System prompt (stable base, ~6k tokens)
//   - Cache checkpoint after the stable base portion
//   - Dynamic injections (skills, reconciler) placed AFTER the checkpoint
//   - Dynamic content changes don't invalidate the cached prefix
//
// Tier 3: Messages (incremental extension)
//   - Cache checkpoint on the LAST message (most recent user/tool_result)
//   - On each call, Bedrock reads the existing prefix from cache and only
//     computes the marginal delta (new content since last checkpoint)
//   - The key insight: the checkpoint extends forward, never shifts backward
//
// This uses 3 of the 4 available checkpoints per request.
// Minimum 4,096 tokens per checkpoint for Claude Opus 4.x.

// buildToolsWithCache wraps the tool configuration with a cache checkpoint
// at the end. Tool definitions are 100% stable per session (~12k tokens),
// making this the highest-value cache target.
//
// Bedrock evaluates tools FIRST in the hierarchy, so this cached prefix
// is the foundation that all subsequent sections build upon.
//
// Note: We use toBedrockToolsSorted here for explicit determinism, even though
// the smithy-go JSON encoder sorts map keys internally. Belt-and-suspenders
// for cache stability — any byte-level change invalidates the entire tool prefix.
func buildToolsWithCache(tools []ToolDefinition) *types.ToolConfiguration {
	if len(tools) == 0 {
		return nil
	}

	bedrockTools := toBedrockToolsSorted(tools)

	// Append cache checkpoint as the last element in the tools array
	bedrockTools = append(bedrockTools, &types.ToolMemberCachePoint{
		Value: types.CachePointBlock{
			Type: types.CachePointTypeDefault,
		},
	})

	return &types.ToolConfiguration{
		Tools: bedrockTools,
	}
}

// buildSystemBlocks splits the system prompt into stable (cacheable) and
// dynamic (variable per turn) portions. The cache checkpoint goes after
// the stable part so it always hits cache. Dynamic injections (skills,
// reconciler) come after and are processed fresh each turn.
//
// Layout:
//
//	[stable base prompt + tool guidance] [CACHE_POINT] [dynamic injections]
//
// The stable portion includes everything up to the first dynamic marker.
// Dynamic markers: "## Active Skills", "## Inkwell"
func buildSystemBlocks(system string) []types.SystemContentBlock {
	// Find where dynamic content begins (first skill or inkwell injection)
	skillMarker := "\n\n## Active Skills\n"
	inkwellMarker := "\n\n## Inkwell"

	splitIdx := -1
	if idx := strings.Index(system, skillMarker); idx != -1 {
		splitIdx = idx
	}
	if idx := strings.Index(system, inkwellMarker); idx != -1 {
		if splitIdx == -1 || idx < splitIdx {
			splitIdx = idx
		}
	}

	if splitIdx == -1 {
		// No dynamic injections — cache the entire system prompt
		return []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: system},
			&types.SystemContentBlockMemberCachePoint{Value: types.CachePointBlock{
				Type: types.CachePointTypeDefault,
			}},
		}
	}

	// Split into stable (cached) and dynamic (fresh) portions
	stablePrompt := system[:splitIdx]
	dynamicPrompt := system[splitIdx:]

	return []types.SystemContentBlock{
		&types.SystemContentBlockMemberText{Value: stablePrompt},
		// Cache checkpoint: everything above (including tool cache) is stable
		&types.SystemContentBlockMemberCachePoint{Value: types.CachePointBlock{
			Type: types.CachePointTypeDefault,
		}},
		// Dynamic injections below — NOT cached (vary per turn)
		&types.SystemContentBlockMemberText{Value: dynamicPrompt},
	}
}

// applyCachePoints implements Incremental Extension Caching.
//
// Instead of shifting a checkpoint backward (which invalidates the cache),
// we place the checkpoint on the LAST message in the array. This means:
//
//   - Turn 1: Bedrock writes tools+system+messages[0..N] to cache
//   - Turn 2: Bedrock reads the prefix from cache (tools+system+messages[0..N]),
//     processes only the new delta (messages[N+1..M]), and writes the extended
//     prefix to cache
//   - Turn 3+: Same pattern — read old prefix, compute delta, write extended
//
// The prefix grows monotonically. Since messages are only ever appended (never
// modified or reordered), the byte-for-byte prefix match is guaranteed.
//
// This uses 1 of the remaining 2 cache checkpoints (tools=1, system=1, messages=1, total=3/4).
func applyCachePoints(messages []types.Message) []types.Message {
	if len(messages) < 2 {
		return messages
	}

	// Make a shallow copy so we don't mutate the caller's slice
	result := make([]types.Message, len(messages))
	copy(result, messages)

	// Place cache point on the LAST message (the most recent content).
	// Bedrock will read the cached prefix of all prior messages and only
	// process the delta since the last cache write.
	idx := len(result) - 1

	// Copy the message's content slice so we don't mutate the original
	origContent := result[idx].Content
	newContent := make([]types.ContentBlock, len(origContent), len(origContent)+1)
	copy(newContent, origContent)
	newContent = append(newContent, &types.ContentBlockMemberCachePoint{
		Value: types.CachePointBlock{
			Type: types.CachePointTypeDefault,
		},
	})

	result[idx] = types.Message{
		Role:    result[idx].Role,
		Content: newContent,
	}

	return result
}

// toBedrockToolsSorted converts tool definitions to Bedrock tools with
// deterministic JSON serialization. This is critical for cache stability:
// if the JSON byte order changes between calls, the cache prefix won't match.
//
// Go's map iteration order is non-deterministic, so we use json.Marshal
// (which sorts map keys) to ensure consistent serialization.
func toBedrockToolsSorted(tools []ToolDefinition) []types.Tool {
	var result []types.Tool
	for _, t := range tools {
		// Use ordered JSON to ensure byte-stable schema serialization
		var orderedSchema interface{}
		if len(t.InputSchema) > 0 {
			// json.Unmarshal into interface{} then json.Marshal produces
			// sorted keys, but we need to go through a map for the document.
			// The key insight: we unmarshal to get the structure, but the
			// document.NewLazyDocument will serialize deterministically from
			// the same input structure.
			var schema map[string]interface{}
			_ = json.Unmarshal(t.InputSchema, &schema)
			orderedSchema = schema
		}

		result = append(result, &types.ToolMemberToolSpec{
			Value: types.ToolSpecification{
				Name:        strPtr(t.Name),
				Description: strPtr(t.Description),
				InputSchema: &types.ToolInputSchemaMemberJson{
					Value: lazyDoc(orderedSchema),
				},
			},
		})
	}
	return result
}
