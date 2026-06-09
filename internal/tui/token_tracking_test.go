package tui

import (
	"testing"
)

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
	m := &Model{}

	// Zero tokens = zero cost
	cost := m.estimateCost()
	if cost != 0 {
		t.Errorf("expected 0 cost for zero tokens, got %f", cost)
	}

	// 1M input tokens = $15.00
	m.totalInputTokens = 1_000_000
	cost = m.estimateCost()
	if cost < 14.99 || cost > 15.01 {
		t.Errorf("expected ~$15.00 for 1M input tokens, got $%.4f", cost)
	}

	// 1M output tokens = $75.00
	m.totalInputTokens = 0
	m.totalOutputTokens = 1_000_000
	cost = m.estimateCost()
	if cost < 74.99 || cost > 75.01 {
		t.Errorf("expected ~$75.00 for 1M output tokens, got $%.4f", cost)
	}

	// 1M cache read tokens = $1.50
	m.totalOutputTokens = 0
	m.totalCacheReadInputTokens = 1_000_000
	cost = m.estimateCost()
	if cost < 1.49 || cost > 1.51 {
		t.Errorf("expected ~$1.50 for 1M cache read tokens, got $%.4f", cost)
	}

	// 1M cache write tokens = $18.75
	m.totalCacheReadInputTokens = 0
	m.totalCacheWriteInputTokens = 1_000_000
	cost = m.estimateCost()
	if cost < 18.74 || cost > 18.76 {
		t.Errorf("expected ~$18.75 for 1M cache write tokens, got $%.4f", cost)
	}

	// Combined: typical session
	// 50k uncached input + 200k cache read + 10k cache write + 20k output
	m.totalInputTokens = 50_000
	m.totalOutputTokens = 20_000
	m.totalCacheReadInputTokens = 200_000
	m.totalCacheWriteInputTokens = 10_000
	cost = m.estimateCost()
	// Expected: (50k/1M * 15) + (20k/1M * 75) + (200k/1M * 1.5) + (10k/1M * 18.75)
	//         = 0.75 + 1.50 + 0.30 + 0.1875 = 2.7375
	if cost < 2.73 || cost > 2.75 {
		t.Errorf("expected ~$2.74 for combined tokens, got $%.4f", cost)
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
