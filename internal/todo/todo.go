// Package todo manages in-memory task lists for the agent session.
package todo

import "fmt"

// Status constants for todo items.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusCancelled  = "cancelled"
)

// Priority constants for todo items.
const (
	PriorityHigh   = "high"
	PriorityMedium = "medium"
	PriorityLow    = "low"
)

// Item represents a single todo entry.
type Item struct {
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
	Assignee string `json:"assignee,omitempty"` // Node ID for async delegation
	Async    bool   `json:"async,omitempty"`    // True if task should run in parallel
}

// List manages the session's todo items.
type List struct {
	items []Item
}

// NewList creates an empty todo list.
func NewList() *List {
	return &List{}
}

// Replace performs a full-state replacement of the todo list.
// Validates that at most one item is in_progress.
func (l *List) Replace(items []Item) error {
	inProgressCount := 0
	for i, item := range items {
		if !validStatus(item.Status) {
			return fmt.Errorf("item %d: invalid status %q", i, item.Status)
		}
		if !validPriority(item.Priority) {
			return fmt.Errorf("item %d: invalid priority %q", i, item.Priority)
		}
		if item.Content == "" {
			return fmt.Errorf("item %d: content is required", i)
		}
		if item.Status == StatusInProgress {
			inProgressCount++
		}
	}
	if inProgressCount > 1 {
		return fmt.Errorf("at most one item can be in_progress, got %d", inProgressCount)
	}
	l.items = make([]Item, len(items))
	copy(l.items, items)
	return nil
}

// Items returns a copy of the current todo list.
func (l *List) Items() []Item {
	result := make([]Item, len(l.items))
	copy(result, l.items)
	return result
}

// InProgress returns the currently in-progress item, or nil if none.
func (l *List) InProgress() *Item {
	for i := range l.items {
		if l.items[i].Status == StatusInProgress {
			return &l.items[i]
		}
	}
	return nil
}

// Summary returns a concise status string like "2/5 done, 1 active".
func (l *List) Summary() string {
	if len(l.items) == 0 {
		return ""
	}
	var pending, inProgress, completed, cancelled int
	for _, item := range l.items {
		switch item.Status {
		case StatusPending:
			pending++
		case StatusInProgress:
			inProgress++
		case StatusCompleted:
			completed++
		case StatusCancelled:
			cancelled++
		}
	}

	total := len(l.items)
	s := fmt.Sprintf("%d/%d done", completed, total)
	if inProgress > 0 {
		s += fmt.Sprintf(" · %d active", inProgress)
	}
	if pending > 0 {
		s += fmt.Sprintf(" · %d pending", pending)
	}
	if cancelled > 0 {
		s += fmt.Sprintf(" · %d cancelled", cancelled)
	}
	return s
}

// Count returns the total number of items.
func (l *List) Count() int {
	return len(l.items)
}

// IsEmpty returns true if the list has no items.
func (l *List) IsEmpty() bool {
	return len(l.items) == 0
}

func validStatus(s string) bool {
	return s == StatusPending || s == StatusInProgress || s == StatusCompleted || s == StatusCancelled
}

func validPriority(p string) bool {
	return p == PriorityHigh || p == PriorityMedium || p == PriorityLow
}
