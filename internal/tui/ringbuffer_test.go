package tui

import "testing"

func TestRingBuffer_PushAndGetAll(t *testing.T) {
	rb := NewRingBuffer(3)

	rb.Push("one")
	rb.Push("two")

	items := rb.GetAll()
	if len(items) != 2 || items[0] != "one" || items[1] != "two" {
		t.Fatalf("unexpected items: %v", items)
	}

	rb.Push("three")
	rb.Push("four") // Should evict "one"

	items = rb.GetAll()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0] != "two" || items[1] != "three" || items[2] != "four" {
		t.Fatalf("unexpected items after eviction: %v", items)
	}
}
