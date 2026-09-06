package openrouter

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/provider"
)

func collectStream(t *testing.T, data string) []provider.StreamEvent {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, data)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var events []provider.StreamEvent
	for event := range New(Config{BaseURL: server.URL}).ConverseStream(ctx, provider.Request{}) {
		events = append(events, event)
	}
	return events
}

func sse(payloads ...string) string {
	return "data: " + strings.Join(payloads, "\n\ndata: ") + "\n\n"
}

func TestStreamInterleavedToolsAreSerialized(t *testing.T) {
	events := collectStream(t, sse(
		`{"choices":[{"delta":{"reasoning":"planning"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"grep","arguments":"{\"pattern\":"}},{"index":0,"id":"a","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.go\"}"}},{"index":1,"function":{"arguments":"\"x\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20}}`,
		`[DONE]`,
	))
	var current string
	var calls []string
	var args strings.Builder
	stops, usages := 0, 0
	for _, event := range events {
		switch e := event.(type) {
		case provider.ToolUseStartEvent:
			if current != "" {
				t.Fatal("overlapping starts cannot be represented by the provider event contract")
			}
			current = e.ToolUseID + ":" + e.Name
			args.Reset()
		case provider.ToolInputDeltaEvent:
			if current == "" {
				t.Fatal("arguments without a tool start")
			}
			args.WriteString(e.Delta)
		case provider.ToolUseStopEvent:
			calls = append(calls, current+":"+args.String())
			current = ""
		case provider.MessageStopEvent:
			stops++
		case provider.UsageEvent:
			usages++
		case provider.StreamErrorEvent:
			t.Fatalf("unexpected error: %v", e.Err)
		}
	}
	want := `a:read_file:{"path":"a.go"}|b:grep:{"pattern":"x"}`
	if got := strings.Join(calls, "|"); got != want || stops != 1 || usages != 1 {
		t.Fatalf("calls=%q stops=%d usages=%d; want %q and one stop/usage", got, stops, usages, want)
	}
}

func TestStreamFailuresAreNotSuccessfulTurns(t *testing.T) {
	for name, data := range map[string]string{
		"reasoning EOF":         sse(`{"choices":[{"delta":{"reasoning":"planning"}}]}`),
		"done without finish":   sse(`[DONE]`),
		"upstream error":        sse(`{"error":{"code":503,"message":"upstream unavailable"}}`),
		"malformed JSON":        sse(`{bad json`, `{"choices":[{"finish_reason":"stop"}]}`, `[DONE]`),
		"scanner limit":         "data: " + strings.Repeat("x", 300*1024),
		"unfinished tool":       sse(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"edit_file","arguments":"{"}}]},"finish_reason":"tool_calls"}]}`, `[DONE]`),
		"tool without identity": sse(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, `[DONE]`),
		"length limited tools":  sse(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"edit_file","arguments":"{}"}}]},"finish_reason":"length"}]}`, `[DONE]`),
	} {
		t.Run(name, func(t *testing.T) {
			errors := 0
			for _, event := range collectStream(t, data) {
				switch event.(type) {
				case provider.StreamErrorEvent:
					errors++
				case provider.MessageStopEvent, provider.ToolUseStartEvent, provider.ToolUseStopEvent:
					t.Fatalf("failed stream emitted executable/success event %T", event)
				}
			}
			if errors != 1 {
				t.Fatalf("errors=%d, want exactly one", errors)
			}
		})
	}
}

func TestStreamUsageSnapshotsDoNotEraseOrDoubleCount(t *testing.T) {
	events := collectStream(t, sse(
		`{"usage":{"prompt_tokens":100,"completion_tokens":2}}`,
		`{"choices":[{"delta":{"content":"hello"},"finish_reason":"stop"}]}`,
		`{"usage":{"completion_tokens":10}}`,
		`{"usage":{}}`, `[DONE]`,
	))
	var usages []provider.UsageEvent
	for _, event := range events {
		if e, ok := event.(provider.UsageEvent); ok {
			usages = append(usages, e)
		}
	}
	if len(usages) != 1 || usages[0].InputTokens != 100 || usages[0].OutputTokens != 10 {
		t.Fatalf("usage=%+v, want one authoritative merged snapshot", usages)
	}
}

func TestStreamRepeatedTerminalChoiceWithUsage(t *testing.T) {
	for _, finish := range []string{"stop", "tool_calls"} {
		t.Run(finish, func(t *testing.T) {
			first := `{"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}`
			if finish == "tool_calls" {
				first = `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`
			}
			metadata := fmt.Sprintf(`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":%q}],"usage":{"prompt_tokens":123,"completion_tokens":4}}`, finish)
			stops, usages, tools := 0, 0, 0
			for _, event := range collectStream(t, sse(first, metadata, metadata, `[DONE]`)) {
				switch e := event.(type) {
				case provider.MessageStopEvent:
					stops++
					if e.StopReason != finish {
						t.Fatalf("stop=%q, want %q", e.StopReason, finish)
					}
				case provider.UsageEvent:
					usages++
					if e.InputTokens != 123 || e.OutputTokens != 4 {
						t.Fatalf("unexpected usage: %+v", e)
					}
				case provider.ToolUseStopEvent:
					tools++
				case provider.StreamErrorEvent:
					t.Fatal(e.Err)
				}
			}
			wantTools := 0
			if finish == "tool_calls" {
				wantTools = 1
			}
			if stops != 1 || usages != 1 || tools != wantTools {
				t.Fatalf("stops=%d usages=%d tools=%d", stops, usages, tools)
			}
		})
	}
}

func TestStreamRejectsContentAfterCompletion(t *testing.T) {
	for _, delta := range []string{
		`{"delta":{"content":"late text"}}`,
		`{"delta":{"reasoning":"late reasoning"}}`,
		`{"delta":{"tool_calls":[{"index":0}]}}`,
		`{"delta":{},"finish_reason":"length"}`,
	} {
		errors := 0
		for _, event := range collectStream(t, sse(`{"choices":[{"finish_reason":"stop"}]}`, `{"choices":[`+delta+`]}`, `[DONE]`)) {
			switch event.(type) {
			case provider.StreamErrorEvent:
				errors++
			case provider.MessageStopEvent, provider.ToolUseStartEvent:
				t.Fatalf("invalid stream emitted success event: %T", event)
			}
		}
		if errors != 1 {
			t.Fatalf("errors=%d, want 1", errors)
		}
	}
}

func TestStreamUsagePresence(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		count         int
		unknown       bool
	}{
		{"absent", `{}`, 0, false},
		{"null", `{"usage":null}`, 0, false},
		{"empty", `{"usage":{}}`, 0, false},
		{"output only", `{"usage":{"completion_tokens":7}}`, 1, true},
		{"explicit zero", `{"usage":{"prompt_tokens":0,"completion_tokens":0}}`, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			for _, event := range collectStream(t, sse(`{"choices":[{"finish_reason":"stop"}]}`, tc.payload, `[DONE]`)) {
				if e, ok := event.(provider.UsageEvent); ok {
					count++
					if e.InputTokensUnknown != tc.unknown {
						t.Fatalf("unknown=%v, want %v", e.InputTokensUnknown, tc.unknown)
					}
				}
				if e, ok := event.(provider.StreamErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if count != tc.count {
				t.Fatalf("usage events=%d, want %d", count, tc.count)
			}
		})
	}
}

func TestStreamReasoningOnlyWithFinishIsValid(t *testing.T) {
	stops := 0
	for _, event := range collectStream(t, sse(`{"choices":[{"delta":{"reasoning":"plan"},"finish_reason":"stop"}]}`, `[DONE]`)) {
		switch e := event.(type) {
		case provider.MessageStopEvent:
			stops++
		case provider.StreamErrorEvent:
			t.Fatal(e.Err)
		}
	}
	if stops != 1 {
		t.Fatalf("stops=%d, want 1", stops)
	}
}

func TestStreamSSEFraming(t *testing.T) {
	data := ": keepalive\r\n\r\ndata:{\"choices\":[\r\ndata: {\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\r\n\r\ndata:[DONE]\r\n\r\n"
	text, stops := "", 0
	for _, event := range collectStream(t, data) {
		switch e := event.(type) {
		case provider.TextDeltaEvent:
			text += e.Text
		case provider.MessageStopEvent:
			stops++
		case provider.StreamErrorEvent:
			t.Fatal(e.Err)
		}
	}
	if text != "ok" || stops != 1 {
		t.Fatalf("text=%q stops=%d", text, stops)
	}
}

// A canceled consumer must release the HTTP body even if it stops draining events.
func TestStreamCancelWithFullEventQueue(t *testing.T) {
	closed := make(chan struct{})
	c := New(Config{})
	c.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data := strings.Repeat(sse(`{"choices":[{"delta":{"content":"x"}}]}`), 200)
		return &http.Response{StatusCode: 200, Body: &notifyingBody{Reader: strings.NewReader(data), closed: closed}, Header: make(http.Header)}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := c.ConverseStream(ctx, provider.Request{})
	deadline := time.After(2 * time.Second)
	for len(ch) < cap(ch) {
		select {
		case <-deadline:
			t.Fatal("event queue did not fill")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		// Drain to clean up the broken implementation before failing.
		go func() {
			for range ch {
			}
		}()
		t.Fatal("canceled producer is blocked on an abandoned event queue")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type notifyingBody struct {
	io.Reader
	closed chan struct{}
}

func (b *notifyingBody) Close() error { close(b.closed); return nil }
