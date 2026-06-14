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
	cache  *CacheManager
}

// New creates a new Google GenAI provider.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	client, err := genai.NewClient(ctx, nil)
	if err != nil {
		return nil, err
	}
	p := &Provider{
		client: client,
		config: cfg,
	}
	// Initialize cache manager if threshold is set
	if cfg.CacheThreshold > 0 {
		p.cache = NewCacheManager(client, cfg.Model, cfg.CacheThreshold)
	}
	return p, nil
}

// Close cleans up resources, including deleting any active cache.
func (p *Provider) Close(ctx context.Context) error {
	if p.cache != nil {
		return p.cache.Close(ctx)
	}
	return nil
}

func (p *Provider) ID() string {
	return "google:" + p.config.Model
}

func (p *Provider) Name() string {
	return p.config.Model
}

func (p *Provider) ContextWindow() int32 {
	// Model-specific context windows
	switch p.config.Model {
	case "gemini-2.5-pro", "gemini-2.5-pro-preview-06-05":
		return 1_048_576
	case "gemini-2.5-flash", "gemini-2.5-flash-preview-05-20":
		return 1_048_576
	case "gemini-2.0-flash":
		return 1_048_576
	case "gemini-1.5-pro":
		return 2_000_000
	case "gemini-1.5-flash":
		return 1_000_000
	default:
		return 1_048_576 // safe default for recent models
	}
}

// EstimateCost calculates estimated dollar cost using Gemini pricing.
// Pricing varies by model and by prompt size (< or > 128k tokens).
// For simplicity, we use the <128k tier; the provider can be enhanced
// later for tiered pricing based on actual token counts.
func (p *Provider) EstimateCost(usage provider.Usage) float64 {
	pricing := p.modelPricing()

	input := float64(usage.InputTokens) / 1_000_000 * pricing.inputPer1M
	output := float64(usage.OutputTokens) / 1_000_000 * pricing.outputPer1M
	cacheRead := float64(usage.CacheReadTokens) / 1_000_000 * pricing.cacheReadPer1M

	return input + output + cacheRead
}

type pricingTier struct {
	inputPer1M     float64
	outputPer1M    float64
	cacheReadPer1M float64
}

func (p *Provider) modelPricing() pricingTier {
	switch p.config.Model {
	case "gemini-2.5-pro", "gemini-2.5-pro-preview-06-05":
		// Gemini 2.5 Pro: $1.25/1M in, $10.00/1M out, $0.3125/1M cache read
		return pricingTier{inputPer1M: 1.25, outputPer1M: 10.00, cacheReadPer1M: 0.3125}
	case "gemini-2.5-flash", "gemini-2.5-flash-preview-05-20":
		// Gemini 2.5 Flash: $0.15/1M in, $0.60/1M out (thinking), $0.0375/1M cache read
		return pricingTier{inputPer1M: 0.15, outputPer1M: 0.60, cacheReadPer1M: 0.0375}
	case "gemini-2.0-flash":
		// Gemini 2.0 Flash: $0.10/1M in, $0.40/1M out, $0.025/1M cache read
		return pricingTier{inputPer1M: 0.10, outputPer1M: 0.40, cacheReadPer1M: 0.025}
	case "gemini-1.5-pro":
		// Gemini 1.5 Pro: $1.25/1M in, $5.00/1M out, $0.3125/1M cache read
		return pricingTier{inputPer1M: 1.25, outputPer1M: 5.00, cacheReadPer1M: 0.3125}
	case "gemini-1.5-flash":
		// Gemini 1.5 Flash: $0.075/1M in, $0.30/1M out, $0.01875/1M cache read
		return pricingTier{inputPer1M: 0.075, outputPer1M: 0.30, cacheReadPer1M: 0.01875}
	default:
		// Default to 2.5 Pro pricing (safest assumption for cost tracking)
		return pricingTier{inputPer1M: 1.25, outputPer1M: 10.00, cacheReadPer1M: 0.3125}
	}
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

	// Use cache-aware call path
	resp, err := p.converseWithCache(ctx, req, config)
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

		// Determine if we should use cached content
		var streamConfig *genai.GenerateContentConfig
		if p.cache != nil {
			cacheName, err := p.cache.EnsureCache(ctx, req.System, config.Tools)
			if err != nil {
				log.Printf("[google-cache] EnsureCache error in stream (proceeding uncached): %v", err)
				streamConfig = config
			} else if cacheName != "" {
				// Use cache: strip system/tools, add CachedContent reference
				streamConfig = &genai.GenerateContentConfig{
					CachedContent: cacheName,
				}
				if config.Temperature != nil {
					streamConfig.Temperature = config.Temperature
				}
				if config.MaxOutputTokens > 0 {
					streamConfig.MaxOutputTokens = config.MaxOutputTokens
				}
			} else {
				streamConfig = config
			}
		} else {
			streamConfig = config
		}

		// GenerateContentStream returns iter.Seq2[*GenerateContentResponse, error]
		for resp, err := range p.client.Models.GenerateContentStream(ctx, p.config.Model, contents, streamConfig) {
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
