package ollama

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
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		// Verify request body
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if req.Model != "gemma4:31b" {
			t.Errorf("model=%q, want gemma4:31b", req.Model)
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
			Model: "gemma4:31b",
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

	client := New(Config{BaseURL: server.URL, Model: "gemma4:31b"})

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

func TestConverse_ToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		json.NewDecoder(r.Body).Decode(&req)

		if len(req.Tools) != 1 {
			t.Errorf("tools=%d, want 1", len(req.Tools))
		}
		if req.Tools[0].Function.Name != "list_dir" {
			t.Errorf("tool name=%q, want list_dir", req.Tools[0].Function.Name)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatCompletion{
			ID:    "test-2",
			Model: "gemma4:31b",
			Choices: []chatChoice{
				{
					Index: 0,
					Message: chatMessage{
						Role: "assistant",
						ToolCalls: []oaiToolCall{
							{
								ID:   "call_123",
								Type: "function",
								Function: oaiToolCallFunction{
									Name:      "list_dir",
									Arguments: `{"path":"."}`,
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			},
			Usage: &chatUsage{PromptTokens: 20, CompletionTokens: 15, TotalTokens: 35},
		})
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, Model: "gemma4:31b"})

	resp, err := client.Converse(context.Background(), provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentBlock{
				provider.TextBlock{Text: "list the directory"},
			}},
		},
		Tools: []provider.ToolDefinition{
			{
				Name:        "list_dir",
				Description: "List files",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if len(resp.ToolUses) != 1 {
		t.Fatalf("tool_uses=%d, want 1", len(resp.ToolUses))
	}
	if resp.ToolUses[0].Name != "list_dir" {
		t.Errorf("tool name=%q, want list_dir", resp.ToolUses[0].Name)
	}
	if resp.ToolUses[0].ToolUseID != "call_123" {
		t.Errorf("tool_use_id=%q, want call_123", resp.ToolUses[0].ToolUseID)
	}
	if string(resp.ToolUses[0].Input) != `{"path":"."}` {
		t.Errorf("input=%s, want {\"path\":\".\"}", string(resp.ToolUses[0].Input))
	}
}

func TestConverse_ToolResultRoundtrip(t *testing.T) {
	var capturedReq chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedReq)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatCompletion{
			ID:    "test-3",
			Model: "gemma4:31b",
			Choices: []chatChoice{
				{Index: 0, Message: chatMessage{Role: "assistant", Content: "Done."}, FinishReason: "stop"},
			},
		})
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, Model: "gemma4:31b"})

	_, err := client.Converse(context.Background(), provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentBlock{
				provider.TextBlock{Text: "list dir"},
			}},
			{Role: provider.RoleAssistant, Content: []provider.ContentBlock{
				provider.ToolUseBlock{ToolUseID: "call_abc", Name: "list_dir", Input: json.RawMessage(`{"path":"."}`)},
			}},
			{Role: provider.RoleUser, Content: []provider.ContentBlock{
				provider.ToolResultBlock{ToolUseID: "call_abc", Content: "file1.txt\nfile2.go"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}

	// Verify the captured request has proper message structure
	// Messages: [user, assistant with tool_calls, tool result]
	if len(capturedReq.Messages) != 3 {
		t.Fatalf("messages=%d, want 3", len(capturedReq.Messages))
	}
	if capturedReq.Messages[0].Role != "user" {
		t.Errorf("msg[0].role=%q, want user", capturedReq.Messages[0].Role)
	}
	if capturedReq.Messages[1].Role != "assistant" {
		t.Errorf("msg[1].role=%q, want assistant", capturedReq.Messages[1].Role)
	}
	if len(capturedReq.Messages[1].ToolCalls) != 1 {
		t.Errorf("msg[1].tool_calls=%d, want 1", len(capturedReq.Messages[1].ToolCalls))
	}
	if capturedReq.Messages[2].Role != "tool" {
		t.Errorf("msg[2].role=%q, want tool", capturedReq.Messages[2].Role)
	}
	if capturedReq.Messages[2].ToolCallID != "call_abc" {
		t.Errorf("msg[2].tool_call_id=%q, want call_abc", capturedReq.Messages[2].ToolCallID)
	}
}

func TestConverseStream_TextOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		chunks := []string{
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"gemma4:31b","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"gemma4:31b","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"gemma4:31b","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		}

		for _, chunk := range chunks {
			io.WriteString(w, "data: "+chunk+"\n\n")
			flusher.Flush()
		}
		io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, Model: "gemma4:31b"})

	ch := client.ConverseStream(context.Background(), provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentBlock{
				provider.TextBlock{Text: "hello"},
			}},
		},
	})

	var text strings.Builder
	var gotUsage, gotStop bool

	for ev := range ch {
		switch e := ev.(type) {
		case provider.TextDeltaEvent:
			text.WriteString(e.Text)
		case provider.UsageEvent:
			gotUsage = true
			if e.InputTokens != 5 || e.OutputTokens != 2 {
				t.Errorf("usage: in=%d out=%d, want 5/2", e.InputTokens, e.OutputTokens)
			}
		case provider.MessageStopEvent:
			gotStop = true
			if e.StopReason != "stop" {
				t.Errorf("stop=%q, want stop", e.StopReason)
			}
		case provider.StreamErrorEvent:
			t.Fatalf("unexpected error: %v", e.Err)
		}
	}

	if text.String() != "Hello world" {
		t.Errorf("text=%q, want 'Hello world'", text.String())
	}
	if !gotUsage {
		t.Error("expected UsageEvent")
	}
	if !gotStop {
		t.Error("expected MessageStopEvent")
	}
}

func TestConverseStream_ToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		chunks := []string{
			// Tool call starts
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"gemma4:31b","choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"id":"call_xyz","index":0,"type":"function","function":{"name":"list_dir","arguments":""}}]},"finish_reason":null}]}`,
			// Tool call arguments streamed
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"gemma4:31b","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","function":{"arguments":"{\"path\""}}]},"finish_reason":null}]}`,
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"gemma4:31b","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","function":{"arguments":":\".\"}"}}]},"finish_reason":null}]}`,
			// Finish
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"gemma4:31b","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		}

		for _, chunk := range chunks {
			io.WriteString(w, "data: "+chunk+"\n\n")
			flusher.Flush()
		}
		io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, Model: "gemma4:31b"})

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

func TestConverseStream_Reasoning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		chunks := []string{
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"gemma4:31b","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning":"Let me think"},"finish_reason":null}]}`,
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"gemma4:31b","choices":[{"index":0,"delta":{"content":"","reasoning":" about this."},"finish_reason":null}]}`,
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"gemma4:31b","choices":[{"index":0,"delta":{"content":"The answer is 4."},"finish_reason":null}]}`,
			`{"id":"1","object":"chat.completion.chunk","created":1000,"model":"gemma4:31b","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}

		for _, chunk := range chunks {
			io.WriteString(w, "data: "+chunk+"\n\n")
			flusher.Flush()
		}
		io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, Model: "gemma4:31b"})

	ch := client.ConverseStream(context.Background(), provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentBlock{
				provider.TextBlock{Text: "2+2?"},
			}},
		},
	})

	var reasoning, text strings.Builder
	for ev := range ch {
		switch e := ev.(type) {
		case provider.ReasoningDeltaEvent:
			reasoning.WriteString(e.Text)
		case provider.TextDeltaEvent:
			text.WriteString(e.Text)
		case provider.StreamErrorEvent:
			t.Fatalf("unexpected error: %v", e.Err)
		}
	}

	if reasoning.String() != "Let me think about this." {
		t.Errorf("reasoning=%q, want 'Let me think about this.'", reasoning.String())
	}
	if text.String() != "The answer is 4." {
		t.Errorf("text=%q, want 'The answer is 4.'", text.String())
	}
}

func TestID(t *testing.T) {
	c := New(Config{Model: "gemma4:31b"})
	if c.ID() != "ollama:gemma4:31b" {
		t.Errorf("ID=%q, want ollama:gemma4:31b", c.ID())
	}
}

func TestName(t *testing.T) {
	c := New(Config{Model: "gemma4:31b"})
	if c.Name() != "gemma4:31b" {
		t.Errorf("Name=%q, want gemma4:31b", c.Name())
	}
}

func TestBuildRequest_SystemMessage(t *testing.T) {
	c := New(Config{Model: "test"})
	body := c.buildRequest(provider.Request{
		System: "You are a bot.",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "hi"}}},
		},
	}, false)

	var req chatRequest
	json.Unmarshal(body, &req)

	if len(req.Messages) != 2 {
		t.Fatalf("messages=%d, want 2", len(req.Messages))
	}
	if req.Messages[0].Role != "system" {
		t.Errorf("msg[0].role=%q, want system", req.Messages[0].Role)
	}
	if req.Messages[0].Content != "You are a bot." {
		t.Errorf("msg[0].content=%q, want system prompt", req.Messages[0].Content)
	}
}

func TestProviderInterface(t *testing.T) {
	// Compile-time check that Client implements provider.Provider.
	var _ provider.Provider = (*Client)(nil)
}
