package openrouter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/provider"
)

func TestRateLimitRecovery(t *testing.T) {
	for _, streamingError := range []bool{false, true} {
		for _, exhaust := range []bool{false, true} {
			t.Run(fmt.Sprintf("SSE=%v/exhaust=%v", streamingError, exhaust), func(t *testing.T) {
				var requests atomic.Int32
				var bodies []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					bodies = append(bodies, string(body))
					n := requests.Add(1)
					if exhaust || n == 1 {
						w.Header().Set("Retry-After", "1")
						if streamingError {
							fmt.Fprint(w, sse(`{"error":{"code":"429","message":"secret upstream text"}}`))
						} else {
							w.WriteHeader(http.StatusTooManyRequests)
							fmt.Fprint(w, "secret upstream text")
						}
						return
					}
					fmt.Fprint(w, sse(`{"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}`, `{"usage":{"prompt_tokens":10,"completion_tokens":1}}`, `[DONE]`))
				}))
				defer server.Close()
				client := New(Config{BaseURL: server.URL})
				client.waitRetry = func(ctx context.Context, delay time.Duration) error {
					if delay != time.Second {
						t.Errorf("Retry-After ignored: %s", delay)
					}
					return ctx.Err()
				}
				retries, failures, stops, usages := 0, 0, 0, 0
				text := ""
				for event := range client.ConverseStream(context.Background(), provider.Request{Messages: []provider.Message{provider.BuildUserTextMessage("test")}}) {
					switch e := event.(type) {
					case provider.RetryEvent:
						retries++
						if e.Attempt != retries || e.MaxRetries != 2 {
							t.Errorf("bad retry event: %+v", e)
						}
					case provider.StreamErrorEvent:
						failures++
						if !provider.IsThrottleError(e.Err) || provider.IsTransientStreamError(e.Err) || strings.Contains(e.Err.Error(), "secret") {
							t.Errorf("unsafe classification/diagnostic: %v", e.Err)
						}
					case provider.TextDeltaEvent:
						text += e.Text
					case provider.MessageStopEvent:
						stops++
					case provider.UsageEvent:
						usages++
					}
				}
				if exhaust {
					if requests.Load() != 3 || retries != 2 || failures != 1 || stops != 0 || usages != 0 {
						t.Fatalf("requests=%d retry=%d errors=%d stop=%d usage=%d", requests.Load(), retries, failures, stops, usages)
					}
				} else if requests.Load() != 2 || retries != 1 || failures != 0 || stops != 1 || usages != 1 || text != "OK" {
					t.Fatalf("requests=%d retry=%d errors=%d stop=%d usage=%d text=%q", requests.Load(), retries, failures, stops, usages, text)
				}
				for _, body := range bodies {
					if body != bodies[0] {
						t.Fatal("retry mutated request/history")
					}
				}
			})
		}
	}
}

func TestRateLimitAfterContentIsNotRetried(t *testing.T) {
	for _, delta := range []string{
		`{"content":"partial"}`, `{"reasoning":"thought"}`,
		`{"tool_calls":[{"index":0,"id":"t","function":{"name":"write_file","arguments":"{"}}]}`,
	} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			fmt.Fprint(w, sse(`{"choices":[{"delta":`+delta+`}]}`, `{"error":{"code":429}}`))
		}))
		client := New(Config{BaseURL: server.URL})
		count := 0
		for event := range client.ConverseStream(context.Background(), provider.Request{}) {
			switch e := event.(type) {
			case provider.RetryEvent, provider.ToolUseStartEvent, provider.MessageStopEvent, provider.UsageEvent:
				t.Fatalf("unsafe event after partial failure: %T", e)
			case provider.StreamErrorEvent:
				count++
				var limit *rateLimitError
				if !errors.As(e.Err, &limit) || !limit.partial || provider.IsTransientStreamError(e.Err) {
					t.Fatalf("partial retry not suppressed: %v", e.Err)
				}
			}
		}
		server.Close()
		if calls.Load() != 1 || count != 1 {
			t.Fatalf("requests=%d errors=%d", calls.Load(), count)
		}
	}
}

func TestRateLimitWithContentInSameFrame(t *testing.T) {
	ch := make(chan provider.StreamEvent, 8)
	err := parseSSEStream(context.Background(), strings.NewReader(sse(`{"choices":[{"delta":{"content":"partial"}}],"error":{"code":429}}`)), ch)
	var limit *rateLimitError
	if !errors.As(err, &limit) || !limit.partial {
		t.Fatalf("co-located content must suppress retry: %v", err)
	}
}

func TestRateLimitCancelBackoff(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(429)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := New(Config{BaseURL: server.URL}).ConverseStream(ctx, provider.Request{})
	select {
	case event := <-ch:
		if _, ok := event.(provider.RetryEvent); !ok {
			t.Fatalf("want retry status, got %T", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no retry event")
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("event after canceled backoff")
		}
	case <-time.After(time.Second):
		t.Fatal("backoff ignored cancellation")
	}
	if calls.Load() != 1 {
		t.Fatalf("extra request after cancel: %d", calls.Load())
	}
}

func TestRetryAfterPolicy(t *testing.T) {
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"2", 2 * time.Second}, {"0", 0}, {"-1", 0}, {"invalid", 0},
		{now.Add(5 * time.Second).Format(http.TimeFormat), 5 * time.Second},
		{now.Add(-time.Second).Format(http.TimeFormat), 0},
		{"9223372036854775807", maxRateLimitWait + time.Second},
	} {
		if got := parseRetryAfter(tc.value, now); got != tc.want {
			t.Errorf("Retry-After %q = %s, want %s", tc.value, got, tc.want)
		}
	}
	if _, retry := rateLimitDelay(&rateLimitError{retryAfter: 3 * time.Minute}, 0); retry {
		t.Fatal("must not shorten server wait to fit retry budget")
	}
	for attempt, want := range []time.Duration{30 * time.Second, 60 * time.Second} {
		if delay, retry := rateLimitDelay(&rateLimitError{}, attempt); !retry || delay != want {
			t.Fatalf("backoff=%s retry=%v", delay, retry)
		}
	}
}

func TestUnaryRateLimitRecovery(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	client := New(Config{BaseURL: server.URL})
	client.waitRetry = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	response, err := client.Converse(context.Background(), provider.Request{})
	if err != nil || response.Content != "OK" || calls.Load() != 2 {
		t.Fatalf("response=%+v err=%v requests=%d", response, err, calls.Load())
	}
}
