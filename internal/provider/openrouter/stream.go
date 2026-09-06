package openrouter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/codecuttle/codecuttlectl/internal/provider"
)

const (
	maxSSEEventBytes = 256 * 1024
	maxToolBytes     = 4 * 1024 * 1024
	maxStreamTools   = 128
)

// streamUsage keeps field presence distinct from an explicitly reported zero.
// Usage chunks are cumulative snapshots, not increments.
type streamUsage struct {
	PromptTokens     *int32 `json:"prompt_tokens"`
	CompletionTokens *int32 `json:"completion_tokens"`
}

func sendStreamEvent(ctx context.Context, events chan<- provider.StreamEvent, event provider.StreamEvent) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case events <- event:
		return nil
	}
}

// parseSSEStream translates SSE into the provider's sequential block protocol.
// Tool arguments are buffered by index, validated, then emitted as contiguous
// start/input/stop blocks only after a successful stream termination. This avoids
// both interleaved argument corruption and execution of incomplete tool calls.
// The caller owns the reader and must cancel its reads through the HTTP context.
func parseSSEStream(ctx context.Context, body io.Reader, events chan<- provider.StreamEvent) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), maxSSEEventBytes)
	type toolCall struct {
		id, name string
		args     strings.Builder
	}
	tools := make(map[int]*toolCall)
	toolBytes := 0
	var usage streamUsage
	var finishReason string
	finished, done := false, false

	emit := func(event provider.StreamEvent) error {
		return sendStreamEvent(ctx, events, event)
	}
	process := func(data string) error {
		if data == "[DONE]" {
			done = true
			return nil
		}
		var chunk struct {
			Choices []chunkChoice   `json:"choices"`
			Usage   *streamUsage    `json:"usage"`
			Error   json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("openrouter: invalid SSE JSON: %w", err)
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			var upstream struct {
				Code json.RawMessage `json:"code"`
			}
			_ = json.Unmarshal(chunk.Error, &upstream)
			// Do not copy arbitrary upstream messages (which may echo prompts).
			return fmt.Errorf("openrouter: upstream stream error (code %s)", upstream.Code)
		}
		if chunk.Usage != nil {
			if n := chunk.Usage.PromptTokens; n != nil {
				if *n < 0 {
					return fmt.Errorf("openrouter: negative prompt token count")
				}
				usage.PromptTokens = n
			}
			if n := chunk.Usage.CompletionTokens; n != nil {
				if *n < 0 {
					return fmt.Errorf("openrouter: negative completion token count")
				}
				usage.CompletionTokens = n
			}
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				return fmt.Errorf("openrouter: unexpected completion choice %d", choice.Index)
			}
			delta := choice.Delta
			if finished {
				// OpenRouter's final usage frame may repeat the terminal choice
				// with an empty delta (including role/content placeholders).
				// Accept metadata, but never new content or a changed stop reason.
				if delta.Content != "" || delta.Reasoning != "" || len(delta.ToolCalls) > 0 ||
					(choice.FinishReason != "" && choice.FinishReason != finishReason) {
					return fmt.Errorf("openrouter: content or conflicting finish reason after completion")
				}
				continue
			}
			if delta.Reasoning != "" {
				if err := emit(provider.ReasoningDeltaEvent{Text: delta.Reasoning}); err != nil {
					return err
				}
			}
			if delta.Content != "" {
				if err := emit(provider.TextDeltaEvent{Text: delta.Content}); err != nil {
					return err
				}
			}
			for _, tc := range delta.ToolCalls {
				if tc.Index < 0 || tc.Index >= maxStreamTools {
					return fmt.Errorf("openrouter: tool index out of range")
				}
				active := tools[tc.Index]
				if active == nil {
					active = &toolCall{}
					tools[tc.Index] = active
				}
				if tc.ID != "" {
					if active.id != "" && active.id != tc.ID {
						return fmt.Errorf("openrouter: tool identity changed midstream")
					}
					active.id = tc.ID
				}
				if tc.Function.Name != "" {
					if active.name != "" && active.name != tc.Function.Name {
						return fmt.Errorf("openrouter: tool name changed midstream")
					}
					active.name = tc.Function.Name
				}
				toolBytes += len(tc.ID) + len(tc.Function.Name) + len(tc.Function.Arguments)
				if toolBytes > maxToolBytes {
					return fmt.Errorf("openrouter: tool arguments exceed stream limit")
				}
				active.args.WriteString(tc.Function.Arguments)
			}
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
				finished = true
			}
		}
		return nil
	}

	// SSE joins repeated data fields with newlines and dispatches on a blank
	// line. A single optional space after the colon is part of the framing.
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		payload := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		return process(payload)
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			if done {
				break
			}
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		if field != "data" {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		if data.Len()+len(value)+1 > maxSSEEventBytes {
			return fmt.Errorf("openrouter: SSE event exceeds size limit")
		}
		data.WriteString(value)
		data.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("openrouter: reading SSE: %w", err)
	}
	if err := flush(); err != nil {
		return err
	}
	if !finished {
		return fmt.Errorf("openrouter: stream ended before finish reason: %w", io.ErrUnexpectedEOF)
	}
	if len(tools) > 0 && finishReason != "tool_calls" {
		return fmt.Errorf("openrouter: tool calls interrupted by finish reason %q", finishReason)
	}
	if len(tools) == 0 && finishReason == "tool_calls" {
		return fmt.Errorf("openrouter: tool_calls finish reason without tools")
	}

	indexes := make([]int, 0, len(tools))
	ids := make(map[string]bool)
	inputs := make(map[int]string)
	for index, tool := range tools {
		if tool.id == "" || tool.name == "" || ids[tool.id] {
			return fmt.Errorf("openrouter: incomplete or duplicate tool identity")
		}
		ids[tool.id] = true
		input := strings.TrimSpace(tool.args.String())
		if input == "" {
			input = "{}"
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(input), &object); err != nil || object == nil {
			return fmt.Errorf("openrouter: invalid tool argument object at index %d", index)
		}
		inputs[index] = input
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		tool := tools[index]
		for _, event := range []provider.StreamEvent{
			provider.ToolUseStartEvent{ToolUseID: tool.id, Name: tool.name},
			provider.ToolInputDeltaEvent{Delta: inputs[index]},
			provider.ToolUseStopEvent{},
		} {
			if err := emit(event); err != nil {
				return err
			}
		}
	}
	if err := emit(provider.MessageStopEvent{StopReason: finishReason}); err != nil {
		return err
	}
	if usage.PromptTokens != nil || usage.CompletionTokens != nil {
		event := provider.UsageEvent{InputTokensUnknown: usage.PromptTokens == nil}
		if usage.PromptTokens != nil {
			event.InputTokens = *usage.PromptTokens
		}
		if usage.CompletionTokens != nil {
			event.OutputTokens = *usage.CompletionTokens
		}
		return emit(event)
	}
	return nil
}
