package swarm

// EventDispatcher is an interface used by background swarm agents to send
// thread-safe messages back to the main Orchestrator UI loop.
type EventDispatcher interface {
	Dispatch(msg any)
}

// TaskStartedMsg indicates a background agent has begun working on a task.
type TaskStartedMsg struct {
	TaskID   string // The Todo Item ID
	Assignee string // The Node ID processing the task
}

// TaskCompletedMsg indicates a background agent has finished a task.
type TaskCompletedMsg struct {
	TaskID   string
	Assignee string
	Result   string
	IsError  bool
}

// TaskProgressMsg is dispatched when a background agent executes a tool or makes progress.
type TaskProgressMsg struct {
	Assignee string
	Progress string
}

// TokenUsageMsg reports token usage from a background agent back to the main UI.
// This allows background tasks to correctly increment the session's running cost.
type TokenUsageMsg struct {
	Assignee              string
	InputTokens           int32
	OutputTokens          int32
	CacheReadInputTokens  int32
	CacheWriteInputTokens int32
}
