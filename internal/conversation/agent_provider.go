package conversation

// agent_provider.go implements the Turn/StreamTurn/GenerateTitle methods
// using the provider.Provider interface (Ollama, future providers).
// These are called when a.provider != nil (instead of a.client).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/inkwell"
	"github.com/codecuttle/codecuttlectl/internal/provider"
	"github.com/codecuttle/codecuttlectl/internal/session"
)

// turnProvider implements Turn using the provider interface.
func (a *Agent) turnProvider(ctx context.Context, userMessage string) (string, error) {
	a.turn++
	a.provHistory = append(a.provHistory, provider.Message{
		Role:    provider.RoleUser,
		Content: []provider.ContentBlock{provider.TextBlock{Text: userMessage}},
	})

	// Initialize state dictionary for this turn — tracks ground-truth about execution.
	sd := newStateDict(userMessage)

	for step := 0; step < a.maxSteps; step++ {
		effectivePrompt := a.effectiveSystemPrompt()

		// Build the messages to send. After step 0, we inject the state dictionary
		// as a transient grounding message to keep the model anchored to reality.
		var messages []provider.Message
		if step > 0 {
			// Inject state dictionary before the model's next turn.
			// This is transient — not persisted in history — and replaces the
			// verbose grounding reminder with a structured state snapshot.
			messages = make([]provider.Message, len(a.provHistory)+1)
			copy(messages, a.provHistory)
			messages[len(a.provHistory)] = provider.Message{
				Role: provider.RoleUser,
				Content: []provider.ContentBlock{provider.TextBlock{
					Text: sd.render(),
				}},
			}
		} else {
			messages = a.provHistory
		}

		req := provider.Request{
			System:   effectivePrompt,
			Messages: messages,
			Tools:    a.allProviderToolDefs(),
		}

		resp, err := a.provider.Converse(ctx, req)
		if err != nil {
			return "", fmt.Errorf("converse step %d: %w", step, err)
		}

		if a.verbose {
			log.Printf("[step %d] stop_reason=%s tools=%d tokens_in=%d tokens_out=%d",
				step, resp.StopReason, len(resp.ToolUses), resp.Usage.InputTokens, resp.Usage.OutputTokens)
		}

		// Accumulate token usage
		a.recordTokenUsage(resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.CacheReadTokens, resp.Usage.CacheWriteTokens, step)

		// Build assistant message for history
		var assistBlocks []provider.ContentBlock
		if resp.Content != "" {
			assistBlocks = append(assistBlocks, provider.TextBlock{Text: resp.Content})
		}
		for _, tu := range resp.ToolUses {
			assistBlocks = append(assistBlocks, provider.ToolUseBlock{
				ToolUseID: tu.ToolUseID,
				Name:      tu.Name,
				Input:     tu.Input,
			})
		}
		if len(assistBlocks) > 0 {
			a.provHistory = append(a.provHistory, provider.Message{
				Role:    provider.RoleAssistant,
				Content: assistBlocks,
			})
		}

		// If no tool calls, we're done
		if len(resp.ToolUses) == 0 {
			a.flushSessionProvider()
			return resp.Content, nil
		}

		// Execute tool calls and collect results
		var resultBlocks []provider.ContentBlock
		for _, toolUse := range resp.ToolUses {
			if a.verbose {
				log.Printf("[tool] %s id=%s input=%s", toolUse.Name, toolUse.ToolUseID, string(toolUse.Input))
			}

			start := time.Now()
			result, status := a.executeTool(ctx, toolUse.Name, toolUse.Input)
			duration := time.Since(start)
			endTime := time.Now().UTC()

			if a.verbose {
				truncated := result
				if len(truncated) > 500 {
					truncated = truncated[:500] + "..."
				}
				log.Printf("[tool result] %s (%dms)", truncated, duration.Milliseconds())
			}

			isErr := status == types.ToolResultStatusError
			errType := string(inkwell.Classify(toolUse.Name, result, isErr).Class)
			a.inkwell = append(a.inkwell, session.InkEntry{
				Timestamp:        start.UTC(),
				EndTime:          endTime,
				Turn:             a.turn,
				Step:             step,
				ToolName:         toolUse.Name,
				ToolUseID:        toolUse.ToolUseID,
				Input:            toolUse.Input,
				Output:           result,
				DurationMs:       duration.Milliseconds(),
				IsError:          isErr,
				ErrorType:        errType,
				ReasoningContext: resp.Content,
				UserIntent:       userMessage,
			})

			a.recordToolExec(toolUse.Name, toolUse.ToolUseID, duration.Milliseconds(), isErr, errType, step)

			// Update state dictionary with ground-truth from this tool execution
			sd.recordToolResult(toolUse.Name, string(toolUse.Input), result, isErr)

			resultBlocks = append(resultBlocks, provider.ToolResultBlock{
				ToolUseID: toolUse.ToolUseID,
				Content:   result,
				IsError:   isErr,
			})
		}

		// Add tool results as a user message
		a.provHistory = append(a.provHistory, provider.Message{
			Role:    provider.RoleUser,
			Content: resultBlocks,
		})
		a.dirty = true
		a.flushSessionProvider()

		// Reconciler check
		advice := a.reconciler.Advise(a.inkwell)
		if advice.ShouldAbort && a.verbose {
			log.Printf("[inkwell] ABORT recommended: %s", advice.AbortReason)
		}
		if advice.InjectPrompt != "" && a.verbose {
			log.Printf("[inkwell] injecting corrective prompt (%d chars)", len(advice.InjectPrompt))
		}
	}

	return "", fmt.Errorf("exceeded maximum steps (%d) without completing", a.maxSteps)
}

// streamTurnProvider implements StreamTurn using the provider interface.
func (a *Agent) streamTurnProvider(ctx context.Context, userMessage string, cb StreamCallback) (string, error) {
	a.turn++
	a.provHistory = append(a.provHistory, provider.Message{
		Role:    provider.RoleUser,
		Content: []provider.ContentBlock{provider.TextBlock{Text: userMessage}},
	})

	for step := 0; step < a.maxSteps; step++ {
		req := provider.Request{
			System:   a.effectiveSystemPrompt(),
			Messages: a.provHistory,
			Tools:    a.allProviderToolDefs(),
		}

		ch := a.provider.ConverseStream(ctx, req)

		var textBuf strings.Builder
		var toolCalls []pendingToolCall
		var currentToolInput strings.Builder
		var currentToolID, currentToolName string

		for event := range ch {
			switch e := event.(type) {
			case provider.TextDeltaEvent:
				textBuf.WriteString(e.Text)
				if cb != nil {
					cb(StreamEvent{Type: "text", Text: e.Text})
				}

			case provider.ToolUseStartEvent:
				currentToolID = e.ToolUseID
				currentToolName = e.Name
				currentToolInput.Reset()
				if cb != nil {
					cb(StreamEvent{Type: "tool_start", ToolName: e.Name, ToolUseID: e.ToolUseID})
				}

			case provider.ToolInputDeltaEvent:
				currentToolInput.WriteString(e.Delta)

			case provider.ToolUseStopEvent:
				if currentToolName != "" {
					input := json.RawMessage(currentToolInput.String())
					if len(input) == 0 {
						input = json.RawMessage("{}")
					}
					toolCalls = append(toolCalls, pendingToolCall{
						id:    currentToolID,
						name:  currentToolName,
						input: input,
					})
					currentToolName = ""
					currentToolID = ""
					currentToolInput.Reset()
				}

			case provider.UsageEvent:
				a.recordTokenUsage(e.InputTokens, e.OutputTokens, e.CacheReadTokens, e.CacheWriteTokens, step)

			case provider.StreamErrorEvent:
				if cb != nil {
					cb(StreamEvent{Type: "error", Error: e.Err})
				}
				return textBuf.String(), e.Err

			case provider.MessageStopEvent:
				// Stream complete for this round
			}
		}

		// Build assistant message for history
		var assistBlocks []provider.ContentBlock
		if textBuf.Len() > 0 {
			assistBlocks = append(assistBlocks, provider.TextBlock{Text: textBuf.String()})
		}
		for _, tc := range toolCalls {
			assistBlocks = append(assistBlocks, provider.ToolUseBlock{
				ToolUseID: tc.id,
				Name:      tc.name,
				Input:     tc.input,
			})
		}
		if len(assistBlocks) > 0 {
			a.provHistory = append(a.provHistory, provider.Message{
				Role:    provider.RoleAssistant,
				Content: assistBlocks,
			})
		}

		// If no tool calls, we're done
		if len(toolCalls) == 0 {
			a.flushSessionProvider()
			if cb != nil {
				cb(StreamEvent{Type: "done"})
			}
			return textBuf.String(), nil
		}

		// Execute tool calls
		var resultBlocks []provider.ContentBlock
		for _, tc := range toolCalls {
			start := time.Now()
			result, status := a.executeTool(ctx, tc.name, tc.input)
			duration := time.Since(start)
			endTime := time.Now().UTC()

			if a.verbose {
				truncated := result
				if len(truncated) > 500 {
					truncated = truncated[:500] + "..."
				}
				log.Printf("[tool] %s (%dms): %s", tc.name, duration.Milliseconds(), truncated)
			}

			isErr := status == types.ToolResultStatusError
			errType := string(inkwell.Classify(tc.name, result, isErr).Class)
			a.inkwell = append(a.inkwell, session.InkEntry{
				Timestamp:        start.UTC(),
				EndTime:          endTime,
				Turn:             a.turn,
				Step:             step,
				ToolName:         tc.name,
				ToolUseID:        tc.id,
				Input:            tc.input,
				Output:           result,
				DurationMs:       duration.Milliseconds(),
				IsError:          isErr,
				ErrorType:        errType,
				ReasoningContext: textBuf.String(),
				UserIntent:       userMessage,
			})

			a.recordToolExec(tc.name, tc.id, duration.Milliseconds(), isErr, errType, step)

			resultBlocks = append(resultBlocks, provider.ToolResultBlock{
				ToolUseID: tc.id,
				Content:   result,
				IsError:   isErr,
			})

			if cb != nil {
				cb(StreamEvent{Type: "tool_done", ToolName: tc.name, ToolUseID: tc.id})
			}
		}

		a.provHistory = append(a.provHistory, provider.Message{
			Role:    provider.RoleUser,
			Content: resultBlocks,
		})
		a.dirty = true
		a.flushSessionProvider()

		// Clear text buffer for next round
		textBuf.Reset()
	}

	return "", fmt.Errorf("exceeded maximum steps (%d) without completing", a.maxSteps)
}

// generateTitleProvider generates a session title using the provider interface.
func (a *Agent) generateTitleProvider(ctx context.Context) string {
	if len(a.provHistory) == 0 {
		return "Empty session"
	}

	// Extract the first user message
	var firstMsg string
	for _, msg := range a.provHistory {
		if msg.Role == provider.RoleUser {
			for _, block := range msg.Content {
				if tb, ok := block.(provider.TextBlock); ok {
					firstMsg = tb.Text
					break
				}
			}
			break
		}
	}

	if firstMsg == "" {
		return "Untitled session"
	}

	titlePrompt := "Generate a 2-5 word title for a coding session that started with this message. Reply with ONLY the title, no punctuation, nothing else."
	req := provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentBlock{
				provider.TextBlock{Text: titlePrompt + "\n\nMessage: " + firstMsg},
			}},
		},
	}

	resp, err := a.provider.Converse(ctx, req)
	if err != nil {
		if len(firstMsg) > 50 {
			return firstMsg[:50]
		}
		return firstMsg
	}

	title := resp.Content
	if len(title) > 60 {
		title = title[:60]
	}
	return title
}

// allProviderToolDefs returns combined tool definitions as provider.ToolDefinition.
func (a *Agent) allProviderToolDefs() []provider.ToolDefinition {
	bedrockDefs := a.allToolDefs()
	var result []provider.ToolDefinition
	for _, d := range bedrockDefs {
		result = append(result, provider.ToolDefinition{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		})
	}
	return result
}

// flushSessionProvider persists session state for provider-based conversations.
// For now, provider sessions don't persist full history (since it's in a different
// format), but we still persist the inkwell and audit trail.
func (a *Agent) flushSessionProvider() {
	if a.store == nil || a.sessionID == "" {
		return
	}

	// Load existing state to preserve metadata
	existing, loadErr := a.store.Load(a.sessionID)
	var meta session.SessionMeta
	if loadErr == nil {
		meta = existing.Meta
	} else {
		meta.ID = a.sessionID
	}

	// Update stats
	meta.Stats.Turns = a.turn
	meta.Stats.ToolCalls = len(a.inkwell)

	state := &session.SessionState{
		Meta:    meta,
		Todos:   a.todos.Items(),
		Inkwell: a.inkwell,
		Audit:   a.AuditTrail(),
	}

	if err := a.store.Save(a.sessionID, state); err != nil {
		if a.verbose {
			log.Printf("[session] warning: failed to save session: %v", err)
		}
	}

	a.dirty = false
}
