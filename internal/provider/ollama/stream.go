package ollama

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/codecuttle/codecuttlectl/internal/provider"
)

// parseSSEStream reads Server-Sent Events from the Ollama streaming response
// and translates them into provider.StreamEvent sent on the events channel.
func parseSSEStream(body io.Reader, events chan<- provider.StreamEvent) {
	scanner := bufio.NewScanner(body)
	// Increase scanner buffer for potentially large tool call arguments.
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	// Track active tool calls by index for multi-tool streaming.
	type activeToolCall struct {
		id       string
		name     string
		argsBuf  strings.Builder
		started  bool
	}
	toolCalls := make(map[int]*activeToolCall)

	// Track whether we're in reasoning mode (to detect transition to content).
	inReasoning := false

	for scanner.Scan() {
		line := scanner.Text()

		// SSE format: lines starting with "data: " contain the payload.
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// "[DONE]" signals end of stream.
		if data == "[DONE]" {
			break
		}

		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			events <- provider.StreamErrorEvent{
				Err: err,
			}
			continue
		}

		// Emit usage if present (typically on last chunk).
		if chunk.Usage != nil {
			events <- provider.UsageEvent{
				InputTokens:  int32(chunk.Usage.PromptTokens),
				OutputTokens: int32(chunk.Usage.CompletionTokens),
			}
		}

		for _, choice := range chunk.Choices {
			delta := choice.Delta

			// Handle reasoning/thinking content.
			if delta.Reasoning != "" {
				if !inReasoning {
					inReasoning = true
				}
				events <- provider.ReasoningDeltaEvent{Text: delta.Reasoning}
			}

			// Handle text content.
			if delta.Content != "" {
				if inReasoning {
					// Transition from reasoning to content — reasoning is done.
					inReasoning = false
				}
				events <- provider.TextDeltaEvent{Text: delta.Content}
			}

			// Handle tool calls (streamed incrementally).
			for _, tc := range delta.ToolCalls {
				idx := tc.Index
				active, exists := toolCalls[idx]

				if !exists {
					// New tool call — emit start event.
					active = &activeToolCall{
						id:   tc.ID,
						name: tc.Function.Name,
					}
					toolCalls[idx] = active
				}

				// Update ID/name if provided (they come in the first chunk).
				if tc.ID != "" {
					active.id = tc.ID
				}
				if tc.Function.Name != "" {
					active.name = tc.Function.Name
				}

				// Emit ToolUseStart once we have both id and name.
				if !active.started && active.id != "" && active.name != "" {
					active.started = true
					events <- provider.ToolUseStartEvent{
						ToolUseID: active.id,
						Name:      active.name,
					}
				}

				// Accumulate arguments.
				if tc.Function.Arguments != "" {
					active.argsBuf.WriteString(tc.Function.Arguments)
					events <- provider.ToolInputDeltaEvent{Delta: tc.Function.Arguments}
				}
			}

			// Check for finish reason.
			if choice.FinishReason != "" {
				// Flush any pending tool calls.
				for _, active := range toolCalls {
					if active.started {
						events <- provider.ToolUseStopEvent{}
					}
				}
				toolCalls = make(map[int]*activeToolCall)

				events <- provider.MessageStopEvent{StopReason: choice.FinishReason}
			}
		}
	}
}
