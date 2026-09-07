package conversation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/provider"
	"github.com/codecuttle/codecuttlectl/internal/todo"
)

// barrierProvider enters synchronously but delegates waiting to the stream
// goroutine, so tests can coordinate admission without racing Agent fields.
type barrierProvider struct {
	mockEchoProvider
	entered  chan context.Context
	release  chan struct{}
	stubborn bool
}

func (p *barrierProvider) ConverseStream(ctx context.Context, _ provider.Request) <-chan provider.StreamEvent {
	p.entered <- ctx
	ch := make(chan provider.StreamEvent, 1)
	go func() {
		defer close(ch)
		if p.stubborn {
			<-p.release
		} else {
			select {
			case <-ctx.Done():
				return
			case <-p.release:
			}
		}
		ch <- provider.TextDeltaEvent{Text: "completed"}
	}()
	return ch
}

func testEngine(t *testing.T, p provider.Provider) (*Engine, *Agent) {
	t.Helper()
	a, err := NewAgent(Config{Provider: p})
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(a)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Errorf("engine cleanup: %v", err)
		}
	})
	return e, a
}

func waitTurn(t *testing.T, h *TurnHandle) TurnResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := h.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func entered(t *testing.T, p *barrierProvider) context.Context {
	t.Helper()
	select {
	case ctx := <-p.entered:
		return ctx
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
		return nil
	}
}

func TestEngineConcurrentAdmissionAndCancel(t *testing.T) {
	p := &barrierProvider{entered: make(chan context.Context, 32), release: make(chan struct{})}
	e, a := testEngine(t, p)
	start := make(chan struct{})
	results := make(chan *TurnHandle, 32)
	failures := make(chan error, 32)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			h, err := e.Submit(context.Background(), "admitted prompt")
			if err != nil {
				failures <- err
			} else {
				results <- h
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	if len(results) != 1 || len(failures) != 31 {
		t.Fatalf("admitted=%d rejected=%d", len(results), len(failures))
	}
	for err := range failures {
		if !errors.Is(err, ErrEngineBusy) {
			t.Fatal(err)
		}
	}
	h := <-results
	providerCtx := entered(t, p)
	if s := e.Snapshot(); s.ActiveTurn != h.ID() || len(s.History) != 0 {
		t.Fatalf("active snapshot must retain completed checkpoint: %+v", s)
	}
	h.Cancel()
	h.Cancel()
	if providerCtx.Err() != context.Canceled {
		t.Fatal("handle did not cancel provider")
	}
	if result := waitTurn(t, h); result.Status != TurnCancelled || !errors.Is(result.Error, context.Canceled) {
		t.Fatalf("result=%+v", result)
	}
	if a.turn != 1 || len(a.provHistory) != 1 || e.Snapshot().ActiveTurn != 0 {
		t.Fatal("rejected submissions changed history or active slot was not released")
	}
	next, err := e.Submit(context.Background(), "next prompt")
	if err != nil || next.ID() <= h.ID() {
		t.Fatalf("new turn: %v", err)
	}
	nextCtx := entered(t, p)
	h.Cancel() // stale cancellation must never cancel a replacement turn
	if nextCtx.Err() != nil {
		t.Fatal("old handle canceled new turn")
	}
	next.Cancel()
	waitTurn(t, next)
}

func TestEngineCloseWaitsForActualWorker(t *testing.T) {
	p := &barrierProvider{entered: make(chan context.Context, 1), release: make(chan struct{}), stubborn: true}
	e, _ := testEngine(t, p)
	defer close(p.release)
	h, err := e.Submit(context.Background(), "wait")
	if err != nil {
		t.Fatal(err)
	}
	providerCtx := entered(t, p)
	waitCtx, stop := context.WithCancel(context.Background())
	stop()
	if _, err := h.Wait(waitCtx); !errors.Is(err, context.Canceled) || providerCtx.Err() != nil {
		t.Fatal("wait cancellation must not cancel the turn")
	}
	if err := e.Close(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("uncooperative worker should time out close: %v", err)
	}
	if s := e.Snapshot(); !s.Closed || s.ActiveTurn != h.ID() {
		t.Fatalf("close falsely released active worker: %+v", s)
	}
	if _, err := e.Submit(context.Background(), "forbidden"); !errors.Is(err, ErrEngineClosed) {
		t.Fatalf("submit after close=%v", err)
	}
	select {
	case <-h.Done():
		t.Fatal("reported completion before provider exited")
	default:
	}
	// Release before waiting; keep cleanup safe without closing twice.
	p.release <- struct{}{}
	if result := waitTurn(t, h); result.Status != TurnCancelled {
		t.Fatalf("result=%+v", result)
	}
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEngineCloseIdleAndDeadline(t *testing.T) {
	e, _ := testEngine(t, &mockEchoProvider{})
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(context.Background(), "closed"); !errors.Is(err, ErrEngineClosed) {
		t.Fatal("closed idle engine admitted a turn")
	}
	p := &barrierProvider{entered: make(chan context.Context, 1), release: make(chan struct{})}
	e2, _ := testEngine(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	h, err := e2.Submit(ctx, "deadline")
	if err != nil {
		t.Fatal(err)
	}
	entered(t, p)
	if result := waitTurn(t, h); result.Status != TurnCancelled || !errors.Is(result.Error, context.DeadlineExceeded) {
		t.Fatalf("deadline outcome=%+v", result)
	}
}

func TestEngineFailureHandleMatchesTerminalEvent(t *testing.T) {
	e, _ := testEngine(t, &terminalErrorProvider{})
	h, err := e.Submit(context.Background(), "error")
	if err != nil {
		t.Fatal(err)
	}
	result := waitTurn(t, h)
	if result.Status != TurnFailed || result.Error == nil {
		t.Fatalf("failure outcome=%+v", result)
	}
	terminals := 0
	for event := range h.Events() {
		if terminal, ok := event.(EventTurnDone); ok {
			terminals++
			if terminal.Error != result.Error {
				t.Fatal("event and handle disagree")
			}
		}
	}
	if terminals != 1 {
		t.Fatalf("terminal events=%d", terminals)
	}
}

func TestEnginePreservesCallerContext(t *testing.T) {
	type traceKey struct{}
	p := &barrierProvider{entered: make(chan context.Context, 1), release: make(chan struct{})}
	e, _ := testEngine(t, p)
	parent, cancel := context.WithTimeout(context.WithValue(context.Background(), traceKey{}, "trace-id"), time.Minute)
	defer cancel()
	h, err := e.Submit(parent, "context")
	if err != nil {
		t.Fatal(err)
	}
	child := entered(t, p)
	wantDeadline, _ := parent.Deadline()
	gotDeadline, ok := child.Deadline()
	if !ok || !gotDeadline.Equal(wantDeadline) || child.Value(traceKey{}) != "trace-id" {
		t.Fatal("engine stripped caller deadline or values")
	}
	h.Cancel()
	waitTurn(t, h)
}

func TestEngineParentCancellationAndAdmissionErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e, a := testEngine(t, &mockEchoProvider{})
	if _, err := e.Submit(ctx, "not admitted"); !errors.Is(err, context.Canceled) || a.turn != 0 {
		t.Fatal("pre-canceled submit mutated state")
	}
	if _, err := NewEngine(nil).Submit(context.Background(), "test"); !errors.Is(err, ErrNoAgent) {
		t.Fatalf("nil agent error=%v", err)
	}
	p := &barrierProvider{entered: make(chan context.Context, 1), release: make(chan struct{})}
	e2, _ := testEngine(t, p)
	parent, stop := context.WithCancel(context.Background())
	defer stop()
	h, err := e2.Submit(parent, "wait")
	if err != nil {
		t.Fatal(err)
	}
	entered(t, p)
	stop()
	if result := waitTurn(t, h); !errors.Is(result.Error, context.Canceled) {
		t.Fatalf("parent cancellation=%+v", result)
	}
}

type burstProvider struct{ mockEchoProvider }

func (*burstProvider) ConverseStream(context.Context, provider.Request) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 1000)
	for i := 0; i < 1000; i++ {
		ch <- provider.TextDeltaEvent{Text: "x"}
	}
	close(ch)
	return ch
}

func TestEngineFullMailboxCompletesWithoutConsumer(t *testing.T) {
	e, _ := testEngine(t, &burstProvider{})
	h, err := e.Submit(context.Background(), "burst")
	if err != nil {
		t.Fatal(err)
	}
	result := waitTurn(t, h) // intentionally no event consumer until completion
	if result.Status != TurnCancelled || !errors.Is(result.Error, ErrSlowConsumer) {
		t.Fatalf("overflow must be explicit: %+v", result)
	}
	terminals := 0
	for event := range h.Events() {
		if done, ok := event.(EventTurnDone); ok {
			terminals++
			if !errors.Is(done.Error, ErrSlowConsumer) {
				t.Fatalf("event error=%v", done.Error)
			}
		}
	}
	if terminals != 1 || e.Snapshot().ActiveTurn != 0 {
		t.Fatal("full mailbox stranded finalization")
	}
}

func TestEngineSnapshotIsDeepCopied(t *testing.T) {
	a, err := NewAgent(Config{Provider: &mockEchoProvider{}, Workbench: []string{"read_file"}})
	if err != nil {
		t.Fatal(err)
	}
	a.provHistory = []provider.Message{provider.BuildAssistantMessage([]provider.ContentBlock{
		provider.ToolUseBlock{ToolUseID: "original", Name: "read_file", Input: []byte(`{"path":"a"}`), ThoughtSignature: "signature"},
	})}
	if err := a.todos.Replace([]todo.Item{{Content: "original", Status: "pending", Priority: "medium"}}); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(a)
	defer e.Close(context.Background())
	s := e.Snapshot()
	s.Workbench[0] = "evil"
	s.Todos[0].Content = "changed"
	s.History[0].Blocks[0].Input[0] = '!'
	s.History[0].Blocks[0].Signature = "changed"
	next := e.Snapshot()
	if next.Workbench[0] != "read_file" || next.Todos[0].Content != "original" || string(next.History[0].Blocks[0].Input) != `{"path":"a"}` || next.History[0].Blocks[0].Signature != "signature" {
		t.Fatal("snapshot aliases authoritative state")
	}
	if string(a.provHistory[0].Content[0].(provider.ToolUseBlock).Input) != `{"path":"a"}` {
		t.Fatal("snapshot aliases Agent input bytes")
	}
}

func TestEngineSnapshotConcurrentReaders(t *testing.T) {
	p := &barrierProvider{entered: make(chan context.Context, 1), release: make(chan struct{})}
	e, _ := testEngine(t, p)
	h, err := e.Submit(context.Background(), "wait")
	if err != nil {
		t.Fatal(err)
	}
	entered(t, p)
	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 200; j++ {
				_ = e.Snapshot()
			}
		}()
	}
	close(p.release)
	waitTurn(t, h)
	readers.Wait()
	if e.Snapshot().LastTurn.Status != TurnCompleted {
		t.Fatal("readers interfered with completion")
	}
}

type approvalTurnProvider struct {
	mockEchoProvider
	calls int
}

func (p *approvalTurnProvider) ConverseStream(context.Context, provider.Request) <-chan provider.StreamEvent {
	p.calls++
	ch := make(chan provider.StreamEvent, 7)
	ch <- provider.ToolUseStartEvent{ToolUseID: "approve", Name: "git"}
	ch <- provider.ToolInputDeltaEvent{Delta: `{"subcommand":"rebase","args":["-i"]}`}
	ch <- provider.ToolUseStopEvent{}
	ch <- provider.ToolUseStartEvent{ToolUseID: "native", Name: "todo_manage"}
	ch <- provider.ToolInputDeltaEvent{Delta: `{"todos":[{"content":"must not happen","status":"pending","priority":"medium"}]}`}
	ch <- provider.ToolUseStopEvent{}
	ch <- provider.MessageStopEvent{StopReason: "tool_use"}
	close(ch)
	return ch
}

func TestEngineCancelDuringApprovalDoesNotDispatch(t *testing.T) {
	p := &approvalTurnProvider{}
	waiting := make(chan struct{})
	release := make(chan struct{})
	a, err := NewAgent(Config{Provider: p, ApprovalFunc: func(_, _, _, _ string) bool {
		close(waiting)
		<-release // legacy callbacks are not context-aware; don't claim instant stop
		return true
	}})
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(a)
	defer e.Close(context.Background())
	h, err := e.Submit(context.Background(), "approve")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-waiting:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("approval callback did not start")
	}
	h.Cancel()
	if _, err := e.Submit(context.Background(), "blocked"); !errors.Is(err, ErrEngineBusy) {
		close(release)
		t.Fatalf("cancel released admission before callback exited: %v", err)
	}
	close(release)
	result := waitTurn(t, h)
	if result.Status != TurnCancelled || p.calls != 1 || !a.todos.IsEmpty() {
		t.Fatalf("cancellation allowed dispatch/request: result=%+v requests=%d todos=%v", result, p.calls, a.todos.Items())
	}
	// No plugin manager was supplied: reaching plugin Execute would have panicked.
}

func TestEngineCompletionWithoutDrainingAndStableResult(t *testing.T) {
	e, _ := testEngine(t, &mockEchoProvider{})
	h, err := e.Submit(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	result := waitTurn(t, h)
	if result.Status != TurnCompleted || !strings.Contains(result.Response, "Hello") || result.Error != nil {
		t.Fatalf("result=%+v", result)
	}
	h.Cancel()
	if again := waitTurn(t, h); again != result {
		t.Fatal("completion changed after cancel")
	}
	if s := e.Snapshot(); s.ActiveTurn != 0 || len(s.History) != 2 || s.LastTurn != result || s.Revision != 2 {
		t.Fatalf("checkpoint=%+v", s)
	}
}
