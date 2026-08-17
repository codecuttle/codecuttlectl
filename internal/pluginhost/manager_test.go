package pluginhost

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestAcquireFileLock_Write(t *testing.T) {
	m := NewManager(false)
	workDir := "/test/dir"
	input := json.RawMessage(`{"path": "file.txt"}`)

	unlock1 := m.acquireFileLock("edit_file", input, workDir)

	// Try to acquire the lock concurrently
	locked := make(chan bool)
	go func() {
		unlock2 := m.acquireFileLock("write_file", input, workDir)
		locked <- true
		unlock2()
	}()

	select {
	case <-locked:
		t.Fatal("second write lock acquired while first was held!")
	case <-time.After(50 * time.Millisecond):
		// Expected: second lock blocks
	}

	unlock1()

	select {
	case <-locked:
		// Expected: second lock succeeds after first is released
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second lock failed to acquire after first was released")
	}
}

func TestAcquireFileLock_Read(t *testing.T) {
	m := NewManager(false)
	workDir := "/test/dir"
	input := json.RawMessage(`{"path": "file.txt"}`)

	unlock1 := m.acquireFileLock("read_file", input, workDir)

	// Multiple reads should succeed immediately
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock2 := m.acquireFileLock("read_file", input, workDir)
			time.Sleep(10 * time.Millisecond)
			unlock2()
		}()
	}

	// Wait for all readers
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(200 * time.Millisecond):
		t.Fatal("concurrent read locks deadlocked")
	}

	unlock1()
}

func TestAcquireFileLock_PathResolution(t *testing.T) {
	m := NewManager(false)
	workDir := "/test/dir"

	inputRel := json.RawMessage(`{"path": "file.txt"}`)
	inputAbs := json.RawMessage(`{"path": "/test/dir/file.txt"}`)

	unlock1 := m.acquireFileLock("edit_file", inputRel, workDir)

	// Absolute path should be blocked because it resolves to the same lock
	locked := make(chan bool)
	go func() {
		unlock2 := m.acquireFileLock("write_file", inputAbs, workDir)
		locked <- true
		unlock2()
	}()

	select {
	case <-locked:
		t.Fatal("absolute and relative paths did not resolve to the same lock")
	case <-time.After(50 * time.Millisecond):
		// Expected
	}

	unlock1()
}

func TestAcquireFileLock_NoPath(t *testing.T) {
	m := NewManager(false)
	input := json.RawMessage(`{"some_other_field": "value"}`)

	// Should not block or panic
	unlock := m.acquireFileLock("edit_file", input, "/test")
	unlock()

	if len(m.fileLocks) != 0 {
		t.Errorf("Expected 0 file locks created, got %d", len(m.fileLocks))
	}
}
