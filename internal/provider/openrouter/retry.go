package openrouter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const maxRateLimitRetries = 2
const maxRateLimitWait = 2 * time.Minute

// rateLimitError is handled by this adapter, not an outer conversation retry
// loop. Retrying after any generated content requires explicit user action.
type rateLimitError struct {
	retryAfter time.Duration
	partial    bool
	reason     string
}

func (e *rateLimitError) Error() string {
	message := "openrouter: rate limited (429)"
	if e.partial {
		return message + "; partial response received, automatic retry suppressed"
	}
	if e.reason != "" {
		return message + "; " + e.reason
	}
	return message
}
func (*rateLimitError) Throttled() bool    { return true }
func (*rateLimitError) RetryHandled() bool { return true }

// Unknown/invalid headers fall back to exponential backoff. Never overflow a
// duration or shorten a valid server wait: oversized waits require manual retry.
func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return 0
		}
		if seconds > int64(maxRateLimitWait/time.Second) {
			return maxRateLimitWait + time.Second
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}

type responseBody struct {
	io.ReadCloser
	retryAfter time.Duration
}

func rateLimitDelay(err error, retries int) (time.Duration, bool) {
	var limit *rateLimitError
	if !errors.As(err, &limit) || limit.partial {
		return 0, false
	}
	if retries >= maxRateLimitRetries {
		limit.reason = fmt.Sprintf("retry limit reached (%d attempts); try again later", retries+1)
		return 0, false
	}
	if limit.retryAfter > maxRateLimitWait {
		limit.reason = "server requested a wait longer than 2 minutes; try again later"
		return 0, false
	}
	delay := 30 * time.Second << uint(retries)
	if limit.retryAfter > 0 {
		delay = limit.retryAfter
	}
	return delay, true
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
