package bedrock

import "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

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
