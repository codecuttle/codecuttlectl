package openrouter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codecuttle/codecuttlectl/internal/provider"
)

func TestConverse_Basic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		// Verify request body
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if req.Model != "qwen/qwen3.8-max" {
			t.Errorf("model=%q, want qwen/qwen3.8-max", req.Model)
		}
		if req.Stream {
			t.Error("expected stream=false for non-streaming")
		}
		if len(req.Messages) != 2 { // system + user
			t.Errorf("messages=%d, want 2", len(req.Messages))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatCompletion{
			ID:    "test-1",
			Model: "qwen/qwen3.8-max",
			Choices: []chatChoice{
				{
					Index:        0,
					Message:      chatMessage{Role: "assistant", Content: "hello!"},
					FinishReason: "stop",
				},
			},
			Usage: &chatUsage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		})
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, Model: "qwen/qwen3.8-max"})

	resp, err := client.Converse(context.Background(), provider.Request{
		System: "You are helpful.",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentBlock{
				provider.TextBlock{Text: "hi"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if resp.Content != "hello!" {
		t.Errorf("content=%q, want hello!", resp.Content)
	}
	if resp.StopReason != "stop" {
		t.Errorf("stop=%q, want stop", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("input_tokens=%d, want 10", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("output_tokens=%d, want 5", resp.Usage.OutputTokens)
	}
}

func TestBuildRequest_ZDRAndFallbacks(t *testing.T) {
	c := New(Config{
		Model:      "qwen/qwen3.8-max",
		Fallbacks:  []string{"anthropic/claude-3-5-sonnet"},
		EnforceZDR: true,
	})

	body := c.buildRequest(provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "hi"}}},
		},
	}, false)

	var req chatRequest
	json.Unmarshal(body, &req)

	if req.Provider == nil {
		t.Fatalf("expected Provider settings, got nil")
	}
	if !req.Provider.ZDR {
		t.Errorf("expected ZDR=true")
	}
	if req.Provider.DataCollection != "deny" {
		t.Errorf("expected DataCollection=deny, got %q", req.Provider.DataCollection)
	}
	if len(req.Models) != 1 {
		t.Fatalf("models len=%d, want 1", len(req.Models))
	}
	if req.Models[0] != "anthropic/claude-3-5-sonnet" {
		t.Errorf("expected fallback in Models, got %v", req.Models)
	}
}

func TestConverseStream_ToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		chunks := []string{
			// Tool call starts
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"qwen/qwen3.8-max","choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"id":"call_xyz","index":0,"type":"function","function":{"name":"list_dir","arguments":""}}]},"finish_reason":null}]}`,
			// Tool call arguments streamed
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"qwen/qwen3.8-max","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","function":{"arguments":"{\"path\""}}]},"finish_reason":null}]}`,
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"qwen/qwen3.8-max","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","function":{"arguments":":\".\"}"}}]},"finish_reason":null}]}`,
			// Finish
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"qwen/qwen3.8-max","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		}

		for _, chunk := range chunks {
			io.WriteString(w, "data: "+chunk+"\n\n")
			flusher.Flush()
		}
		io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, Model: "qwen/qwen3.8-max"})

	ch := client.ConverseStream(context.Background(), provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentBlock{
				provider.TextBlock{Text: "list files"},
			}},
		},
		Tools: []provider.ToolDefinition{
			{Name: "list_dir", Description: "List files", InputSchema: json.RawMessage(`{}`)},
		},
	})

	var toolName, toolID string
	var toolInput strings.Builder
	var gotStart, gotStop, gotMsgStop bool

	for ev := range ch {
		switch e := ev.(type) {
		case provider.ToolUseStartEvent:
			gotStart = true
			toolName = e.Name
			toolID = e.ToolUseID
		case provider.ToolInputDeltaEvent:
			toolInput.WriteString(e.Delta)
		case provider.ToolUseStopEvent:
			gotStop = true
		case provider.MessageStopEvent:
			gotMsgStop = true
			if e.StopReason != "tool_calls" {
				t.Errorf("stop=%q, want tool_calls", e.StopReason)
			}
		case provider.StreamErrorEvent:
			t.Fatalf("unexpected error: %v", e.Err)
		}
	}

	if !gotStart {
		t.Error("expected ToolUseStartEvent")
	}
	if toolName != "list_dir" {
		t.Errorf("tool_name=%q, want list_dir", toolName)
	}
	if toolID != "call_xyz" {
		t.Errorf("tool_id=%q, want call_xyz", toolID)
	}
	if toolInput.String() != `{"path":"."}` {
		t.Errorf("tool_input=%q, want {\"path\":\".\"}", toolInput.String())
	}
	if !gotStop {
		t.Error("expected ToolUseStopEvent")
	}
	if !gotMsgStop {
		t.Error("expected MessageStopEvent")
	}
}
