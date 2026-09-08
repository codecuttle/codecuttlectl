package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeProvider is a controllable Provider for fallback tests.
type fakeProvider struct {
	id          string
	converse    func(ctx context.Context, req Request) (*Response, error)
	converseStr func(ctx context.Context, req Request) <-chan StreamEvent
	contextWin  int32
	cost        float64
}

func (f *fakeProvider) ID() string   { return f.id }
func (f *fakeProvider) Name() string { return f.id }

func (f *fakeProvider) Converse(ctx context.Context, req Request) (*Response, error) {
	if f.converse != nil {
		return f.converse(ctx, req)
	}
	return &Response{Content: f.id + ":ok"}, nil
}

func (f *fakeProvider) ConverseStream(ctx context.Context, req Request) <-chan StreamEvent {
	if f.converseStr != nil {
		return f.converseStr(ctx, req)
	}
	ch := make(chan StreamEvent, 2)
	ch <- TextDeltaEvent{Text: f.id + ":ok"}
	ch <- MessageStopEvent{StopReason: "end_turn"}
	close(ch)
	return ch
}

func (f *fakeProvider) ContextWindow() int32                      { return f.contextWin }
func (f *fakeProvider) EstimateCost(u Usage) float64               { return f.cost }

var _ Provider = (*fakeProvider)(nil)
var _ ContextWindowProvider = (*fakeProvider)(nil)
var _ CostEstimator = (*fakeProvider)(nil)

type throttleErr struct{}

func (throttleErr) Error() string          { return "throttled" }
func (throttleErr) Throttled() bool        { return true }
func (throttleErr) RetryHandled() bool     { return true }

func TestFallbackConverseSuccessWithoutFallback(t *testing.T) {
	primary := &fakeProvider{id: "p1"}
	f := NewFallbackProvider("node", primary, &fakeProvider{id: "p2"})
	resp, err := f.Converse(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "p1:ok" {
		t.Fatalf("expected p1 response, got %q", resp.Content)
	}
}

func TestFallbackConverseOnThrottle(t *testing.T) {
	var primaryCalls, backupCalls int
	primary := &fakeProvider{
		id: "openrouter:primary",
		converse: func(ctx context.Context, req Request) (*Response, error) {
			primaryCalls++
			return nil, throttleErr{}
		},
	}
	backup := &fakeProvider{
		id: "bedrock:backup",
		converse: func(ctx context.Context, req Request) (*Response, error) {
			backupCalls++
			return &Response{Content: "backup:ok"}, nil
		},
	}
	f := NewFallbackProvider("astra", primary, backup)
	resp, err := f.Converse(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "backup:ok" || primaryCalls != 1 || backupCalls != 1 {
		t.Fatalf("unexpected: resp=%q primaryCalls=%d backupCalls=%d", resp.Content, primaryCalls, backupCalls)
	}
}

func TestFallbackConverseNoFallbackOnValidationError(t *testing.T) {
	var backupCalls int
	primary := &fakeProvider{
		id: "p1",
		converse: func(ctx context.Context, req Request) (*Response, error) {
			return nil, errors.New("openrouter: HTTP 400: invalid request")
		},
	}
	backup := &fakeProvider{
		id: "p2",
		converse: func(ctx context.Context, req Request) (*Response, error) {
			backupCalls++
			return &Response{Content: "backup:ok"}, nil
		},
	}
	f := NewFallbackProvider("node", primary, backup)
	_, err := f.Converse(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected error for non-eligible failure")
	}
	if backupCalls != 0 {
		t.Fatalf("backup should not be called for validation error, got %d calls", backupCalls)
	}
}

func TestFallbackCooldownSkipsThrottledPrimary(t *testing.T) {
	// Inject a deterministic clock, restore after.
	origNow := nowFunc
	now := time.Unix(1_000_000, 0)
	nowFunc = func() time.Time { return now }
	defer func() { nowFunc = origNow }()

	var primaryCalls, backupCalls int
	primary := &fakeProvider{
		id: "p1",
		converse: func(ctx context.Context, req Request) (*Response, error) {
			primaryCalls++
			return nil, throttleErr{}
		},
	}
	backup := &fakeProvider{
		id: "p2",
		converse: func(ctx context.Context, req Request) (*Response, error) {
			backupCalls++
			return &Response{Content: "backup:ok"}, nil
		},
	}
	f := NewFallbackProvider("node", primary, backup)

	// First request: primary throttles, backup succeeds, primary enters cooldown.
	if _, err := f.Converse(context.Background(), Request{}); err != nil {
		t.Fatal(err)
	}
	if primaryCalls != 1 || backupCalls != 1 {
		t.Fatalf("first: primaryCalls=%d backupCalls=%d", primaryCalls, backupCalls)
	}

	// Second request within cooldown: primary skipped entirely.
	if _, err := f.Converse(context.Background(), Request{}); err != nil {
		t.Fatal(err)
	}
	if primaryCalls != 1 || backupCalls != 2 {
		t.Fatalf("second should skip primary: primaryCalls=%d backupCalls=%d", primaryCalls, backupCalls)
	}

	// After cooldown: primary tried again.
	now = now.Add(cooldownDuration + time.Second)
	if _, err := f.Converse(context.Background(), Request{}); err != nil {
		t.Fatal(err)
	}
	if primaryCalls != 2 || backupCalls != 3 {
		t.Fatalf("post-cooldown should retry primary: primaryCalls=%d backupCalls=%d", primaryCalls, backupCalls)
	}
}

func TestFallbackStreamFailoverWithoutContent(t *testing.T) {
	var primaryCalls, backupCalls int
	primary := &fakeProvider{
		id: "p1",
		converseStr: func(ctx context.Context, req Request) <-chan StreamEvent {
			primaryCalls++
			ch := make(chan StreamEvent, 1)
			ch <- StreamErrorEvent{Err: throttleErr{}}
			close(ch)
			return ch
		},
	}
	backup := &fakeProvider{
		id: "p2",
		converseStr: func(ctx context.Context, req Request) <-chan StreamEvent {
			backupCalls++
			ch := make(chan StreamEvent, 2)
			ch <- TextDeltaEvent{Text: "backup-stream-ok"}
			ch <- MessageStopEvent{StopReason: "end_turn"}
			close(ch)
			return ch
		},
	}
	f := NewFallbackProvider("node", primary, backup)

	var text string
	var gotErr error
	for ev := range f.ConverseStream(context.Background(), Request{}) {
		switch e := ev.(type) {
		case TextDeltaEvent:
			text += e.Text
		case StreamErrorEvent:
			gotErr = e.Err
		}
	}
	if gotErr != nil || text != "backup-stream-ok" {
		t.Fatalf("expected backup stream success, text=%q err=%v", text, gotErr)
	}
	if primaryCalls != 1 || backupCalls != 1 {
		t.Fatalf("primaryCalls=%d backupCalls=%d", primaryCalls, backupCalls)
	}
}

func TestFallbackStreamSuppressesAfterContent(t *testing.T) {
	var backupCalls int
	primary := &fakeProvider{
		id: "p1",
		converseStr: func(ctx context.Context, req Request) <-chan StreamEvent {
			ch := make(chan StreamEvent, 2)
			ch <- TextDeltaEvent{Text: "partial"}
			ch <- StreamErrorEvent{Err: throttleErr{}}
			close(ch)
			return ch
		},
	}
	backup := &fakeProvider{
		id: "p2",
		converseStr: func(ctx context.Context, req Request) <-chan StreamEvent {
			backupCalls++
			ch := make(chan StreamEvent, 0)
			close(ch)
			return ch
		},
	}
	f := NewFallbackProvider("node", primary, backup)

	var text string
	var gotErr error
	for ev := range f.ConverseStream(context.Background(), Request{}) {
		switch e := ev.(type) {
		case TextDeltaEvent:
			text += e.Text
		case StreamErrorEvent:
			gotErr = e.Err
		}
	}
	if text != "partial" || gotErr == nil {
		t.Fatalf("expected partial text + error, text=%q err=%v", text, gotErr)
	}
	if backupCalls != 0 {
		t.Fatalf("backup must not run after partial content, got %d calls", backupCalls)
	}
}

func TestFallbackAllCoolingDown(t *testing.T) {
	origNow := nowFunc
	now := time.Now()
	nowFunc = func() time.Time { return now }
	defer func() { nowFunc = origNow }()

	f := NewFallbackProvider("node",
		&fakeProvider{id: "p1", converse: func(ctx context.Context, r Request) (*Response, error) { return nil, throttleErr{} }},
		&fakeProvider{id: "p2", converse: func(ctx context.Context, r Request) (*Response, error) { return nil, throttleErr{} }},
	)
	if _, err := f.Converse(context.Background(), Request{}); err == nil {
		t.Fatal("expected last error")
	}
	// Both in cooldown now.
	var cooling *AllProvidersCoolingDownError
	if _, err := f.Converse(context.Background(), Request{}); err == nil || !errors.As(err, &cooling) {
		t.Fatalf("expected AllProvidersCoolingDownError, got %v", err)
	}
}

func TestFallbackContextWindowAndCost(t *testing.T) {
	f := NewFallbackProvider("node",
		&fakeProvider{id: "p1", contextWin: 1000, cost: 0.5},
		&fakeProvider{id: "p2", contextWin: 2000, cost: 1.5},
	)
	if got := f.ContextWindow(); got != 1000 {
		t.Fatalf("context window = %d, want 1000", got)
	}
	if got := f.EstimateCost(Usage{}); got != 0.5 {
		t.Fatalf("cost = %f, want 0.5", got)
	}
}