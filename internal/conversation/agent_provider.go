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
	"github.com/codecuttle/codecuttlectl/internal/compact"
	"github.com/codecuttle/codecuttlectl/internal/embedding"
	"github.com/codecuttle/codecuttlectl/internal/inkwell"
	"github.com/codecuttle/codecuttlectl/internal/opticlobe"
	"github.com/codecuttle/codecuttlectl/internal/provider"
	"github.com/codecuttle/codecuttlectl/internal/session"
	"github.com/codecuttle/codecuttlectl/internal/swarm"
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

		// For smaller/local models, append the state dictionary to the system prompt.
		// This provides continuous grounding by injecting a structured reality-anchor
		// (per PDDL-INSTRUCT / "Code as Agent Harness" research). Large frontier models
		// don't need this — they maintain their own world state effectively.
		if step >= 2 && a.needsGroundingAssist() {
			effectivePrompt += "\n\n" + sd.render()
		}

		req := provider.Request{
			System:   effectivePrompt,
			Messages: a.provHistory,
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
				ToolUseID:        tu.ToolUseID,
				Name:             tu.Name,
				Input:            tu.Input,
				ThoughtSignature: tu.ThoughtSignature,
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
			// Try to extract a plan from the model's final response for the todo panel
			a.maybeUpdatePlanFromText(resp.Content)
			a.flushSessionProvider()
			return resp.Content, nil
		}

		// The model expressed intent + tool calls — check for plan in its text
		a.maybeUpdatePlanFromText(resp.Content)

		// Execute tool calls and collect results
		var resultBlocks []provider.ContentBlock
		for _, toolUse := range resp.ToolUses {
			if a.verbose {
				log.Printf("[tool] %s id=%s input=%s sig=%s", toolUse.Name, toolUse.ToolUseID, string(toolUse.Input), toolUse.ThoughtSignature)
			}

			if a.dispatcher != nil {
				a.dispatcher.Dispatch(swarm.TaskProgressMsg{
					Assignee: a.activeNode,
					Progress: fmt.Sprintf("Calling %s...", toolUse.Name),
				})
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
				Name:      toolUse.Name,
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

		// Compact old tool results to keep context window manageable for small models
		if a.needsGroundingAssist() {
			var summaries []string
			a.provHistory, _, summaries = compact.CompactProviderIfNeeded(
				a.provHistory, a.turn, 0, 0, compact.SmallModelConfig(),
			)
			if len(summaries) > 0 && a.opticStore != nil && a.opticStore.IsAvailable() {
				// Inject the summaries into the Optic Lobe as Insights to preserve the knowledge graph
				for _, sum := range summaries {
					emb, _ := embedding.Generate(ctx, sum)
					if emb == nil {
						emb = make([]float32, 768) // Fallback zero vector
					}
					_, _ = a.opticStore.AddInsight(ctx, "global", opticlobe.InsightData{
						Content:   "Compacted memory trace: " + sum,
						Embedding: emb,
					})
				}
			}
		}

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

	// State dictionary for grounding (only used for small models)
	sd := newStateDict(userMessage)

	for step := 0; step < a.maxSteps; step++ {
		// For smaller/local models, append state dictionary to system prompt.
		effectivePrompt := a.effectiveSystemPrompt()
		if step >= 2 && a.needsGroundingAssist() {
			effectivePrompt += "\n\n" + sd.render()
		}

		req := provider.Request{
			System:   effectivePrompt,
			Messages: a.provHistory,
			Tools:    a.allProviderToolDefs(),
		}

		var textBuf strings.Builder
		var reasoningBuf strings.Builder
		var toolCalls []pendingToolCall
		var currentToolInput strings.Builder
		var currentToolID, currentToolName, currentToolSig string

		// Bedrock can fail mid-stream with transient errors (e.g.
		// InternalServerException) or reject the request outright with
		// ThrottlingException ("Too many tokens, please wait before trying
		// again") once the SDK's own millisecond-scale retries are exhausted.
		// Retry here with error-aware backoff: short exponential for server
		// faults, long (30s+) for throttling so the token bucket can refill.
		// Partial output from a failed attempt is discarded so history is
		// never polluted with truncated assistant messages.
		const maxStreamRetries = 5
		for attempt := 0; ; attempt++ {
			textBuf.Reset()
			reasoningBuf.Reset()
			toolCalls = nil
			currentToolInput.Reset()
			currentToolID, currentToolName, currentToolSig = "", "", ""

			var streamErr error
			ch := a.provider.ConverseStream(ctx, req)

			for event := range ch {
				switch e := event.(type) {
				case provider.TextDeltaEvent:
					textBuf.WriteString(e.Text)
					if cb != nil {
						cb(StreamEvent{Type: "text", Text: e.Text})
					}

				case provider.ReasoningDeltaEvent:
					reasoningBuf.WriteString(e.Text)

				case provider.ReasoningSignatureEvent:
					currentToolSig = e.Signature

				case provider.ToolUseStartEvent:
					currentToolID = e.ToolUseID
					currentToolName = e.Name
					if e.ThoughtSignature != "" {
						currentToolSig = e.ThoughtSignature
					}
					currentToolInput.Reset()
					if cb != nil {
						cb(StreamEvent{Type: "tool_start", ToolName: e.Name, ToolUseID: e.ToolUseID})
					}

				case provider.ToolInputDeltaEvent:
					currentToolInput.WriteString(e.Delta)

				case provider.ToolUseStopEvent:
					if currentToolName != "" {
						raw := strings.TrimSpace(currentToolInput.String())
						if raw == "" || raw == "null" {
							// Zero-argument tool call — Bedrock rejects empty/null
							// toolUse.input, so normalize to an empty JSON object.
							raw = "{}"
						}
						input := json.RawMessage(raw)
						toolCalls = append(toolCalls, pendingToolCall{
							id:               currentToolID,
							name:             currentToolName,
							input:            input,
							thoughtSignature: currentToolSig,
						})
						currentToolName = ""
						currentToolID = ""
						currentToolInput.Reset()
					}

				case provider.UsageEvent:
					a.recordTokenUsage(e.InputTokens, e.OutputTokens, e.CacheReadTokens, e.CacheWriteTokens, step)

				case provider.StreamErrorEvent:
					// Record and keep draining until the channel closes so the
					// producer goroutine is not leaked.
					streamErr = e.Err

				case provider.MessageStopEvent:
					// Stream complete for this round
				}
			}

			if streamErr == nil {
				break
			}

			if attempt < maxStreamRetries && ctx.Err() == nil && provider.IsTransientStreamError(streamErr) {
				delay := provider.RetryDelay(attempt, streamErr)
				if a.verbose {
					log.Printf("[stream] transient error (attempt %d/%d), retrying in %s: %v",
						attempt+1, maxStreamRetries, delay.Round(time.Second), streamErr)
				}
				if cb != nil {
					reason := "stream interrupted"
					if provider.IsThrottleError(streamErr) {
						reason = "rate limited"
					}
					cb(StreamEvent{Type: "text", Text: fmt.Sprintf(
						"\n[%s — retrying %d/%d in %s]\n", reason, attempt+1, maxStreamRetries, delay.Round(time.Second))})
				}
				select {
				case <-ctx.Done():
					return textBuf.String(), ctx.Err()
				case <-time.After(delay):
				}
				continue
			}

			if cb != nil {
				cb(StreamEvent{Type: "error", Error: streamErr})
			}
			return textBuf.String(), streamErr
		}

		// Build assistant message for history
		var assistBlocks []provider.ContentBlock
		if reasoningBuf.Len() > 0 {
			assistBlocks = append(assistBlocks, provider.ReasoningBlock{
				Text:      reasoningBuf.String(),
				Signature: currentToolSig,
			})
		}
		if textBuf.Len() > 0 {
			assistBlocks = append(assistBlocks, provider.TextBlock{Text: textBuf.String()})
		}
		for _, tc := range toolCalls {
			assistBlocks = append(assistBlocks, provider.ToolUseBlock{
				ToolUseID:        tc.id,
				Name:             tc.name,
				Input:            tc.input,
				ThoughtSignature: tc.thoughtSignature,
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
				log.Printf("[tool] %s id=%s input=%s sig=%s (%dms)", tc.name, tc.id, tc.input, tc.thoughtSignature, duration.Milliseconds())
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

			// Update state dictionary with ground-truth from this tool execution
			sd.recordToolResult(tc.name, string(tc.input), result, isErr)

			resultBlocks = append(resultBlocks, provider.ToolResultBlock{
				ToolUseID: tc.id,
				Name:      tc.name,
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

		// Compact old tool results to keep context window manageable for small models
		if a.needsGroundingAssist() {
			var summaries []string
			a.provHistory, _, summaries = compact.CompactProviderIfNeeded(
				a.provHistory, a.turn, 0, 0, compact.SmallModelConfig(),
			)
			if len(summaries) > 0 && a.opticStore != nil && a.opticStore.IsAvailable() {
				// Inject the summaries into the Optic Lobe as Insights to preserve the knowledge graph
				for _, sum := range summaries {
					emb, _ := embedding.Generate(ctx, sum)
					if emb == nil {
						emb = make([]float32, 768) // Fallback zero vector
					}
					_, _ = a.opticStore.AddInsight(ctx, "global", opticlobe.InsightData{
						Content:   "Compacted memory trace: " + sum,
						Embedding: emb,
					})
				}
			}
		}

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

// needsGroundingAssist returns true if the current provider likely benefits from
// periodic grounding reminders. Large frontier models (Opus 4.6, etc.) have strong
// enough internal state tracking that injecting grounding messages actually hurts —
// the model treats the injected user message as a conversation turn boundary and
// "resets", often re-introducing itself. Smaller/local models (Gemma 4, Llama 3,
// Qwen 3, etc.) genuinely drift without these reminders.
//
// Heuristic: if the provider implements ContextWindowProvider and reports a context
// window > 512k tokens, it's likely a frontier model that doesn't need help.
// Models with unknown or smaller context windows get grounding assistance.
func (a *Agent) needsGroundingAssist() bool {
	if a.provider == nil {
		return false
	}

	// Check if the provider reports a large context window (frontier model indicator)
	if cwp, ok := a.provider.(provider.ContextWindowProvider); ok {
		if cwp.ContextWindow() > 512_000 {
			return false // Large model — doesn't need grounding injection
		}
	}

	return true // Small/local model or unknown — inject grounding
}

// flushSessionProvider persists session state for provider-based conversations.
// Converts provider-agnostic messages to the session serializable format for
// full history persistence, enabling session resume across restarts.
func (a *Agent) flushSessionProvider() {
	if a.store == nil || a.sessionID == "" {
		return
	}

	// Marshal provider history to session format
	messages, err := session.MarshalProviderHistory(a.provHistory)
	if err != nil {
		if a.verbose {
			log.Printf("[session] warning: failed to marshal provider history: %v", err)
		}
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
		Meta:     meta,
		Messages: messages,
		Todos:    a.todos.Items(),
		Inkwell:  a.inkwell,
		Audit:    a.AuditTrail(),
	}

	if err := a.store.Save(a.sessionID, state); err != nil {
		if a.verbose {
			log.Printf("[session] warning: failed to save session: %v", err)
		}
	}

	a.dirty = false
}
