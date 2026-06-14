package googleprov

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/codecuttle/codecuttlectl/internal/provider"
	"google.golang.org/genai"
)

// Config holds Google API configuration.
type Config struct {
	Model          string
	CacheThreshold int
}

// Provider implements provider.Provider for Google GenAI.
type Provider struct {
	client *genai.Client
	config Config
}

// New creates a new Google GenAI provider.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	client, err := genai.NewClient(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Provider{
		client: client,
		config: cfg,
	}, nil
}

func (p *Provider) ID() string {
	return "google:" + p.config.Model
}

func (p *Provider) Name() string {
	return p.config.Model
}

func (p *Provider) ContextWindow() int32 {
	return 2_000_000
}

func (p *Provider) EstimateCost(usage provider.Usage) float64 {
	const (
		inputPer1M  = 1.25
		outputPer1M = 5.00
	)
	input := float64(usage.InputTokens) / 1_000_000 * inputPer1M
	output := float64(usage.OutputTokens) / 1_000_000 * outputPer1M
	return input + output
}

func toGenAITools(tools []provider.ToolDefinition) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}
	var decls []*genai.FunctionDeclaration
	for _, t := range tools {
		var schema map[string]any
		if len(t.InputSchema) > 0 {
			if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
				log.Printf("Warning: failed to unmarshal schema for tool %s: %v", t.Name, err)
			}
		}
		decls = append(decls, &genai.FunctionDeclaration{
			Name:                 t.Name,
			Description:          t.Description,
			ParametersJsonSchema: schema,
		})
	}
	return []*genai.Tool{
		{FunctionDeclarations: decls},
	}
}

func toGenAIContents(msgs []provider.Message) []*genai.Content {
	var out []*genai.Content
	for _, m := range msgs {
		role := string(m.Role)
		if role == "assistant" {
			role = "model"
		}
		var parts []*genai.Part
		for _, b := range m.Content {
			switch b := b.(type) {
			case provider.TextBlock:
				parts = append(parts, &genai.Part{Text: b.Text})
			case provider.ToolUseBlock:
				var args map[string]any
				if len(b.Input) > 0 {
					_ = json.Unmarshal(b.Input, &args)
				}
				parts = append(parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						ID:   b.ToolUseID,
						Name: b.Name,
						Args: args,
					},
				})
			case provider.ToolResultBlock:
				parts = append(parts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						ID:   b.ToolUseID,
						Name: b.Name,
						Response: map[string]any{
							"result":  b.Content,
							"isError": b.IsError,
						},
					},
				})
			}
		}
		out = append(out, &genai.Content{
			Role:  role,
			Parts: parts,
		})
	}
	return out
}

func extractUsage(meta *genai.GenerateContentResponseUsageMetadata) provider.Usage {
	var u provider.Usage
	if meta != nil {
		u.InputTokens = meta.PromptTokenCount
		u.OutputTokens = meta.CandidatesTokenCount
		u.CacheReadTokens = meta.CachedContentTokenCount
	}
	return u
}

func (p *Provider) Converse(ctx context.Context, req provider.Request) (*provider.Response, error) {
	config := &genai.GenerateContentConfig{
		Tools: toGenAITools(req.Tools),
	}
	if req.System != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: req.System}},
		}
	}
	if req.Config.Temperature != nil {
		t := float32(*req.Config.Temperature)
		config.Temperature = &t
	}
	if req.Config.MaxTokens > 0 {
		config.MaxOutputTokens = int32(req.Config.MaxTokens)
	}

	contents := toGenAIContents(req.Messages)
	resp, err := p.client.Models.GenerateContent(ctx, p.config.Model, contents, config)
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates returned")
	}

	cand := resp.Candidates[0]
	var res provider.Response
	res.Usage = extractUsage(resp.UsageMetadata)

	for _, part := range cand.Content.Parts {
		if part.Text != "" {
			res.Content += part.Text
		}
		if part.FunctionCall != nil {
			inputBytes, _ := json.Marshal(part.FunctionCall.Args)
			res.ToolUses = append(res.ToolUses, provider.ToolUseRequest{
				ToolUseID: part.FunctionCall.ID,
				Name:      part.FunctionCall.Name,
				Input:     inputBytes,
			})
		}
	}

	if cand.FinishReason != "" {
		res.StopReason = string(cand.FinishReason)
	}

	return &res, nil
}

func (p *Provider) ConverseStream(ctx context.Context, req provider.Request) <-chan provider.StreamEvent {
	events := make(chan provider.StreamEvent, 64)

	go func() {
		defer close(events)
		config := &genai.GenerateContentConfig{
			Tools: toGenAITools(req.Tools),
		}
		if req.System != "" {
			config.SystemInstruction = &genai.Content{
				Parts: []*genai.Part{{Text: req.System}},
			}
		}
		if req.Config.Temperature != nil {
			t := float32(*req.Config.Temperature)
			config.Temperature = &t
		}
		if req.Config.MaxTokens > 0 {
			config.MaxOutputTokens = int32(req.Config.MaxTokens)
		}

		contents := toGenAIContents(req.Messages)

		// GenerateContentStream returns iter.Seq2[*GenerateContentResponse, error]
		for resp, err := range p.client.Models.GenerateContentStream(ctx, p.config.Model, contents, config) {
			if err != nil {
				events <- provider.StreamErrorEvent{Err: err}
				return
			}

			if len(resp.Candidates) > 0 {
				cand := resp.Candidates[0]
				if cand.Content != nil {
					for _, part := range cand.Content.Parts {
						if part.Text != "" {
							events <- provider.TextDeltaEvent{Text: part.Text}
						}
						if part.FunctionCall != nil {
							// Stream events for function call
							events <- provider.ToolUseStartEvent{
								ToolUseID: part.FunctionCall.ID,
								Name:      part.FunctionCall.Name,
							}

							inputBytes, _ := json.Marshal(part.FunctionCall.Args)
							if len(inputBytes) > 0 && string(inputBytes) != "null" {
								events <- provider.ToolInputDeltaEvent{Delta: string(inputBytes)}
							}

							events <- provider.ToolUseStopEvent{}
						}
					}
				}
				if cand.FinishReason != "" {
					events <- provider.MessageStopEvent{StopReason: string(cand.FinishReason)}
				}
			}

			if resp.UsageMetadata != nil {
				usage := extractUsage(resp.UsageMetadata)
				events <- provider.UsageEvent{
					InputTokens:      usage.InputTokens,
					OutputTokens:     usage.OutputTokens,
					CacheReadTokens:  usage.CacheReadTokens,
					CacheWriteTokens: usage.CacheWriteTokens,
				}
			}
		}
	}()

	return events
}
