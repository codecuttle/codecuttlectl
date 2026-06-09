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
		{"25% used (50k)", 10000, 35000, 5000, 25},
		{"50% used (100k)", 20000, 70000, 10000, 50},
		{"100% used (200k)", 40000, 140000, 20000, 100},
		{"small session", 5000, 0, 13000, 9}, // 18000/200000 = 9%
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctxUsed := tt.input + tt.cacheRead + tt.cacheWrite
			got := 0
			if ctxUsed > 0 {
				got = int(float64(ctxUsed) / float64(contextWindowSize) * 100)
			}
			if got != tt.wantPct {
				t.Errorf("context window %% = %d, want %d", got, tt.wantPct)
			}
		})
	}
}
