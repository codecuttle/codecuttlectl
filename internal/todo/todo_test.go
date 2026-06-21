package todo

import (
	"testing"
)

func TestReplaceValid(t *testing.T) {
	l := NewList()
	items := []Item{
		{Content: "Task 1", Status: StatusPending, Priority: PriorityHigh},
		{Content: "Task 2", Status: StatusInProgress, Priority: PriorityMedium},
		{Content: "Task 3", Status: StatusCompleted, Priority: PriorityLow},
	}
	if err := l.Replace(items); err != nil {
		t.Fatalf("Replace() error: %v", err)
	}
	if l.Count() != 3 {
		t.Errorf("Count() = %d, want 3", l.Count())
	}
}

func TestReplaceMultipleInProgress(t *testing.T) {
	l := NewList()
	items := []Item{
		{Content: "Task 1", Status: StatusInProgress, Priority: PriorityHigh},
		{Content: "Task 2", Status: StatusInProgress, Priority: PriorityMedium},
	}
	// Phase 2: Swarm Backlog allows multiple in_progress tasks simultaneously
	if err := l.Replace(items); err != nil {
		t.Errorf("Replace() should allow multiple in_progress items, got error: %v", err)
	}
}

func TestReplaceInvalidStatus(t *testing.T) {
	l := NewList()
	items := []Item{
		{Content: "Task 1", Status: "invalid", Priority: PriorityHigh},
	}
	if err := l.Replace(items); err == nil {
		t.Error("Replace() should reject invalid status")
	}
}

func TestReplaceInvalidPriority(t *testing.T) {
	l := NewList()
	items := []Item{
		{Content: "Task 1", Status: StatusPending, Priority: "critical"},
	}
	if err := l.Replace(items); err == nil {
		t.Error("Replace() should reject invalid priority")
	}
}

func TestReplaceEmptyContent(t *testing.T) {
	l := NewList()
	items := []Item{
		{Content: "", Status: StatusPending, Priority: PriorityHigh},
	}
	if err := l.Replace(items); err == nil {
		t.Error("Replace() should reject empty content")
	}
}

func TestInProgress(t *testing.T) {
	l := NewList()
	items := []Item{
		{Content: "Done", Status: StatusCompleted, Priority: PriorityHigh},
		{Content: "Active", Status: StatusInProgress, Priority: PriorityMedium},
		{Content: "Pending", Status: StatusPending, Priority: PriorityLow},
	}
	l.Replace(items)

	active := l.InProgress()
	if active == nil {
		t.Fatal("InProgress() returned nil")
	}
	if active.Content != "Active" {
		t.Errorf("InProgress().Content = %q, want %q", active.Content, "Active")
	}
}

func TestInProgressNone(t *testing.T) {
	l := NewList()
	items := []Item{
		{Content: "Done", Status: StatusCompleted, Priority: PriorityHigh},
		{Content: "Pending", Status: StatusPending, Priority: PriorityLow},
	}
	l.Replace(items)

	if l.InProgress() != nil {
		t.Error("InProgress() should return nil when no item is in_progress")
	}
}

func TestSummary(t *testing.T) {
	l := NewList()
	items := []Item{
		{Content: "A", Status: StatusCompleted, Priority: PriorityHigh},
		{Content: "B", Status: StatusCompleted, Priority: PriorityHigh},
		{Content: "C", Status: StatusInProgress, Priority: PriorityMedium},
		{Content: "D", Status: StatusPending, Priority: PriorityLow},
		{Content: "E", Status: StatusPending, Priority: PriorityLow},
	}
	l.Replace(items)

	summary := l.Summary()
	if summary == "" {
		t.Error("Summary() should not be empty")
	}
	// Should contain "2/5 done"
	if !contains(summary, "2/5 done") {
		t.Errorf("Summary() = %q, expected to contain '2/5 done'", summary)
	}
}

func TestIsEmpty(t *testing.T) {
	l := NewList()
	if !l.IsEmpty() {
		t.Error("new list should be empty")
	}
	l.Replace([]Item{{Content: "X", Status: StatusPending, Priority: PriorityLow}})
	if l.IsEmpty() {
		t.Error("list with items should not be empty")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
