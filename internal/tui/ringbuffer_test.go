package tui

import "testing"

func TestRingBufferResetReleasesItems(t *testing.T) {
	rb := NewRingBuffer(2)
	rb.Push("retained output")
	rb.Reset()
	if rb.Size() != 0 || len(rb.GetAll()) != 0 {
		t.Fatal("reset did not empty buffer")
	}
	for _, item := range rb.items {
		if item != "" {
			t.Fatal("reset retained references to old output")
		}
	}
	rb.Push("fresh")
	if got := rb.GetAll(); len(got) != 1 || got[0] != "fresh" {
		t.Fatalf("unexpected contents after reset: %v", got)
	}
}

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
