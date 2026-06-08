package backlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateID(t *testing.T) {
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID() error: %v", err)
	}
	if !strings.HasPrefix(id, "wi_") {
		t.Errorf("expected prefix 'wi_', got %q", id)
	}
	if len(id) != 11 { // "wi_" + 8 hex chars
		t.Errorf("expected length 11, got %d (%q)", len(id), id)
	}

	// Should be unique
	id2, _ := GenerateID()
	if id == id2 {
		t.Error("two consecutive IDs should be different")
	}
}

func TestFileStoreCreateAndLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	item := &WorkItem{
		Title:   "Test item",
		Kind:    KindTask,
		Project: "test-project",
		Origin: Origin{
			SessionID:   "ses_abc123",
			Turn:        1,
			Trigger:     TriggerUserRequest,
			Description: "User asked for this",
		},
		Effort:   EffortSmall,
		Priority: 50,
		Tags:     []string{"test", "example"},
	}

	id, err := store.Create(item)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(id, "wi_") {
		t.Errorf("expected ID prefix 'wi_', got %q", id)
	}

	// Load it back
	loaded, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Title != "Test item" {
		t.Errorf("expected title 'Test item', got %q", loaded.Title)
	}
	if loaded.Kind != KindTask {
		t.Errorf("expected kind 'task', got %q", loaded.Kind)
	}
	if loaded.Status != StatusProposed {
		t.Errorf("expected default status 'proposed', got %q", loaded.Status)
	}
	if loaded.Project != "test-project" {
		t.Errorf("expected project 'test-project', got %q", loaded.Project)
	}
	if loaded.Priority != 50 {
		t.Errorf("expected priority 50, got %d", loaded.Priority)
	}
	if loaded.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if loaded.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestFileStoreSave(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	item := &WorkItem{
		Title:    "Save test",
		Kind:     KindPlugin,
		Priority: 30,
		Effort:   EffortMedium,
	}

	id, err := store.Create(item)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update and save
	loaded, _ := store.Load(id)
	loaded.Status = StatusApproved
	loaded.Priority = 80

	if err := store.Save(id, loaded); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload and verify
	reloaded, _ := store.Load(id)
	if reloaded.Status != StatusApproved {
		t.Errorf("expected status 'approved', got %q", reloaded.Status)
	}
	if reloaded.Priority != 80 {
		t.Errorf("expected priority 80, got %d", reloaded.Priority)
	}
}

func TestFileStoreSaveNotFound(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	item := &WorkItem{
		ID:     "wi_nonexist",
		Title:  "Ghost",
		Kind:   KindTask,
		Status: StatusProposed,
	}
	err := store.Save("wi_nonexist", item)
	if err == nil {
		t.Error("expected error for non-existent item")
	}
}

func TestFileStoreLoadNotFound(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	_, err := store.Load("wi_nonexist")
	if err == nil {
		t.Error("expected error for non-existent item")
	}
}

func TestFileStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	item := &WorkItem{Title: "Delete me", Kind: KindTask, Priority: 10, Effort: EffortTrivial}
	id, _ := store.Create(item)

	if err := store.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Should not be loadable
	_, err := store.Load(id)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestFileStoreDeleteNotFound(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	err := store.Delete("wi_nonexist")
	if err == nil {
		t.Error("expected error for non-existent delete")
	}
}

func TestFileStoreList(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	// Create items with different attributes
	items := []struct {
		title    string
		kind     Kind
		project  string
		priority int
		status   Status
		tags     []string
	}{
		{"Task A", KindTask, "proj-a", 90, StatusProposed, []string{"urgent"}},
		{"Task B", KindPlugin, "proj-b", 50, StatusApproved, []string{"tooling"}},
		{"Task C", KindTask, "proj-a", 70, StatusProposed, []string{"urgent", "tooling"}},
		{"Task D", KindResearch, "proj-a", 30, StatusDone, []string{"research"}},
	}

	for _, it := range items {
		item := &WorkItem{
			Title:    it.title,
			Kind:     it.kind,
			Project:  it.project,
			Priority: it.priority,
			Status:   it.status,
			Tags:     it.tags,
			Effort:   EffortSmall,
		}
		store.Create(item)
	}

	// List all
	all, err := store.List(ListFilter{Project: "*"})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("expected 4 items, got %d", len(all))
	}
	// Should be sorted by priority descending
	if all[0].Priority != 90 {
		t.Errorf("expected first item priority 90, got %d", all[0].Priority)
	}

	// Filter by project
	projA, _ := store.List(ListFilter{Project: "proj-a"})
	if len(projA) != 3 {
		t.Errorf("expected 3 proj-a items, got %d", len(projA))
	}

	// Filter by status
	proposed, _ := store.List(ListFilter{Status: "proposed", Project: "*"})
	if len(proposed) != 2 {
		t.Errorf("expected 2 proposed items, got %d", len(proposed))
	}

	// Filter by kind
	tasks, _ := store.List(ListFilter{Kind: "task", Project: "*"})
	if len(tasks) != 2 {
		t.Errorf("expected 2 task items, got %d", len(tasks))
	}

	// Filter by tag
	urgent, _ := store.List(ListFilter{Tag: "urgent", Project: "*"})
	if len(urgent) != 2 {
		t.Errorf("expected 2 urgent items, got %d", len(urgent))
	}

	// Combined filters
	urgentProjA, _ := store.List(ListFilter{Tag: "urgent", Project: "proj-a"})
	if len(urgentProjA) != 2 {
		t.Errorf("expected 2 urgent proj-a items, got %d", len(urgentProjA))
	}

	// Limit
	limited, _ := store.List(ListFilter{Project: "*", Limit: 2})
	if len(limited) != 2 {
		t.Errorf("expected 2 items with limit, got %d", len(limited))
	}
}

func TestFileStorePrune(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	// Create an old done item
	item := &WorkItem{
		Title:    "Old done",
		Kind:     KindTask,
		Status:   StatusDone,
		Priority: 10,
		Effort:   EffortTrivial,
	}
	id, _ := store.Create(item)

	// Manually backdate its updated_at
	loaded, _ := store.Load(id)
	loaded.UpdatedAt = time.Now().Add(-100 * 24 * time.Hour) // 100 days ago
	// Write directly to bypass Save's timestamp update
	os.WriteFile(
		filepath.Join(dir, id+".json"),
		mustMarshal(loaded),
		0600,
	)

	// Create a recent proposed item (should NOT be pruned)
	store.Create(&WorkItem{
		Title:    "Recent proposed",
		Kind:     KindTask,
		Priority: 50,
		Effort:   EffortSmall,
	})

	// Create a recent done item (should NOT be pruned — too new)
	store.Create(&WorkItem{
		Title:    "Recent done",
		Kind:     KindTask,
		Status:   StatusDone,
		Priority: 20,
		Effort:   EffortTrivial,
	})

	// Prune items older than 90 days
	deleted, err := store.Prune(90 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 pruned, got %d", deleted)
	}

	// Verify the old one is gone
	_, err = store.Load(id)
	if err == nil {
		t.Error("expected old done item to be pruned")
	}

	// Verify others remain
	remaining, _ := store.List(ListFilter{Project: "*"})
	if len(remaining) != 2 {
		t.Errorf("expected 2 remaining items, got %d", len(remaining))
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name    string
		item    WorkItem
		wantErr bool
		field   string
	}{
		{
			name:    "valid item",
			item:    WorkItem{Title: "Good", Kind: KindTask, Status: StatusProposed, Priority: 50},
			wantErr: false,
		},
		{
			name:    "missing title",
			item:    WorkItem{Kind: KindTask, Status: StatusProposed},
			wantErr: true,
			field:   "title",
		},
		{
			name:    "invalid kind",
			item:    WorkItem{Title: "Bad kind", Kind: "invalid", Status: StatusProposed},
			wantErr: true,
			field:   "kind",
		},
		{
			name:    "invalid status",
			item:    WorkItem{Title: "Bad status", Kind: KindTask, Status: "nope"},
			wantErr: true,
			field:   "status",
		},
		{
			name:    "priority too high",
			item:    WorkItem{Title: "Too high", Kind: KindTask, Status: StatusProposed, Priority: 150},
			wantErr: true,
			field:   "priority",
		},
		{
			name:    "priority negative",
			item:    WorkItem{Title: "Negative", Kind: KindTask, Status: StatusProposed, Priority: -1},
			wantErr: true,
			field:   "priority",
		},
		{
			name:    "invalid effort",
			item:    WorkItem{Title: "Bad effort", Kind: KindTask, Status: StatusProposed, Effort: "huge"},
			wantErr: true,
			field:   "effort",
		},
		{
			name:    "empty effort is valid",
			item:    WorkItem{Title: "No effort", Kind: KindTask, Status: StatusProposed},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.item.Validate()
			if tt.wantErr {
				if err == nil {
					t.Error("expected validation error, got nil")
				} else if ve, ok := err.(*ValidationError); ok && ve.Field != tt.field {
					t.Errorf("expected error on field %q, got %q", tt.field, ve.Field)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestDetectProject(t *testing.T) {
	// Test with the current workspace (which has a go.mod and git remote)
	project := DetectProject("/home/coder/workspace/codecuttlectl")
	if project != "codecuttlectl" {
		t.Errorf("expected 'codecuttlectl', got %q", project)
	}

	// Test with empty dir (falls back to basename)
	tmpDir := t.TempDir()
	project = DetectProject(tmpDir)
	if project == "" {
		t.Error("expected non-empty project from temp dir basename")
	}

	// Test with empty string
	project = DetectProject("")
	if project != "" {
		t.Errorf("expected empty string for empty workDir, got %q", project)
	}
}

func TestDetectProjectFromPackageJSON(t *testing.T) {
	dir := t.TempDir()
	pkg := `{"name": "@myorg/cool-app", "version": "1.0.0"}`
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)

	project := DetectProject(dir)
	if project != "cool-app" {
		t.Errorf("expected 'cool-app' from scoped package.json, got %q", project)
	}
}

func TestDetectProjectFromGoMod(t *testing.T) {
	dir := t.TempDir()
	gomod := "module github.com/user/stocks-app\n\ngo 1.22\n"
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0644)

	project := DetectProject(dir)
	if project != "stocks-app" {
		t.Errorf("expected 'stocks-app' from go.mod, got %q", project)
	}
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	item := &WorkItem{Title: "Atomic test", Kind: KindTask, Priority: 10, Effort: EffortTrivial}
	id, _ := store.Create(item)

	// Verify no .tmp files remain
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("found leftover .tmp file: %s", e.Name())
		}
	}

	// Verify the file exists as .json
	path := filepath.Join(dir, id+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("expected file %s to exist", path)
	}
}

func TestWorkItemSummary(t *testing.T) {
	now := time.Now().UTC()
	item := WorkItem{
		ID:        "wi_abc12345",
		Title:     "Summary test",
		Status:    StatusApproved,
		Kind:      KindPlugin,
		Project:   "my-project",
		Priority:  75,
		Effort:    EffortMedium,
		UpdatedAt: now,
	}

	summary := item.Summary()
	if summary.ID != "wi_abc12345" {
		t.Errorf("ID mismatch: %q", summary.ID)
	}
	if summary.Title != "Summary test" {
		t.Errorf("Title mismatch: %q", summary.Title)
	}
	if summary.Status != StatusApproved {
		t.Errorf("Status mismatch: %q", summary.Status)
	}
	if summary.Kind != KindPlugin {
		t.Errorf("Kind mismatch: %q", summary.Kind)
	}
	if summary.Project != "my-project" {
		t.Errorf("Project mismatch: %q", summary.Project)
	}
	if summary.Priority != 75 {
		t.Errorf("Priority mismatch: %d", summary.Priority)
	}
}

func mustMarshal(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
