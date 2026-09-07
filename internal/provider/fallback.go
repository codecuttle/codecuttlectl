package provider

import (
	"context"
	"errors"
	"sync"
	"time"
)

// cooldownDuration is how long a failed candidate stays marked as exhausted
// before it is eligible to be retried again. This avoids repeatedly paying the
// adapter's long rate-limit backoff (30s/60s) on every request while the
// throttled endpoint remains saturated.
const cooldownDuration = 5 * time.Minute

// nowFunc is injectable for deterministic tests.
var nowFunc = time.Now

// FallbackProvider wraps an ordered list of Providers (primary first), trying
// each in turn when the active candidate fails with an eligible error.
//
// It is provider-agnostic and only changes which backend answers a request; it
// does not change the caller's logical persona, workbench, tool catalog, or
// conversation history. Each request retrieves an immutable chain snapshot, so
// concurrent requests never mutate each other's active selection.
type FallbackProvider struct {
	logicalID string // stable identity exposed to the caller (node identity)
	chain     []Provider

	mu   sync.Mutex
	cool map[string]time.Time // provider ID -> unavailable-until
}

// NewFallbackProvider wraps providers in priority order (primary first). At
// least one provider is required.
func NewFallbackProvider(logicalID string, providers ...Provider) *FallbackProvider {
	clean := make([]Provider, 0, len(providers))
	for _, p := range providers {
		if p != nil {
			clean = append(clean, p)
		}
	}
	return &FallbackProvider{
		logicalID: logicalID,
		chain:     clean,
		cool:      make(map[string]time.Time),
	}
}

// ID returns the logical identity of the wrapped node, not the currently
// selected backend. Use ActiveID() to observe the effective backend.
func (f *FallbackProvider) ID() string { return f.logicalID }

// Name returns the logical display name.
func (f *FallbackProvider) Name() string {
	return "fallback:" + f.logicalID
}

// ActiveID returns the provider selected for a new request at this instant,
// taking cooldowns into account. It is advisory for display purposes; the
// request itself re-evaluates against its own snapshot to stay race-safe.
func (f *FallbackProvider) ActiveID() string {
	p := f.firstEligible(nowFunc())
	if p == nil {
		return ""
	}
	return p.ID()
}

// Providers returns a copy of the configured chain.
func (f *FallbackProvider) Providers() []Provider {
	return append([]Provider(nil), f.chain...)
}

func (f *FallbackProvider) firstEligible(now time.Time) Provider {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.chain {
		if until, ok := f.cool[p.ID()]; ok && now.Before(until) {
			continue
		}
		return p
	}
	return nil
}

func (f *FallbackProvider) markCooldown(id string, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cool[id] = now.Add(cooldownDuration)
}

func (f *FallbackProvider) eligibleProvidersAt(now time.Time) []Provider {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Provider
	for _, p := range f.chain {
		if until, ok := f.cool[p.ID()]; ok && now.Before(until) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Converse implements Provider.Converse with failover across the chain.
func (f *FallbackProvider) Converse(ctx context.Context, req Request) (*Response, error) {
	candidates := f.eligibleProvidersAt(nowFunc())
	if len(candidates) == 0 {
		candidates = append(candidates, f.chain...)
		// All candidates cooling down: return unified retry guidance.
		return nil, &AllProvidersCoolingDownError{}
	}

	var lastErr error
	for _, p := range candidates {
		resp, err := p.Converse(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isFallbackEligible(err) {
			return nil, err
		}
		f.markCooldown(p.ID(), nowFunc())
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

// ConverseStream implements Provider.ConverseStream with failover.
//
// The primary candidate's stream events are forwarded to the returned channel
// as-is. If the primary fails with an eligible error WITHOUT having emitted any
// content, the next candidate is tried. If partial content/tools were already
// forwarded, failover is suppressed and the original error is surfaced.
func (f *FallbackProvider) ConverseStream(ctx context.Context, req Request) <-chan StreamEvent {
	out := make(chan StreamEvent, 64)

	go func() {
		defer close(out)

		candidates := f.eligibleProvidersAt(nowFunc())
		if len(candidates) == 0 {
			candidates = append(candidates, f.chain...)
			if err := sendStreamSafe(ctx, out, StreamErrorEvent{Err: &AllProvidersCoolingDownError{}}); err != nil {
				return
			}
			return
		}

		var lastErr error
		for _, p := range candidates {
			if ctx.Err() != nil {
				return
			}
			stream := p.ConverseStream(ctx, req)
			emittedContent := false
			var streamErr error

			for ev := range stream {
				switch e := ev.(type) {
				case StreamErrorEvent:
					// Defer error delivery: if this is an eligible failure with
					// no content yet, fail over instead of surfacing it.
					streamErr = e.Err
					continue
				case TextDeltaEvent, ReasoningDeltaEvent, ToolUseStartEvent, ToolInputDeltaEvent:
					emittedContent = true
				}
				if err := sendStreamSafe(ctx, out, ev); err != nil {
					return
				}
			}

			// No content progressed and the stream failed with an eligible
			// error: mark this candidate and advance to the next without
			// emitting the error event.
			if streamErr != nil && !emittedContent && isFallbackEligible(streamErr) {
				lastErr = streamErr
				f.markCooldown(p.ID(), nowFunc())
				continue
			}

			// Otherwise terminate: surface any error once, or rely on the
			// underlying stream's already-forwarded terminal events on success.
			if streamErr != nil {
				lastErr = streamErr
				_ = sendStreamSafe(ctx, out, StreamErrorEvent{Err: streamErr})
				return
			}
			return
		}

		if lastErr != nil {
			_ = sendStreamSafe(ctx, out, StreamErrorEvent{Err: lastErr})
		}
	}()

	return out
}

// ContextWindow reports the context window of the current active candidate.
func (f *FallbackProvider) ContextWindow() int32 {
	if p := f.firstEligible(nowFunc()); p != nil {
		if cwp, ok := p.(ContextWindowProvider); ok {
			return cwp.ContextWindow()
		}
	}
	return 0
}

// EstimateCost reports the current active candidate's estimate if it supports it.
func (f *FallbackProvider) EstimateCost(usage Usage) float64 {
	if p := f.firstEligible(nowFunc()); p != nil {
		if ce, ok := p.(CostEstimator); ok {
			return ce.EstimateCost(usage)
		}
	}
	return 0
}

// AllProvidersCoolingDownError indicates every configured candidate is within
// cooldown and none can serve a request right now. It is marked throttled so
// generic retry classification does not re-drive the chain unexpectedly.
type AllProvidersCoolingDownError struct{}

func (AllProvidersCoolingDownError) Error() string {
	return "fallback: all providers are cooling down, retry later"
}
func (AllProvidersCoolingDownError) Throttled() bool { return true }

func isFinalTerminal(ev StreamEvent) bool {
	switch ev.(type) {
	case MessageStopEvent:
		return true
	default:
		return false
	}
}

func sendStreamSafe(ctx context.Context, ch chan<- StreamEvent, ev StreamEvent) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case ch <- ev:
		return nil
	}
}

// isFallbackEligible reports whether a failed invocation should advance to the
// next candidate. Only clearly transient/rate-limit failures qualify; auth,
// validation, caller cancellation, malformed streams, or partial generation are
// never eligible.
func isFallbackEligible(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// A rate limit is safe to fail over even when the adapter already
	// exhausted its own bounded retries (RetryHandled). RetryHandled exists to
	// stop the *outer* conversation retry loop from replays, not to block
	// advancing to a different backend.
	if IsThrottleError(err) {
		return true
	}
	var handled interface{ RetryHandled() bool }
	if errors.As(err, &handled) && handled.RetryHandled() {
		return false
	}
	return false
}