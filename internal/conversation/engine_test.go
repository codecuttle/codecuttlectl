package conversation

import (
	"context"
	"testing"

	"github.com/codecuttle/codecuttlectl/internal/provider"
)

type mockEchoProvider struct{}

func (m *mockEchoProvider) ID() string   { return "mock:echo" }
func (m *mockEchoProvider) Name() string { return "Mock Echo" }

func (m *mockEchoProvider) Converse(ctx context.Context, req provider.Request) (*provider.Response, error) {
	return &provider.Response{
		Content: "Echo: " + req.System,
	}, nil
}

func (m *mockEchoProvider) ConverseStream(ctx context.Context, req provider.Request) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 5)
	go func() {
		defer close(ch)
		ch <- provider.TextDeltaEvent{Text: "Hello from engine!"}
		ch <- provider.MessageStopEvent{StopReason: "end_turn"}
	}()
	return ch
}

func TestEngine_StreamTurnAsync(t *testing.T) {
	agent, err := NewAgent(Config{
		Provider: &mockEchoProvider{},
		WorkDir:  "/tmp",
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	engine := NewEngine(agent)
	if engine.Agent() != agent {
		t.Errorf("engine.Agent() did not match created agent")
	}

	eventCh, err := engine.StreamTurnAsync(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("StreamTurnAsync failed: %v", err)
	}

	var receivedTokens []string
	var doneReceived bool

	for event := range eventCh {
		switch e := event.(type) {
		case EventToken:
			receivedTokens = append(receivedTokens, e.Text)
		case EventTurnDone:
			doneReceived = true
			if e.Error != nil {
				t.Errorf("unexpected error in EventTurnDone: %v", e.Error)
			}
		}
	}

	if !doneReceived {
		t.Errorf("expected EventTurnDone, but none received")
	}
	if len(receivedTokens) == 0 {
		t.Errorf("expected to receive EventToken chunks, got none")
	}
}
