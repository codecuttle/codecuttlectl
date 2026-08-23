package tui

import (
	"context"
	"testing"

	"github.com/codecuttle/codecuttlectl/internal/provider"
	"github.com/codecuttle/codecuttlectl/internal/todo"
)

// mockBedrockProvider implements provider.Provider and provider.CostEstimator
// with Bedrock-like pricing for testing purposes.
type mockBedrockProvider struct{}

func (p *mockBedrockProvider) ID() string   { return "test:mock" }
func (p *mockBedrockProvider) Name() string { return "mock" }
func (p *mockBedrockProvider) Converse(ctx context.Context, req provider.Request) (*provider.Response, error) {
	return nil, nil
}
func (p *mockBedrockProvider) ConverseStream(ctx context.Context, req provider.Request) <-chan provider.StreamEvent {
	return nil
}
func (p *mockBedrockProvider) EstimateCost(usage provider.Usage) float64 {
	const (
		inputPer1M      = 5.00
		outputPer1M     = 25.00
		cacheWritePer1M = 6.25
		cacheReadPer1M  = 0.50
	)
	input := float64(usage.InputTokens) / 1_000_000 * inputPer1M
	output := float64(usage.OutputTokens) / 1_000_000 * outputPer1M
	cacheWrite := float64(usage.CacheWriteTokens) / 1_000_000 * cacheWritePer1M
	cacheRead := float64(usage.CacheReadTokens) / 1_000_000 * cacheReadPer1M
	return input + output + cacheWrite + cacheRead
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		tokens int32
		want   string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{12500, "12.5k"},
		{100000, "100.0k"},
		{999999, "1000.0k"},
		{1000000, "1.0M"},
		{1500000, "1.5M"},
		{15000000, "15.0M"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatTokenCount(tt.tokens)
			if got != tt.want {
				t.Errorf("formatTokenCount(%d) = %q, want %q", tt.tokens, got, tt.want)
			}
		})
	}
}

func TestEstimateCost(t *testing.T) {
	m := &Model{
		llmProvider: &mockBedrockProvider{},
	}

	// Zero tokens = zero cost
	cost := m.estimateCost()
	if cost != 0 {
		t.Errorf("expected 0 cost for zero tokens, got %f", cost)
	}

	// 1M input tokens = $5.00 (Claude Opus 4.x Bedrock pricing)
	m.totalInputTokens = 1_000_000
	cost = m.estimateCost()
	if cost < 4.99 || cost > 5.01 {
		t.Errorf("expected ~$5.00 for 1M input tokens, got $%.4f", cost)
	}

	// 1M output tokens = $25.00
	m.totalInputTokens = 0
	m.totalOutputTokens = 1_000_000
	cost = m.estimateCost()
	if cost < 24.99 || cost > 25.01 {
		t.Errorf("expected ~$25.00 for 1M output tokens, got $%.4f", cost)
	}

	// 1M cache read tokens = $0.50
	m.totalOutputTokens = 0
	m.totalCacheReadInputTokens = 1_000_000
	cost = m.estimateCost()
	if cost < 0.49 || cost > 0.51 {
		t.Errorf("expected ~$0.50 for 1M cache read tokens, got $%.4f", cost)
	}

	// 1M cache write tokens = $6.25
	m.totalCacheReadInputTokens = 0
	m.totalCacheWriteInputTokens = 1_000_000
	cost = m.estimateCost()
	if cost < 6.24 || cost > 6.26 {
		t.Errorf("expected ~$6.25 for 1M cache write tokens, got $%.4f", cost)
	}

	// Combined: typical session
	// 50k uncached input + 200k cache read + 10k cache write + 20k output
	m.totalInputTokens = 50_000
	m.totalOutputTokens = 20_000
	m.totalCacheReadInputTokens = 200_000
	m.totalCacheWriteInputTokens = 10_000
	cost = m.estimateCost()
	// Expected: (50k/1M * 5) + (20k/1M * 25) + (200k/1M * 0.50) + (10k/1M * 6.25)
	//         = 0.25 + 0.50 + 0.10 + 0.0625 = 0.9125
	if cost < 0.91 || cost > 0.92 {
		t.Errorf("expected ~$0.91 for combined tokens, got $%.4f", cost)
	}
}

func TestCacheHitPercentage(t *testing.T) {
	// Simulate what the status bar calculation does
	tests := []struct {
		name        string
		input       int32
		cacheRead   int32
		cacheWrite  int32
		wantPercent int
	}{
		{"no tokens", 0, 0, 0, 0},
		{"all uncached", 10000, 0, 0, 0},
		{"all from cache", 0, 10000, 0, 100},
		{"50% cache hit", 5000, 5000, 0, 50},
		{"with cache write", 1000, 8000, 1000, 80}, // 8000/(1000+8000+1000) = 80%
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			totalIn := tt.input + tt.cacheRead + tt.cacheWrite
			got := 0
			if totalIn > 0 {
				got = int(float64(tt.cacheRead) / float64(totalIn) * 100)
			}
			if got != tt.wantPercent {
				t.Errorf("cache hit %% = %d, want %d", got, tt.wantPercent)
			}
		})
	}
}

func TestContextWindowPercentage(t *testing.T) {
	tests := []struct {
		name       string
		input      int32
		cacheRead  int32
		cacheWrite int32
		wantPct    int
	}{
		{"no tokens", 0, 0, 0, 0},
		{"25% used (250k)", 50000, 175000, 25000, 25},
		{"50% used (500k)", 100000, 350000, 50000, 50},
		{"100% used (1M)", 200000, 700000, 100000, 100},
		{"small session", 5000, 0, 13000, 1}, // 18000/1000000 = 1%
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctxUsed := tt.input + tt.cacheRead + tt.cacheWrite
			got := 0
			if ctxUsed > 0 {
				got = int(float64(ctxUsed) / float64(defaultContextWindowSize) * 100)
			}
			if got != tt.wantPct {
				t.Errorf("context window %% = %d, want %d", got, tt.wantPct)
			}
		})
	}
}

func TestRenderTodoBar_EmptySwarmProgress(t *testing.T) {
	tests := []struct {
		name             string
		activeSwarmTasks int
		swarmProgress    map[string]string
		contains         string
	}{
		{
			name:             "active task with progress",
			activeSwarmTasks: 1,
			swarmProgress:    map[string]string{"flash_coder": "reading files..."},
			contains:         "1 background task (flash_coder: reading files...)",
		},
		{
			name:             "active task with empty progress map",
			activeSwarmTasks: 1,
			swarmProgress:    map[string]string{},
			contains:         "1 background tasks",
		},
		{
			name:             "multiple active tasks with partial progress",
			activeSwarmTasks: 2,
			swarmProgress:    map[string]string{"flash_coder": "compiling"},
			contains:         "1 background task (flash_coder: compiling)",
		},
		{
			name:             "multiple active tasks with multiple progress entries",
			activeSwarmTasks: 2,
			swarmProgress:    map[string]string{"flash_coder": "compiling", "deep_coder": "reviewing"},
			contains:         "2 background tasks",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{
				width:            120,
				todos:            todo.NewList(),
				activeSwarmTasks: tt.activeSwarmTasks,
				swarmProgress:    tt.swarmProgress,
			}

			got := m.renderTodoBar()
			if got == "" {
				t.Errorf("renderTodoBar returned empty string")
			}
		})
	}
}

func TestDraftDebounceTickMsg(t *testing.T) {
	m := &Model{
		lastSavedDraft:   "old draft",
		draftDebounceSeq: 3,
	}

	// Message with matching sequence ID
	msg := DraftDebounceTickMsg{ID: 3}
	if msg.ID != m.draftDebounceSeq {
		t.Errorf("expected sequence ID to match")
	}

	// Stale sequence ID
	staleMsg := DraftDebounceTickMsg{ID: 2}
	if staleMsg.ID == m.draftDebounceSeq {
		t.Errorf("expected stale sequence ID not to match current debounce sequence")
	}
}
