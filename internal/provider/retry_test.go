package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestIsThrottleError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"modeled throttling", &types.ThrottlingException{Message: strPtr("Too many tokens, please wait before trying again.")}, true},
		{"wrapped modeled throttling", fmt.Errorf("bedrock converse stream: %w", &types.ThrottlingException{Message: strPtr("Too many tokens")}), true},
		{"string 429", errors.New("https response error StatusCode: 429, RequestID: x, ThrottlingException: Too many tokens"), true},
		{"internal server", &types.InternalServerException{Message: strPtr("unexpected error")}, false},
		{"unrelated", errors.New("file not found"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsThrottleError(tc.err); got != tc.want {
				t.Errorf("IsThrottleError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsTransientStreamError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, false},
		{"wrapped context canceled", fmt.Errorf("stream error: %w", context.Canceled), false},
		{"deadline exceeded", context.DeadlineExceeded, false},
		{"modeled internal server", &types.InternalServerException{Message: strPtr("The system encountered an unexpected error during processing. Try your request again.")}, true},
		{"wrapped internal server", fmt.Errorf("stream error: %w", &types.InternalServerException{Message: strPtr("unexpected error")}), true},
		{"modeled stream error", &types.ModelStreamErrorException{Message: strPtr("stream broke")}, true},
		{"modeled throttling", &types.ThrottlingException{Message: strPtr("Too many tokens")}, true},
		{"service unavailable", &types.ServiceUnavailableException{Message: strPtr("try later")}, true},
		{"string InternalServerException", errors.New("operation error Bedrock Runtime: ConverseStream, https response error StatusCode: 500, InternalServerException: The system encountered an unexpected error"), true},
		{"string 429 after sdk retries", errors.New("operation error Bedrock Runtime: ConverseStream, exceeded maximum number of attempts, 3, https response error StatusCode: 429, ThrottlingException: Too many tokens, please wait before trying again."), true},
		{"connection reset", errors.New("read tcp: connection reset by peer"), true},
		{"validation error not transient", &types.ValidationException{Message: strPtr("The value at messages.19.content.1.toolUse.input is empty.")}, false},
		{"unrelated error", errors.New("no such file or directory"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTransientStreamError(tc.err); got != tc.want {
				t.Errorf("IsTransientStreamError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRetryDelay(t *testing.T) {
	throttle := &types.ThrottlingException{Message: strPtr("Too many tokens")}
	server := &types.InternalServerException{Message: strPtr("oops")}

	// Throttling delays must be long enough for the token bucket to refill
	// (base 30s, 60s, 120s cap; ±20% jitter).
	for attempt, wantBase := range []time.Duration{30 * time.Second, 60 * time.Second, 2 * time.Minute} {
		d := RetryDelay(attempt, throttle)
		lo, hi := wantBase-wantBase/5, wantBase+wantBase/2
		if d < lo || d > hi {
			t.Errorf("throttle RetryDelay(attempt=%d) = %s, want within [%s, %s]", attempt, d, lo, hi)
		}
	}

	// Server faults use short exponential backoff (base 2s, 4s, 8s).
	for attempt, wantBase := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
		d := RetryDelay(attempt, server)
		lo, hi := wantBase-wantBase/5, wantBase+wantBase/2
		if d < lo || d > hi {
			t.Errorf("server RetryDelay(attempt=%d) = %s, want within [%s, %s]", attempt, d, lo, hi)
		}
	}

	// Caps: throttle capped at 2min base, server at 30s base (plus jitter headroom).
	if d := RetryDelay(10, throttle); d > 3*time.Minute {
		t.Errorf("throttle delay not capped: %s", d)
	}
	if d := RetryDelay(10, server); d > 45*time.Second {
		t.Errorf("server delay not capped: %s", d)
	}
	// Delays must never be negative.
	for i := 0; i < 6; i++ {
		if d := RetryDelay(i, server); d <= 0 {
			t.Errorf("non-positive delay at attempt %d: %s", i, d)
		}
	}
}

func strPtr(s string) *string { return &s }
