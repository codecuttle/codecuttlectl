package swarm

import (
	"context"
	"fmt"
	"testing"

	"github.com/codecuttle/codecuttlectl/internal/provider"
)

type mockProvider struct {
	id string
}

func (m *mockProvider) ID() string   { return m.id }
func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Converse(ctx context.Context, req provider.Request) (*provider.Response, error) {
	return nil, nil
}
func (m *mockProvider) ConverseStream(ctx context.Context, req provider.Request) <-chan provider.StreamEvent {
	return nil
}
func (m *mockProvider) ContextWindow() int32                      { return 1000 }
func (m *mockProvider) EstimateCost(usage provider.Usage) float64 { return 1.5 }

func mockFactory(ctx context.Context, providerName, modelID string) (provider.Provider, error) {
	if providerName == "error" {
		return nil, fmt.Errorf("factory error")
	}
	return &mockProvider{id: providerName + "-" + modelID}, nil
}

func TestNewPool(t *testing.T) {
	morph := &Morphology{
		Nodes: map[string]Node{
			"orchestrator": {
				Provider:  "bedrock",
				Model:     "opus",
				IsPrimary: true,
			},
			"auxiliary": {
				Provider: "google",
				Model:    "gemini",
			},
			"reviewer": {
				Provider: "ollama",
				Model:    "qwen",
			},
		},
	}

	pool, err := NewPool(context.Background(), morph, mockFactory)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pool.Primary().(*mockProvider).id != "bedrock-opus" {
		t.Errorf("wrong primary: %v", pool.Primary().(*mockProvider).id)
	}
	if pool.Auxiliary().(*mockProvider).id != "google-gemini" {
		t.Errorf("wrong auxiliary: %v", pool.Auxiliary().(*mockProvider).id)
	}

	// planning should fallback to primary since no "planning" node
	if pool.Planning().(*mockProvider).id != "bedrock-opus" {
		t.Errorf("wrong planning: %v", pool.Planning().(*mockProvider).id)
	}

	info := pool.Info("primary")
	if info.ModelID != "opus" {
		t.Errorf("wrong info ModelID: %v", info.ModelID)
	}
	if info.ContextWindow != 1000 {
		t.Errorf("wrong info ContextWindow: %v", info.ContextWindow)
	}

	cost := pool.EstimateCost("primary", 10, 10, 10, 10)
	if cost != 1.5 {
		t.Errorf("wrong cost: %v", cost)
	}

	rev, ok := pool.GetNode("reviewer")
	if !ok {
		t.Fatal("reviewer not found")
	}
	if rev.(*mockProvider).id != "ollama-qwen" {
		t.Errorf("wrong reviewer: %v", rev.(*mockProvider).id)
	}
}

func TestNewPool_FactoryError(t *testing.T) {
	morph := &Morphology{
		Nodes: map[string]Node{
			"orchestrator": {
				Provider:  "error",
				Model:     "opus",
				IsPrimary: true,
			},
		},
	}

	_, err := NewPool(context.Background(), morph, mockFactory)
	if err == nil {
		t.Fatal("expected error")
	}
}
