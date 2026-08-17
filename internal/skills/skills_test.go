package skills

import (
	"strings"
	"testing"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
)

// --- Trigger Parsing Tests ---

func TestParseTriggerAlways(t *testing.T) {
	te := ParseTrigger("always")
	if len(te) != 1 || te[0].Kind != TriggerAlways {
		t.Errorf("expected TriggerAlways, got %+v", te)
	}
}

func TestParseTriggerOnRequest(t *testing.T) {
	te := ParseTrigger("on_request")
	if len(te) != 1 || te[0].Kind != TriggerOnRequest {
		t.Errorf("expected TriggerOnRequest, got %+v", te)
	}
}

func TestParseTriggerOnError(t *testing.T) {
	te := ParseTrigger("on_error:compile")
	if len(te) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(te))
	}
	if te[0].Kind != TriggerOnError || te[0].Value != "compile" {
		t.Errorf("expected on_error:compile, got %+v", te[0])
	}
}

func TestParseTriggerOnErrorWildcard(t *testing.T) {
	te := ParseTrigger("on_error:*")
	if te[0].Kind != TriggerOnError || te[0].Value != "*" {
		t.Errorf("expected on_error:*, got %+v", te[0])
	}
}

func TestParseTriggerCombined(t *testing.T) {
	te := ParseTrigger("on_error:compile|on_language:go")
	if len(te) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(te))
	}
	if te[0].Kind != TriggerOnError || te[0].Value != "compile" {
		t.Errorf("condition 0: expected on_error:compile, got %+v", te[0])
	}
	if te[1].Kind != TriggerOnLang || te[1].Value != "go" {
		t.Errorf("condition 1: expected on_language:go, got %+v", te[1])
	}
}

func TestParseTriggerAllKinds(t *testing.T) {
	tests := []struct {
		input string
		kind  TriggerKind
		value string
	}{
		{"always", TriggerAlways, ""},
		{"on_request", TriggerOnRequest, ""},
		{"on_loop", TriggerOnLoop, ""},
		{"on_error:syntax", TriggerOnError, "syntax"},
		{"on_tool:bash_exec", TriggerOnTool, "bash_exec"},
		{"on_file:*.go", TriggerOnFile, "*.go"},
		{"on_file:Dockerfile", TriggerOnFile, "Dockerfile"},
		{"on_language:python", TriggerOnLang, "python"},
		{"on_turn:first", TriggerOnTurn, "first"},
	}

	for _, tt := range tests {
		te := ParseTrigger(tt.input)
		if len(te) != 1 {
			t.Errorf("%s: expected 1 condition, got %d", tt.input, len(te))
			continue
		}
		if te[0].Kind != tt.kind {
			t.Errorf("%s: expected kind %v, got %v", tt.input, tt.kind, te[0].Kind)
		}
		if te[0].Value != tt.value {
			t.Errorf("%s: expected value %q, got %q", tt.input, tt.value, te[0].Value)
		}
	}
}

func TestParseTriggerEmpty(t *testing.T) {
	te := ParseTrigger("")
	if len(te) != 1 || te[0].Kind != TriggerOnRequest {
		t.Errorf("empty trigger should default to on_request, got %+v", te)
	}
}

// --- Trigger Evaluation Tests ---

func TestMatchAlways(t *testing.T) {
	te := ParseTrigger("always")
	ctx := Context{} // Empty context
	if !te.Matches(ctx) {
		t.Error("'always' should match with empty context")
	}
}

func TestMatchOnErrorSpecific(t *testing.T) {
	te := ParseTrigger("on_error:compile")

	ctx := Context{RecentErrors: []string{"compile"}}
	if !te.Matches(ctx) {
		t.Error("on_error:compile should match when compile error present")
	}

	ctx2 := Context{RecentErrors: []string{"syntax"}}
	if te.Matches(ctx2) {
		t.Error("on_error:compile should NOT match when only syntax error present")
	}
}

func TestMatchOnErrorWildcard(t *testing.T) {
	te := ParseTrigger("on_error:*")

	ctx := Context{RecentErrors: []string{"anything"}}
	if !te.Matches(ctx) {
		t.Error("on_error:* should match any error")
	}

	ctx2 := Context{RecentErrors: nil}
	if te.Matches(ctx2) {
		t.Error("on_error:* should NOT match when no errors")
	}
}

func TestMatchOnTool(t *testing.T) {
	te := ParseTrigger("on_tool:bash_exec")

	ctx := Context{RecentTools: []string{"read_file", "bash_exec"}}
	if !te.Matches(ctx) {
		t.Error("on_tool:bash_exec should match when bash_exec was used")
	}

	ctx2 := Context{RecentTools: []string{"read_file"}}
	if te.Matches(ctx2) {
		t.Error("on_tool:bash_exec should NOT match when only read_file was used")
	}
}

func TestMatchOnFile(t *testing.T) {
	te := ParseTrigger("on_file:*.go")

	ctx := Context{RecentFiles: []string{"/home/user/main.go"}}
	if !te.Matches(ctx) {
		t.Error("on_file:*.go should match .go files")
	}

	ctx2 := Context{RecentFiles: []string{"/home/user/script.py"}}
	if te.Matches(ctx2) {
		t.Error("on_file:*.go should NOT match .py files")
	}
}

func TestMatchOnFileExact(t *testing.T) {
	te := ParseTrigger("on_file:Dockerfile")

	ctx := Context{RecentFiles: []string{"/project/Dockerfile"}}
	if !te.Matches(ctx) {
		t.Error("on_file:Dockerfile should match Dockerfile")
	}
}

func TestMatchOnLanguage(t *testing.T) {
	te := ParseTrigger("on_language:python")

	ctx := Context{DetectedLang: "python"}
	if !te.Matches(ctx) {
		t.Error("on_language:python should match")
	}

	ctx2 := Context{DetectedLang: "go"}
	if te.Matches(ctx2) {
		t.Error("on_language:python should NOT match go")
	}
}

func TestMatchOnTurnFirst(t *testing.T) {
	te := ParseTrigger("on_turn:first")

	ctx := Context{IsFirstTurn: true}
	if !te.Matches(ctx) {
		t.Error("on_turn:first should match on first turn")
	}

	ctx2 := Context{IsFirstTurn: false}
	if te.Matches(ctx2) {
		t.Error("on_turn:first should NOT match on subsequent turns")
	}
}

func TestMatchOnLoop(t *testing.T) {
	te := ParseTrigger("on_loop")

	ctx := Context{IsLooping: true}
	if !te.Matches(ctx) {
		t.Error("on_loop should match when looping")
	}

	ctx2 := Context{IsLooping: false}
	if te.Matches(ctx2) {
		t.Error("on_loop should NOT match when not looping")
	}
}

func TestMatchCombinedOR(t *testing.T) {
	te := ParseTrigger("on_error:compile|on_language:go")

	// Either condition should fire it
	ctx1 := Context{RecentErrors: []string{"compile"}, DetectedLang: "python"}
	if !te.Matches(ctx1) {
		t.Error("should match on error alone")
	}

	ctx2 := Context{RecentErrors: nil, DetectedLang: "go"}
	if !te.Matches(ctx2) {
		t.Error("should match on language alone")
	}

	ctx3 := Context{RecentErrors: []string{"compile"}, DetectedLang: "go"}
	if !te.Matches(ctx3) {
		t.Error("should match when both conditions true")
	}

	ctx4 := Context{RecentErrors: []string{"syntax"}, DetectedLang: "python"}
	if te.Matches(ctx4) {
		t.Error("should NOT match when neither condition is true")
	}
}

func TestMatchOnRequest(t *testing.T) {
	te := ParseTrigger("on_request")

	ctx := Context{}
	if te.Matches(ctx) {
		t.Error("on_request should NOT match without explicit request")
	}

	ctx2 := Context{ExplicitRequest: "my_skill"}
	if !te.Matches(ctx2) {
		t.Error("on_request should match when explicitly requested")
	}
}

// --- Relevance Scoring Tests ---

func TestRelevanceScoring(t *testing.T) {
	ctx := Context{
		RecentErrors: []string{"compile"},
		DetectedLang: "go",
		IsLooping:    true,
	}

	// on_loop should score highest after on_request
	loopTrigger := ParseTrigger("on_loop")
	errorTrigger := ParseTrigger("on_error:compile")
	alwaysTrigger := ParseTrigger("always")

	loopScore := loopTrigger.Relevance(ctx)
	errorScore := errorTrigger.Relevance(ctx)
	alwaysScore := alwaysTrigger.Relevance(ctx)

	if loopScore <= errorScore {
		t.Errorf("on_loop (%d) should score higher than on_error (%d)", loopScore, errorScore)
	}
	if errorScore <= alwaysScore {
		t.Errorf("on_error (%d) should score higher than always (%d)", errorScore, alwaysScore)
	}
}

// --- Registry Tests ---

func TestRegistryRegisterAndEvaluate(t *testing.T) {
	reg := NewRegistry(DefaultBudget)

	reg.Register("my-plugin", "1.0.0", []*pb.Skill{
		{
			Name:            "always_skill",
			Trigger:         "always",
			ContentType:     "markdown",
			Content:         "Always active content.",
			Priority:        10,
			EstimatedTokens: 10,
		},
		{
			Name:            "error_skill",
			Trigger:         "on_error:compile",
			ContentType:     "workflow",
			Content:         "Fix compile errors by...",
			Priority:        50,
			EstimatedTokens: 20,
		},
		{
			Name:            "hidden_skill",
			Trigger:         "on_request",
			ContentType:     "knowledge",
			Content:         "Advanced knowledge...",
			Priority:        30,
			EstimatedTokens: 100,
		},
	})

	if reg.Count() != 3 {
		t.Fatalf("expected 3 skills, got %d", reg.Count())
	}

	// With compile error context, both always_skill and error_skill should match
	ctx := Context{RecentErrors: []string{"compile"}}
	matched := reg.Evaluate(ctx)

	if len(matched) != 2 {
		t.Fatalf("expected 2 matched skills, got %d", len(matched))
	}

	// error_skill should rank higher (higher relevance + priority)
	if matched[0].Skill.Name != "error_skill" {
		t.Errorf("expected error_skill first, got %s", matched[0].Skill.Name)
	}
}

func TestRegistryRenderWithBudget(t *testing.T) {
	reg := NewRegistry(50) // Tight budget: 50 tokens

	reg.Register("plugin", "1.0.0", []*pb.Skill{
		{
			Name:            "small",
			Trigger:         "always",
			Content:         "Small content.",
			Priority:        50,
			EstimatedTokens: 10,
		},
		{
			Name:            "large",
			Trigger:         "always",
			Content:         strings.Repeat("x", 1000), // Way over budget
			Priority:        90,
			EstimatedTokens: 250,
		},
	})

	ctx := Context{}
	matched := reg.Evaluate(ctx)
	rendered := reg.Render(matched)

	// Only small should fit in the budget
	if !strings.Contains(rendered, "Small content.") {
		t.Error("expected small skill to be rendered")
	}
	if strings.Contains(rendered, strings.Repeat("x", 100)) {
		t.Error("large skill should NOT be rendered (over budget)")
	}
}

func TestRegistryGetByName(t *testing.T) {
	reg := NewRegistry(DefaultBudget)
	reg.Register("plugin", "1.0.0", []*pb.Skill{
		{Name: "target_skill", Trigger: "on_request", Content: "found it"},
	})

	skill, ok := reg.GetByName("target_skill")
	if !ok {
		t.Fatal("expected to find target_skill")
	}
	if skill.Skill.Content != "found it" {
		t.Errorf("unexpected content: %q", skill.Skill.Content)
	}

	_, ok = reg.GetByName("nonexistent")
	if ok {
		t.Error("should not find nonexistent skill")
	}
}

func TestRegistryWeightedOrdering(t *testing.T) {
	reg := NewRegistry(DefaultBudget)

	reg.Register("plugin", "1.0.0", []*pb.Skill{
		{Name: "low_priority_high_relevance", Trigger: "on_loop", Priority: 10, Content: "a", EstimatedTokens: 5},
		{Name: "high_priority_low_relevance", Trigger: "always", Priority: 90, Content: "b", EstimatedTokens: 5},
	})

	// In a looping context, on_loop should score higher despite lower priority
	ctx := Context{IsLooping: true}
	matched := reg.Evaluate(ctx)

	if len(matched) != 2 {
		t.Fatalf("expected 2 matched, got %d", len(matched))
	}

	// on_loop (relevance 90 * 2 + priority 10 = 190) vs always (relevance 10 * 2 + priority 90 = 110)
	if matched[0].Skill.Name != "low_priority_high_relevance" {
		t.Errorf("expected on_loop skill first (higher relevance), got %s (score %d vs %d)",
			matched[0].Skill.Name, matched[0].Score, matched[1].Score)
	}
}

func TestIsOnRequestOnly(t *testing.T) {
	te := ParseTrigger("on_request")
	if !te.IsOnRequestOnly() {
		t.Error("on_request should be request-only")
	}

	te2 := ParseTrigger("on_error:compile|on_request")
	if te2.IsOnRequestOnly() {
		t.Error("combined trigger should NOT be request-only")
	}

	te3 := ParseTrigger("always")
	if te3.IsOnRequestOnly() {
		t.Error("always should NOT be request-only")
	}
}
