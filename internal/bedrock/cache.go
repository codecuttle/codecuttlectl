package bedrock

import (
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// systemPromptSeparator is the marker between the stable base prompt and
// dynamic injections (skills, reconciler advice). The cache checkpoint is
// placed between them so the base prompt is always cached regardless of
// what dynamic content is appended.
const systemPromptSeparator = "\n\n## Additional Tool Guidance\n"

// buildSystemBlocks splits the system prompt into stable (cacheable) and
// dynamic (variable per turn) portions. The cache checkpoint goes after
// the stable part so it always hits cache. Dynamic injections (skills,
// reconciler) come after and are processed fresh each turn.
//
// Layout:
//   [stable base prompt] [CACHE_POINT] [dynamic injections]
//
// This means:
//   - Base system prompt + tool hints: cached (stable across all turns)
//   - Skills/reconciler injections: fresh (vary by context)
//   - Net effect: ~5-6k tokens cached, ~0.5-2k tokens fresh per turn
func buildSystemBlocks(system string) []types.SystemContentBlock {
	// Split at the dynamic injection boundary
	// The system prompt is constructed as: base + "\n\n## Additional Tool Guidance\n" + hints + skills + reconciler
	// We want to cache everything up to and including the tool guidance (stable per session)
	// and leave skills/reconciler uncached (vary per turn).
	
	// Find the LAST occurrence of a skill/reconciler injection marker
	// Skills are appended as "\n\n## Active Skills\n" 
	// Reconciler is appended as "\n\n## Inkwell Diagnostic Alert\n" or similar
	
	skillMarker := "\n\n## Active Skills\n"
	inkwellMarker := "\n\n## Inkwell"
	
	// Find where dynamic content begins (first skill or inkwell injection)
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
		// Cache checkpoint: everything above this is stable per session
		&types.SystemContentBlockMemberCachePoint{Value: types.CachePointBlock{
			Type: types.CachePointTypeDefault,
		}},
		// Dynamic injections below — NOT cached (vary per turn based on context)
		&types.SystemContentBlockMemberText{Value: dynamicPrompt},
	}
}

// applyCachePoints inserts cache checkpoints into a message slice to maximize
// cache hits across multi-turn conversations. The strategy:
//
//   - Place a checkpoint after the second-to-last message. This means on
//     turn N, messages 1..N-2 (which are identical to turn N-1) get read
//     from cache rather than reprocessed. Only the latest user message is
//     fresh input.
//
//   - Only place the checkpoint if there are enough messages to benefit
//     (at least 3 messages — otherwise the history is too small to cache).
//
//   - The checkpoint is a content block appended to an existing message's
//     content. Per Bedrock docs, cache checkpoints within messages must be
//     ContentBlock items inside a message's Content slice.
//
// This uses 1 of the remaining 3 cache checkpoints (system uses 1, max is 4).
func applyCachePoints(messages []types.Message) []types.Message {
	if len(messages) < 3 {
		return messages
	}

	// Make a shallow copy so we don't mutate the caller's slice
	result := make([]types.Message, len(messages))
	copy(result, messages)

	// Place cache point on the second-to-last message.
	// This means everything up to and including that message gets cached.
	// The last message (newest user input) is the only uncached portion.
	idx := len(result) - 2

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
