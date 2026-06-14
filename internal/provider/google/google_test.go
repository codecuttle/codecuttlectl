package googleprov

import (
	"context"
	"testing"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/provider"
)

func TestCacheManagerBelowThreshold(t *testing.T) {
	// CacheManager should return empty string when content is below threshold
	cm := &CacheManager{
		threshold: 32000,
	}

	// Short system prompt = ~10 tokens, well below 32k threshold
	name, err := cm.EnsureCache(context.Background(), "Hello world", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "" {
		t.Errorf("expected empty cache name for below-threshold content, got %q", name)
	}
}

func TestCacheStatsEmpty(t *testing.T) {
	cm := &CacheManager{
		threshold: 32000,
	}

	stats := cm.Stats()
	if stats.Active {
		t.Error("expected inactive stats for new cache manager")
	}
	if stats.Name != "" {
		t.Errorf("expected empty name, got %q", stats.Name)
	}
}

func TestCacheStatsActive(t *testing.T) {
	expiry := time.Now().Add(5 * time.Minute)
	cm := &CacheManager{
		threshold: 32000,
		cacheName: "test-cache-123",
		expiresAt: expiry,
	}

	stats := cm.Stats()
	if !stats.Active {
		t.Error("expected active stats")
	}
	if stats.Name != "test-cache-123" {
		t.Errorf("expected name 'test-cache-123', got %q", stats.Name)
	}
	if stats.TTL <= 0 {
		t.Errorf("expected positive TTL, got %v", stats.TTL)
	}
}

func TestPricingTiers(t *testing.T) {
	tests := []struct {
		model      string
		wantInput  float64
		wantOutput float64
	}{
		{"gemini-2.5-pro", 1.25, 10.00},
		{"gemini-2.5-flash", 0.15, 0.60},
		{"gemini-2.0-flash", 0.10, 0.40},
		{"gemini-1.5-pro", 1.25, 5.00},
		{"gemini-1.5-flash", 0.075, 0.30},
		{"unknown-model", 1.25, 10.00}, // defaults to 2.5 Pro
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			p := &Provider{config: Config{Model: tt.model}}
			pricing := p.modelPricing()
			if pricing.inputPer1M != tt.wantInput {
				t.Errorf("input pricing = %v, want %v", pricing.inputPer1M, tt.wantInput)
			}
			if pricing.outputPer1M != tt.wantOutput {
				t.Errorf("output pricing = %v, want %v", pricing.outputPer1M, tt.wantOutput)
			}
		})
	}
}

func TestEstimateCostGoogle(t *testing.T) {
	p := &Provider{config: Config{Model: "gemini-2.5-pro"}}

	// 1M input + 1M output + 1M cache read
	usage := provider.Usage{
		InputTokens:     1_000_000,
		OutputTokens:    1_000_000,
		CacheReadTokens: 1_000_000,
	}

	cost := p.EstimateCost(usage)
	// Expected: 1.25 + 10.00 + 0.3125 = 11.5625
	if cost < 11.56 || cost > 11.57 {
		t.Errorf("expected ~$11.56, got $%.4f", cost)
	}
}

func TestContextWindow(t *testing.T) {
	tests := []struct {
		model string
		want  int32
	}{
		{"gemini-2.5-pro", 1_048_576},
		{"gemini-1.5-pro", 2_000_000},
		{"gemini-1.5-flash", 1_000_000},
		{"gemini-2.0-flash", 1_048_576},
		{"some-future-model", 1_048_576},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			p := &Provider{config: Config{Model: tt.model}}
			got := p.ContextWindow()
			if got != tt.want {
				t.Errorf("ContextWindow() = %d, want %d", got, tt.want)
			}
		})
	}
}

// helper to create provider.Usage
func providerUsage(input, output, cacheRead, cacheWrite int32) provider.Usage {
	return provider.Usage{
		InputTokens:      input,
		OutputTokens:     output,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
	}
}
