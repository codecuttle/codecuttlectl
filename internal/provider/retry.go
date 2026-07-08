package provider

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/aws/smithy-go"
)

// IsThrottleError reports whether the error is a rate-limit/throttling
// rejection (Bedrock ThrottlingException / HTTP 429, "Too many tokens").
// These need much longer backoff than transient server faults: the Bedrock
// token bucket refills over tens of seconds, so the SDK's millisecond-scale
// retryer always exhausts its attempts without ever succeeding.
func IsThrottleError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "ThrottlingException", "TooManyRequestsException", "Throttling", "LimitExceededException":
			return true
		}
	}
	msg := err.Error()
	for _, marker := range []string{
		"ThrottlingException",
		"Too many tokens",
		"too many requests",
		"StatusCode: 429",
		"rate limit",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// RetryDelay returns the backoff duration before retry attempt `attempt`
// (0-based) for the given error. Throttling errors back off much more
// aggressively (30s, 60s, 120s, capped) because Bedrock's token bucket
// refills slowly; other transient faults use short exponential backoff
// (2s, 4s, 8s). A ±20% jitter is applied to avoid thundering-herd retries.
func RetryDelay(attempt int, err error) time.Duration {
	var base time.Duration
	if IsThrottleError(err) {
		base = 30 * time.Second << uint(attempt) // 30s, 60s, 120s...
		if base > 2*time.Minute {
			base = 2 * time.Minute
		}
	} else {
		base = 2 * time.Second << uint(attempt) // 2s, 4s, 8s...
		if base > 30*time.Second {
			base = 30 * time.Second
		}
	}
	// ±20% jitter
	jitter := time.Duration(rand.Int64N(int64(base)/2)) - base/5
	return base + jitter
}

// IsTransientStreamError reports whether an error from a streaming (or
// non-streaming) model invocation is transient and safe to retry.
//
// Bedrock can fail *mid-stream* with errors like InternalServerException or
// ModelStreamErrorException ("The system encountered an unexpected error
// during processing. Try your request again."). The AWS SDK's built-in
// retryer only covers the initial HTTP handshake — once the event stream is
// open, errors surface exactly once via stream.Err() and are never retried
// by the SDK. Long generations (big tool inputs, extended thinking) are the
// most exposed, so the harness must retry these itself.
func IsTransientStreamError(err error) bool {
	if err == nil {
		return false
	}
	// User-initiated cancellation is never retryable.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if IsThrottleError(err) {
		return true
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "InternalServerException",
			"ModelStreamErrorException",
			"ServiceUnavailableException",
			"ThrottlingException",
			"ModelNotReadyException",
			"InternalFailure",
			"ServiceUnavailable",
			"RequestTimeout",
			"RequestTimeoutException":
			return true
		}
		// Fault-based classification for event-stream errors that don't map
		// to a modeled exception type.
		if apiErr.ErrorFault() == smithy.FaultServer {
			return true
		}
	}

	// String fallback for providers (Google, Ollama) and wrapped transport
	// errors that don't implement smithy.APIError.
	msg := err.Error()
	for _, marker := range []string{
		"InternalServerException",
		"ModelStreamErrorException",
		"ServiceUnavailableException",
		"ThrottlingException",
		"too many requests",
		"connection reset by peer",
		"unexpected EOF",
		"stream closed",
		"http2: server sent GOAWAY",
		"StatusCode: 429",
		"StatusCode: 500",
		"StatusCode: 502",
		"StatusCode: 503",
		"StatusCode: 504",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
