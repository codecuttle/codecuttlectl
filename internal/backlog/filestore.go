package backlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultDataDir returns the base directory for backlog storage,
// following XDG Base Directory conventions.
func DefaultDataDir() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "codecuttlectl", "backlog")
}

// FileStore implements Store using local JSON files.
// Each work item is stored as a single JSON file named {id}.json.
// Writes are atomic: data is written to a .tmp file, then renamed.
type FileStore struct {
	dir string
}

// NewFileStore creates a FileStore rooted at the given directory.
// Creates the directory if it doesn't exist.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating backlog directory %s: %w", dir, err)
	}
	return &FileStore{dir: dir}, nil
}

// Dir returns the storage directory path.
func (fs *FileStore) Dir() string {
	return fs.dir
}

// Create persists a new work item with a generated ID.
func (fs *FileStore) Create(item *WorkItem) (string, error) {
	id, err := GenerateID()
	if err != nil {
		return "", err
	}

	item.ID = id
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now

	// Default status to proposed if not set
	if item.Status == "" {
		item.Status = StatusProposed
	}

	if err := item.Validate(); err != nil {
		return "", fmt.Errorf("validation failed: %w", err)
	}

	if err := fs.write(id, item); err != nil {
		return "", err
	}

	return id, nil
}

// Save persists an updated work item.
func (fs *FileStore) Save(id string, item *WorkItem) error {
	// Verify the item exists
	if _, err := os.Stat(fs.filePath(id)); os.IsNotExist(err) {
		return fmt.Errorf("work item %s not found", id)
	}

	item.UpdatedAt = time.Now().UTC()

	if err := item.Validate(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	return fs.write(id, item)
}

// Load retrieves a work item by ID.
func (fs *FileStore) Load(id string) (*WorkItem, error) {
	data, err := os.ReadFile(fs.filePath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("work item %s not found", id)
		}
		return nil, fmt.Errorf("reading work item %s: %w", id, err)
	}

	var item WorkItem
	if err := json.Unmarshal(data, &item); err != nil {
		return nil, fmt.Errorf("parsing work item %s: %w", id, err)
	}

	return &item, nil
}

// List returns work item summaries matching the given filter.
func (fs *FileStore) List(filter ListFilter) ([]WorkItemSummary, error) {
	entries, err := os.ReadDir(fs.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing backlog directory: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}

	var summaries []WorkItemSummary

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(fs.dir, name))
		if err != nil {
			continue
		}

		var item WorkItem
		if err := json.Unmarshal(data, &item); err != nil {
			continue
		}

		// Apply filters
		if filter.Status != "" && string(item.Status) != filter.Status {
			continue
		}
		if filter.Kind != "" && string(item.Kind) != filter.Kind {
			continue
		}
		if filter.Tag != "" && !containsTag(item.Tags, filter.Tag) {
			continue
		}
		if filter.Project != "" && filter.Project != "*" && item.Project != filter.Project {
			continue
		}

		summaries = append(summaries, item.Summary())
	}

	// Sort by priority descending, then updated_at descending
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Priority != summaries[j].Priority {
			return summaries[i].Priority > summaries[j].Priority
		}
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})

	if len(summaries) > limit {
		summaries = summaries[:limit]
	}

	return summaries, nil
}

// Delete removes a work item from storage.
func (fs *FileStore) Delete(id string) error {
	path := fs.filePath(id)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("work item %s not found", id)
		}
		return fmt.Errorf("deleting work item %s: %w", id, err)
	}
	return nil
}

// Prune removes done/rejected items older than maxAge.
func (fs *FileStore) Prune(maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(fs.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("listing backlog for prune: %w", err)
	}

	cutoff := time.Now().UTC().Add(-maxAge)
	deleted := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(fs.dir, name))
		if err != nil {
			continue
		}

		var item WorkItem
		if err := json.Unmarshal(data, &item); err != nil {
			continue
		}

		// Only prune done or rejected items
		if item.Status != StatusDone && item.Status != StatusRejected {
			continue
		}

		if item.UpdatedAt.Before(cutoff) {
			if err := os.Remove(filepath.Join(fs.dir, name)); err == nil {
				deleted++
			}
		}
	}

	return deleted, nil
}

// write atomically persists a work item to disk.
func (fs *FileStore) write(id string, item *WorkItem) error {
	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling work item %s: %w", id, err)
	}

	target := fs.filePath(id)
	tmp := target + ".tmp"

	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("writing work item temp file %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("renaming work item file %s: %w", target, err)
	}

	return nil
}

func (fs *FileStore) filePath(id string) string {
	return filepath.Join(fs.dir, id+".json")
}

func containsTag(tags []string, target string) bool {
	for _, t := range tags {
		if t == target {
			return true
		}
	}
	return false
}
