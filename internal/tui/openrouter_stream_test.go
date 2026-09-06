package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/provider"
	"github.com/codecuttle/codecuttlectl/internal/provider/openrouter"
)

func TestOpenRouterReasoningToolsAndLateUsageReachTUI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, payload := range []string{
			`{"choices":[{"delta":{"reasoning":"plan"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"grep","arguments":"{\"pattern\":"}},{"index":0,"id":"a","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.go\"}"}},{"index":1,"function":{"arguments":"\"x\"}"}}]},"finish_reason":"tool_calls"}]}`,
			`{"usage":{"prompt_tokens":1234,"completion_tokens":42}}`,
			`{"usage":{}}`,
			`[DONE]`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", payload)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m := New(Config{PluginMgr: pluginhost.NewManager(false)})
	m.history = []provider.Message{provider.BuildUserTextMessage("inspect files")}
	m.streaming = true
	m.streamCh = openrouter.New(openrouter.Config{BaseURL: server.URL}).ConverseStream(ctx, provider.Request{})
	// Consume through the real adapter and TUI reader. Do not run the command
	// returned by StreamDone: that is the actual tool execution boundary.
	for steps := 0; steps < 30; steps++ {
		msg := m.readNextStreamEvent()()
		updated, cmd := m.Update(msg)
		m = updated.(Model)
		if errMsg, ok := msg.(StreamErrorMsg); ok {
			t.Fatal(errMsg.Err)
		}
		if _, ok := msg.(StreamDoneMsg); !ok {
			continue
		}
		if cmd == nil || len(m.lastExecutedTools) != 2 {
			t.Fatalf("no continuation for tools: %+v", m.lastExecutedTools)
		}
		if m.lastExecutedTools[0].id != "a" || string(m.lastExecutedTools[0].input) != `{"path":"a.go"}` || m.lastExecutedTools[1].id != "b" || string(m.lastExecutedTools[1].input) != `{"pattern":"x"}` {
			t.Fatalf("corrupted tool calls: %+v", m.lastExecutedTools)
		}
		if len(m.history) != 2 || len(m.history[1].Content) != 3 {
			t.Fatalf("reasoning and two tools missing from history: %+v", m.history)
		}
		if m.lastCallInputTokens != 1234 || m.totalInputTokens != 1234 || m.totalOutputTokens != 42 {
			t.Fatalf("incorrect usage: context=%d input=%d output=%d", m.lastCallInputTokens, m.totalInputTokens, m.totalOutputTokens)
		}
		return
	}
	t.Fatal("stream failed to reach tool continuation")
}

func TestPartialUsagePreservesTUIContextButExplicitZeroDoesNot(t *testing.T) {
	m := New(Config{PluginMgr: pluginhost.NewManager(false)})
	m.history = []provider.Message{provider.BuildUserTextMessage("keep this history")}
	m.lastCallInputTokens = 500
	m.lastCallCacheReadInputTokens = 100
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.UsageEvent{OutputTokens: 7, InputTokensUnknown: true}
	close(ch)
	m.streamCh = ch
	msg := m.readNextStreamEvent()()
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.lastCallInputTokens != 500 || m.lastCallCacheReadInputTokens != 100 || m.totalOutputTokens != 7 {
		t.Fatal("partial usage erased known context or lost output tokens")
	}
	updated, _ = m.Update(StreamUsageMsg{})
	m = updated.(Model)
	if m.lastCallInputTokens != 0 || m.lastCallCacheReadInputTokens != 0 {
		t.Fatal("explicit zero usage must remain authoritative")
	}
	if len(m.history) != 1 {
		t.Fatal("usage handling mutated history")
	}
}

func TestOpenRouterPrematureEOFSurfacesInTUI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"plan\"}}]}\n\n")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m := New(Config{PluginMgr: pluginhost.NewManager(false)})
	m.streaming = true
	m.history = []provider.Message{provider.BuildUserTextMessage("inspect")}
	m.streamCh = openrouter.New(openrouter.Config{BaseURL: server.URL}).ConverseStream(ctx, provider.Request{})
	for steps := 0; steps < 10; steps++ {
		msg := m.readNextStreamEvent()()
		updated, _ := m.Update(msg)
		m = updated.(Model)
		if _, ok := msg.(StreamDoneMsg); ok {
			t.Fatal("premature EOF became successful completion")
		}
		if _, ok := msg.(StreamErrorMsg); ok {
			if m.streaming || len(m.history) != 1 || len(m.pendingToolCalls) != 0 {
				t.Fatal("failed stream continued execution or changed conversation history")
			}
			if len(m.messages) == 0 || !strings.Contains(m.messages[len(m.messages)-1].content, "before finish reason") {
				t.Fatal("error was not visible in the UI")
			}
			return
		}
	}
	t.Fatal("no terminal error")
}
