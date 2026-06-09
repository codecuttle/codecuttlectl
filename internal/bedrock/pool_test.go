package bedrock

import (
	"testing"
)

func TestResolveModelID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"opus", "us.anthropic.claude-opus-4-6-v1"},
		{"opus-4-6", "us.anthropic.claude-opus-4-6-v1"},
		{"opus-4-8", "us.anthropic.claude-opus-4-8"},
		{"sonnet", "us.anthropic.claude-sonnet-4-6"},
		{"sonnet-4-6", "us.anthropic.claude-sonnet-4-6"},
		{"haiku", "us.anthropic.claude-haiku-4-5-20251001-v1:0"},
		{"haiku-4-5", "us.anthropic.claude-haiku-4-5-20251001-v1:0"},
		// Full IDs pass through unchanged
		{"us.anthropic.claude-opus-4-6-v1", "us.anthropic.claude-opus-4-6-v1"},
		{"some-unknown-model", "some-unknown-model"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ResolveModelID(tt.input)
			if got != tt.want {
				t.Errorf("ResolveModelID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLookupModel(t *testing.T) {
	// Known model
	info := LookupModel("us.anthropic.claude-haiku-4-5-20251001-v1:0")
	if info.DisplayName != "haiku-4-5" {
		t.Errorf("expected DisplayName 'haiku-4-5', got %q", info.DisplayName)
	}
	if info.InputCost != 1.00 {
		t.Errorf("expected InputCost 1.00, got %f", info.InputCost)
	}
	if info.ContextWindow != 200_000 {
		t.Errorf("expected ContextWindow 200000, got %d", info.ContextWindow)
	}

	// Unknown model — should get conservative defaults
	info = LookupModel("us.meta.llama-3-70b")
	if info.ModelID != "us.meta.llama-3-70b" {
		t.Errorf("expected ModelID preserved, got %q", info.ModelID)
	}
	if info.InputCost != 5.00 {
		t.Errorf("expected conservative InputCost 5.00, got %f", info.InputCost)
	}
	if info.SupportsCache != false {
		t.Error("expected SupportsCache=false for unknown model")
	}
}

func TestDefaultRoles(t *testing.T) {
	tests := []struct {
		name      string
		primary   string
		wantAux   string
		wantPlan  string
	}{
		{
			"opus primary gets haiku aux + sonnet plan",
			"us.anthropic.claude-opus-4-6-v1",
			"us.anthropic.claude-haiku-4-5-20251001-v1:0",
			"us.anthropic.claude-sonnet-4-6",
		},
		{
			"opus-4-8 gets haiku aux + sonnet plan",
			"us.anthropic.claude-opus-4-8",
			"us.anthropic.claude-haiku-4-5-20251001-v1:0",
			"us.anthropic.claude-sonnet-4-6",
		},
		{
			"sonnet primary gets haiku aux + self as plan",
			"us.anthropic.claude-sonnet-4-6",
			"us.anthropic.claude-haiku-4-5-20251001-v1:0",
			"us.anthropic.claude-sonnet-4-6",
		},
		{
			"haiku primary gets self for all roles",
			"us.anthropic.claude-haiku-4-5-20251001-v1:0",
			"us.anthropic.claude-haiku-4-5-20251001-v1:0",
			"us.anthropic.claude-haiku-4-5-20251001-v1:0",
		},
		{
			"unknown model gets haiku aux + sonnet plan",
			"us.meta.llama-3-70b",
			"us.anthropic.claude-haiku-4-5-20251001-v1:0",
			"us.anthropic.claude-sonnet-4-6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roles := DefaultRoles(tt.primary)
			if roles.Auxiliary != tt.wantAux {
				t.Errorf("Auxiliary = %q, want %q", roles.Auxiliary, tt.wantAux)
			}
			if roles.Planning != tt.wantPlan {
				t.Errorf("Planning = %q, want %q", roles.Planning, tt.wantPlan)
			}
			if roles.Primary != tt.primary {
				t.Errorf("Primary = %q, want %q", roles.Primary, tt.primary)
			}
		})
	}
}
