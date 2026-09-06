package tui

import "sync"

// RingBuffer is a fixed-size, thread-safe circular buffer for streaming strings.
type RingBuffer struct {
	mu       sync.RWMutex
	capacity int
	items    []string
	head     int
	size     int
}

// NewRingBuffer allocates a circular buffer with the specified capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 100
	}
	return &RingBuffer{
		capacity: capacity,
		items:    make([]string, capacity),
	}
}

// Push adds an item to the circular buffer, evicting the oldest item if at capacity.
func (r *RingBuffer) Push(item string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx := (r.head + r.size) % r.capacity
	r.items[idx] = item

	if r.size < r.capacity {
		r.size++
	} else {
		r.head = (r.head + 1) % r.capacity
	}
}

// GetAll returns all elements in chronological order.
func (r *RingBuffer) GetAll() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, r.size)
	for i := 0; i < r.size; i++ {
		result[i] = r.items[(r.head+i)%r.capacity]
	}
	return result
}

// Size returns the current number of items.
func (r *RingBuffer) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}

// Reset clears the buffer.
func (r *RingBuffer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.items)
	r.head = 0
	r.size = 0
}
