package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/todo"
)

// --- ID Generation Tests ---

func TestGenerateID(t *testing.T) {
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID() error: %v", err)
	}
	if len(id) != 12 { // "ses_" (4) + 8 hex chars
		t.Errorf("expected ID length 12, got %d: %q", len(id), id)
	}
	if id[:4] != "ses_" {
		t.Errorf("expected ID prefix 'ses_', got %q", id[:4])
	}
}

func TestGenerateIDUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id, err := GenerateID()
		if err != nil {
			t.Fatalf("GenerateID() iteration %d error: %v", i, err)
		}
		if seen[id] {
			t.Fatalf("duplicate ID generated at iteration %d: %s", i, id)
		}
		seen[id] = true
	}
}

// --- Message Serialization Tests ---

func TestMarshalUnmarshalTextOnly(t *testing.T) {
	messages := []types.Message{
		{
			Role: types.ConversationRoleUser,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: "Hello, world!"},
			},
		},
		{
			Role: types.ConversationRoleAssistant,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: "Hi there! How can I help?"},
			},
		},
	}

	serialized, err := MarshalHistory(messages)
	if err != nil {
		t.Fatalf("MarshalHistory() error: %v", err)
	}

	if len(serialized) != 2 {
		t.Fatalf("expected 2 serialized messages, got %d", len(serialized))
	}
	if serialized[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", serialized[0].Role)
	}
	if serialized[0].Blocks[0].Type != "text" {
		t.Errorf("expected type 'text', got %q", serialized[0].Blocks[0].Type)
	}
	if serialized[0].Blocks[0].Text != "Hello, world!" {
		t.Errorf("expected text 'Hello, world!', got %q", serialized[0].Blocks[0].Text)
	}

	// Round-trip
	restored, err := UnmarshalHistory(serialized)
	if err != nil {
		t.Fatalf("UnmarshalHistory() error: %v", err)
	}

	if len(restored) != 2 {
		t.Fatalf("expected 2 restored messages, got %d", len(restored))
	}
	if restored[0].Role != types.ConversationRoleUser {
		t.Errorf("expected role user, got %v", restored[0].Role)
	}
	textBlock, ok := restored[0].Content[0].(*types.ContentBlockMemberText)
	if !ok {
		t.Fatalf("expected ContentBlockMemberText, got %T", restored[0].Content[0])
	}
	if textBlock.Value != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got %q", textBlock.Value)
	}
}

func TestMarshalUnmarshalToolUse(t *testing.T) {
	toolInput := map[string]interface{}{
		"path":   "/tmp/test.go",
		"offset": float64(0),
		"limit":  float64(100),
	}

	messages := []types.Message{
		{
			Role: types.ConversationRoleAssistant,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: "Let me read that file."},
				&types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						ToolUseId: aws.String("tu_abc123"),
						Name:      aws.String("read_file"),
						Input:     document.NewLazyDocument(toolInput),
					},
				},
			},
		},
	}

	serialized, err := MarshalHistory(messages)
	if err != nil {
		t.Fatalf("MarshalHistory() error: %v", err)
	}

	if len(serialized) != 1 {
		t.Fatalf("expected 1 message, got %d", len(serialized))
	}
	if len(serialized[0].Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(serialized[0].Blocks))
	}

	// Verify tool_use block
	tuBlock := serialized[0].Blocks[1]
	if tuBlock.Type != "tool_use" {
		t.Errorf("expected type 'tool_use', got %q", tuBlock.Type)
	}
	if tuBlock.ToolUseID != "tu_abc123" {
		t.Errorf("expected tool_use_id 'tu_abc123', got %q", tuBlock.ToolUseID)
	}
	if tuBlock.Name != "read_file" {
		t.Errorf("expected name 'read_file', got %q", tuBlock.Name)
	}

	// Verify input JSON
	var parsedInput map[string]interface{}
	if err := json.Unmarshal(tuBlock.Input, &parsedInput); err != nil {
		t.Fatalf("parsing tool input JSON: %v", err)
	}
	if parsedInput["path"] != "/tmp/test.go" {
		t.Errorf("expected path '/tmp/test.go', got %v", parsedInput["path"])
	}

	// Round-trip back to Bedrock types
	restored, err := UnmarshalHistory(serialized)
	if err != nil {
		t.Fatalf("UnmarshalHistory() error: %v", err)
	}

	if len(restored) != 1 {
		t.Fatalf("expected 1 restored message, got %d", len(restored))
	}
	if restored[0].Role != types.ConversationRoleAssistant {
		t.Errorf("expected assistant role, got %v", restored[0].Role)
	}

	// Verify text block survived
	restoredText, ok := restored[0].Content[0].(*types.ContentBlockMemberText)
	if !ok {
		t.Fatalf("expected ContentBlockMemberText, got %T", restored[0].Content[0])
	}
	if restoredText.Value != "Let me read that file." {
		t.Errorf("expected text, got %q", restoredText.Value)
	}

	// Verify tool_use block survived with document.Interface
	restoredTU, ok := restored[0].Content[1].(*types.ContentBlockMemberToolUse)
	if !ok {
		t.Fatalf("expected ContentBlockMemberToolUse, got %T", restored[0].Content[1])
	}
	if aws.ToString(restoredTU.Value.ToolUseId) != "tu_abc123" {
		t.Errorf("expected tool_use_id 'tu_abc123', got %q", aws.ToString(restoredTU.Value.ToolUseId))
	}
	if aws.ToString(restoredTU.Value.Name) != "read_file" {
		t.Errorf("expected name 'read_file', got %q", aws.ToString(restoredTU.Value.Name))
	}

	// Verify input was reconstructed as a document.Interface
	if restoredTU.Value.Input == nil {
		t.Fatal("expected non-nil Input document")
	}
	// Use MarshalSmithyDocument to get JSON and verify content
	inputBytes, err := restoredTU.Value.Input.MarshalSmithyDocument()
	if err != nil {
		t.Fatalf("MarshalSmithyDocument: %v", err)
	}
	var restoredMap map[string]interface{}
	if err := json.Unmarshal(inputBytes, &restoredMap); err != nil {
		t.Fatalf("json.Unmarshal restored input: %v", err)
	}
	if restoredMap["path"] != "/tmp/test.go" {
		t.Errorf("expected path '/tmp/test.go', got %v", restoredMap["path"])
	}
}

func TestMarshalUnmarshalToolResult(t *testing.T) {
	messages := []types.Message{
		{
			Role: types.ConversationRoleUser,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberToolResult{
					Value: types.ToolResultBlock{
						ToolUseId: aws.String("tu_abc123"),
						Content: []types.ToolResultContentBlock{
							&types.ToolResultContentBlockMemberText{Value: "file contents here"},
						},
						Status: types.ToolResultStatusSuccess,
					},
				},
			},
		},
	}

	serialized, err := MarshalHistory(messages)
	if err != nil {
		t.Fatalf("MarshalHistory() error: %v", err)
	}

	if serialized[0].Blocks[0].Type != "tool_result" {
		t.Errorf("expected type 'tool_result', got %q", serialized[0].Blocks[0].Type)
	}
	if serialized[0].Blocks[0].Status != "success" {
		t.Errorf("expected status 'success', got %q", serialized[0].Blocks[0].Status)
	}
	if serialized[0].Blocks[0].ResultFor != "tu_abc123" {
		t.Errorf("expected result_for 'tu_abc123', got %q", serialized[0].Blocks[0].ResultFor)
	}

	// Round-trip
	restored, err := UnmarshalHistory(serialized)
	if err != nil {
		t.Fatalf("UnmarshalHistory() error: %v", err)
	}

	trBlock, ok := restored[0].Content[0].(*types.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("expected ContentBlockMemberToolResult, got %T", restored[0].Content[0])
	}
	if aws.ToString(trBlock.Value.ToolUseId) != "tu_abc123" {
		t.Errorf("expected tool_use_id 'tu_abc123', got %q", aws.ToString(trBlock.Value.ToolUseId))
	}
	if trBlock.Value.Status != types.ToolResultStatusSuccess {
		t.Errorf("expected success status, got %v", trBlock.Value.Status)
	}
}

func TestMarshalUnmarshalToolResultError(t *testing.T) {
	messages := []types.Message{
		{
			Role: types.ConversationRoleUser,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberToolResult{
					Value: types.ToolResultBlock{
						ToolUseId: aws.String("tu_err456"),
						Content: []types.ToolResultContentBlock{
							&types.ToolResultContentBlockMemberText{Value: "Error: file not found"},
						},
						Status: types.ToolResultStatusError,
					},
				},
			},
		},
	}

	serialized, err := MarshalHistory(messages)
	if err != nil {
		t.Fatalf("MarshalHistory() error: %v", err)
	}

	if serialized[0].Blocks[0].Status != "error" {
		t.Errorf("expected status 'error', got %q", serialized[0].Blocks[0].Status)
	}

	restored, err := UnmarshalHistory(serialized)
	if err != nil {
		t.Fatalf("UnmarshalHistory() error: %v", err)
	}

	trBlock := restored[0].Content[0].(*types.ContentBlockMemberToolResult)
	if trBlock.Value.Status != types.ToolResultStatusError {
		t.Errorf("expected error status, got %v", trBlock.Value.Status)
	}
}

func TestMarshalUnmarshalMultiTurn(t *testing.T) {
	// Simulate: user asks → assistant calls tool → tool result → assistant responds
	messages := []types.Message{
		{
			Role: types.ConversationRoleUser,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: "Read my config file"},
			},
		},
		{
			Role: types.ConversationRoleAssistant,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						ToolUseId: aws.String("tu_001"),
						Name:      aws.String("read_file"),
						Input:     document.NewLazyDocument(map[string]interface{}{"path": "/etc/config.yaml"}),
					},
				},
			},
		},
		{
			Role: types.ConversationRoleUser,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberToolResult{
					Value: types.ToolResultBlock{
						ToolUseId: aws.String("tu_001"),
						Content: []types.ToolResultContentBlock{
							&types.ToolResultContentBlockMemberText{Value: "key: value\nport: 8080"},
						},
						Status: types.ToolResultStatusSuccess,
					},
				},
			},
		},
		{
			Role: types.ConversationRoleAssistant,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: "Your config has key=value and port=8080."},
			},
		},
	}

	serialized, err := MarshalHistory(messages)
	if err != nil {
		t.Fatalf("MarshalHistory() error: %v", err)
	}

	if len(serialized) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(serialized))
	}

	// Verify the full round-trip
	restored, err := UnmarshalHistory(serialized)
	if err != nil {
		t.Fatalf("UnmarshalHistory() error: %v", err)
	}

	if len(restored) != 4 {
		t.Fatalf("expected 4 restored messages, got %d", len(restored))
	}

	// Verify roles
	expectedRoles := []types.ConversationRole{
		types.ConversationRoleUser,
		types.ConversationRoleAssistant,
		types.ConversationRoleUser,
		types.ConversationRoleAssistant,
	}
	for i, expected := range expectedRoles {
		if restored[i].Role != expected {
			t.Errorf("message %d: expected role %v, got %v", i, expected, restored[i].Role)
		}
	}

	// Verify final text
	finalText := restored[3].Content[0].(*types.ContentBlockMemberText)
	if finalText.Value != "Your config has key=value and port=8080." {
		t.Errorf("unexpected final text: %q", finalText.Value)
	}
}

func TestMarshalEmptyHistory(t *testing.T) {
	serialized, err := MarshalHistory(nil)
	if err != nil {
		t.Fatalf("MarshalHistory(nil) error: %v", err)
	}
	if len(serialized) != 0 {
		t.Errorf("expected empty result, got %d messages", len(serialized))
	}

	restored, err := UnmarshalHistory(nil)
	if err != nil {
		t.Fatalf("UnmarshalHistory(nil) error: %v", err)
	}
	if len(restored) != 0 {
		t.Errorf("expected empty result, got %d messages", len(restored))
	}
}

func TestMarshalToolUseEmptyInput(t *testing.T) {
	messages := []types.Message{
		{
			Role: types.ConversationRoleAssistant,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						ToolUseId: aws.String("tu_empty"),
						Name:      aws.String("some_tool"),
						Input:     document.NewLazyDocument(map[string]interface{}{}),
					},
				},
			},
		},
	}

	serialized, err := MarshalHistory(messages)
	if err != nil {
		t.Fatalf("MarshalHistory() error: %v", err)
	}

	// Verify the input serialized as empty object
	if string(serialized[0].Blocks[0].Input) != "{}" {
		t.Errorf("expected '{}', got %q", string(serialized[0].Blocks[0].Input))
	}

	restored, err := UnmarshalHistory(serialized)
	if err != nil {
		t.Fatalf("UnmarshalHistory() error: %v", err)
	}

	tu := restored[0].Content[0].(*types.ContentBlockMemberToolUse)
	if tu.Value.Input == nil {
		t.Fatal("expected non-nil input document for empty input")
	}
}

func TestMarshalToolUseComplexNestedInput(t *testing.T) {
	complexInput := map[string]interface{}{
		"command": "find / -name '*.go' | head -10",
		"nested": map[string]interface{}{
			"array": []interface{}{1.0, "two", true, nil},
			"deep": map[string]interface{}{
				"key": "value",
			},
		},
		"timeout": 30.0,
	}

	messages := []types.Message{
		{
			Role: types.ConversationRoleAssistant,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						ToolUseId: aws.String("tu_complex"),
						Name:      aws.String("bash_exec"),
						Input:     document.NewLazyDocument(complexInput),
					},
				},
			},
		},
	}

	serialized, err := MarshalHistory(messages)
	if err != nil {
		t.Fatalf("MarshalHistory() error: %v", err)
	}

	restored, err := UnmarshalHistory(serialized)
	if err != nil {
		t.Fatalf("UnmarshalHistory() error: %v", err)
	}

	tu := restored[0].Content[0].(*types.ContentBlockMemberToolUse)
	// Use MarshalSmithyDocument to verify round-tripped content
	inputBytes, err := tu.Value.Input.MarshalSmithyDocument()
	if err != nil {
		t.Fatalf("MarshalSmithyDocument: %v", err)
	}
	var restoredInput map[string]interface{}
	if err := json.Unmarshal(inputBytes, &restoredInput); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if restoredInput["command"] != "find / -name '*.go' | head -10" {
		t.Errorf("command mismatch: %v", restoredInput["command"])
	}
	nested, ok := restoredInput["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nested map, got %T", restoredInput["nested"])
	}
	arr, ok := nested["array"].([]interface{})
	if !ok {
		t.Fatalf("expected array, got %T", nested["array"])
	}
	if len(arr) != 4 {
		t.Errorf("expected 4 array elements, got %d", len(arr))
	}
}

func TestMarshalJSONRoundTrip(t *testing.T) {
	// Verify that our Message type survives a JSON marshal/unmarshal cycle
	// (simulating what happens when written to and read from disk)
	messages := []Message{
		{
			Role: "user",
			Blocks: []ContentItem{
				{Type: "text", Text: "hello"},
			},
		},
		{
			Role: "assistant",
			Blocks: []ContentItem{
				{Type: "text", Text: "I'll check."},
				{Type: "tool_use", ToolUseID: "tu_1", Name: "read_file", Input: json.RawMessage(`{"path":"/tmp/x"}`)},
			},
		},
		{
			Role: "user",
			Blocks: []ContentItem{
				{Type: "tool_result", Content: "file contents", Status: "success", ResultFor: "tu_1"},
			},
		},
	}

	data, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var decoded []Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if len(decoded) != 3 {
		t.Fatalf("expected 3, got %d", len(decoded))
	}
	if decoded[1].Blocks[1].Name != "read_file" {
		t.Errorf("expected 'read_file', got %q", decoded[1].Blocks[1].Name)
	}
}

// --- FileStore Tests ---

func TestFileStoreCreateLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	meta := SessionMeta{
		Title:   "test session",
		Model:   "us.anthropic.claude-opus-4-6-v1",
		Region:  "us-east-1",
		WorkDir: "/home/test",
		Stats:   Stats{InputTokens: 100, OutputTokens: 50, ToolCalls: 2, Turns: 1},
	}

	id, err := store.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if id[:4] != "ses_" {
		t.Errorf("expected ID prefix 'ses_', got %q", id[:4])
	}

	// Load and verify
	state, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if state.Meta.ID != id {
		t.Errorf("expected ID %q, got %q", id, state.Meta.ID)
	}
	if state.Meta.Title != "test session" {
		t.Errorf("expected title 'test session', got %q", state.Meta.Title)
	}
	if state.Meta.Model != "us.anthropic.claude-opus-4-6-v1" {
		t.Errorf("expected model, got %q", state.Meta.Model)
	}
	if state.Meta.Stats.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", state.Meta.Stats.InputTokens)
	}
}

func TestFileStoreSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	id, err := store.Create(SessionMeta{
		Title: "initial",
		Model: "test-model",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Load, modify, save
	state, _ := store.Load(id)
	state.Meta.Title = "updated title"
	state.Meta.Stats.Turns = 5
	state.Messages = []Message{
		{Role: "user", Blocks: []ContentItem{{Type: "text", Text: "hello"}}},
	}
	state.Todos = []todo.Item{
		{Content: "fix bug", Status: "in_progress", Priority: "high"},
	}
	state.Inkwell = []InkEntry{
		{
			Timestamp:  time.Now().UTC(),
			Turn:       1,
			ToolName:   "bash_exec",
			ToolUseID:  "tu_001",
			DurationMs: 150,
			IsError:    false,
		},
	}

	if err := store.Save(id, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload and verify all changes persisted
	reloaded, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}

	if reloaded.Meta.Title != "updated title" {
		t.Errorf("expected 'updated title', got %q", reloaded.Meta.Title)
	}
	if reloaded.Meta.Stats.Turns != 5 {
		t.Errorf("expected 5 turns, got %d", reloaded.Meta.Stats.Turns)
	}
	if len(reloaded.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(reloaded.Messages))
	}
	if len(reloaded.Todos) != 1 {
		t.Errorf("expected 1 todo, got %d", len(reloaded.Todos))
	}
	if reloaded.Todos[0].Content != "fix bug" {
		t.Errorf("expected 'fix bug', got %q", reloaded.Todos[0].Content)
	}
	if len(reloaded.Inkwell) != 1 {
		t.Errorf("expected 1 inkwell entry, got %d", len(reloaded.Inkwell))
	}
	if reloaded.Inkwell[0].ToolName != "bash_exec" {
		t.Errorf("expected 'bash_exec', got %q", reloaded.Inkwell[0].ToolName)
	}

	// Verify updated_at changed
	if !reloaded.Meta.UpdatedAt.After(reloaded.Meta.CreatedAt) {
		t.Error("expected updated_at to be after created_at")
	}
}

func TestFileStoreList(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Create 5 sessions with staggered timestamps
	ids := make([]string, 5)
	for i := 0; i < 5; i++ {
		id, err := store.Create(SessionMeta{
			Title: "session " + string(rune('A'+i)),
			Model: "test-model",
		})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		ids[i] = id
		// Small delay to ensure distinct updated_at
		time.Sleep(10 * time.Millisecond)
	}

	// List all
	metas, err := store.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 5 {
		t.Fatalf("expected 5 sessions, got %d", len(metas))
	}

	// Verify sorted by most recent first
	for i := 1; i < len(metas); i++ {
		if metas[i].UpdatedAt.After(metas[i-1].UpdatedAt) {
			t.Errorf("sessions not sorted by recency: %v after %v at index %d",
				metas[i].UpdatedAt, metas[i-1].UpdatedAt, i)
		}
	}

	// Test limit
	limited, err := store.List(3)
	if err != nil {
		t.Fatalf("List(3): %v", err)
	}
	if len(limited) != 3 {
		t.Errorf("expected 3 sessions with limit, got %d", len(limited))
	}
}

func TestFileStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	id, err := store.Create(SessionMeta{Title: "to delete"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify it exists
	_, err = store.Load(id)
	if err != nil {
		t.Fatalf("Load before delete: %v", err)
	}

	// Delete
	if err := store.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify it's gone
	_, err = store.Load(id)
	if err == nil {
		t.Fatal("expected error loading deleted session")
	}
}

func TestFileStoreDeleteNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	err = store.Delete("ses_nonexistent")
	if err == nil {
		t.Fatal("expected error deleting non-existent session")
	}
}

func TestFileStorePrune(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Create sessions with different timestamps
	oldID, _ := store.Create(SessionMeta{Title: "old"})
	recentID, _ := store.Create(SessionMeta{Title: "recent"})

	// Manually backdate the "old" session
	oldState, _ := store.Load(oldID)
	oldState.Meta.UpdatedAt = time.Now().UTC().Add(-48 * time.Hour)
	// Write directly to bypass the auto-update of UpdatedAt
	data, _ := json.MarshalIndent(oldState, "", "  ")
	os.WriteFile(filepath.Join(dir, oldID+".json"), data, 0600)

	// Prune sessions older than 24 hours
	deleted, err := store.Prune(24 * time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// Verify old is gone
	_, err = store.Load(oldID)
	if err == nil {
		t.Error("expected old session to be pruned")
	}

	// Verify recent survives
	_, err = store.Load(recentID)
	if err != nil {
		t.Errorf("expected recent session to survive prune: %v", err)
	}
}

func TestFileStoreAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	id, _ := store.Create(SessionMeta{Title: "atomic test"})

	// Save valid state
	state, _ := store.Load(id)
	state.Messages = []Message{
		{Role: "user", Blocks: []ContentItem{{Type: "text", Text: "first"}}},
	}
	store.Save(id, state)

	// Verify no .tmp files left behind
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("found leftover .tmp file: %s", e.Name())
		}
	}

	// Verify state persisted correctly
	reloaded, _ := store.Load(id)
	if len(reloaded.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(reloaded.Messages))
	}
}

func TestFileStoreLoadNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	_, err = store.Load("ses_doesnotexist")
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
}

func TestFileStoreEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	metas, err := store.List(10)
	if err != nil {
		t.Fatalf("List on empty dir: %v", err)
	}
	if len(metas) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(metas))
	}
}

// --- Session Restore Tests (simulating TUI restore flow) ---

func TestSessionSaveAndRestoreWithHistory(t *testing.T) {
	// This test simulates the full TUI flow:
	// 1. Create a session
	// 2. Have a multi-turn conversation with tool use
	// 3. Save the session
	// 4. Load it back and verify all messages are reconstructable

	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Create session
	id, err := store.Create(SessionMeta{
		Title:   "test restore",
		Model:   "test-model",
		Region:  "us-east-1",
		WorkDir: "/home/test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Build a realistic conversation with tool use
	messages := []Message{
		{
			Role: "user",
			Blocks: []ContentItem{
				{Type: "text", Text: "List the files in /tmp"},
			},
		},
		{
			Role: "assistant",
			Blocks: []ContentItem{
				{Type: "tool_use", ToolUseID: "tu_001", Name: "list_directory", Input: json.RawMessage(`{"path":"/tmp"}`)},
			},
		},
		{
			Role: "user",
			Blocks: []ContentItem{
				{Type: "tool_result", Content: "file1.txt\nfile2.txt\nsubdir/\n", Status: "success", ResultFor: "tu_001"},
			},
		},
		{
			Role: "assistant",
			Blocks: []ContentItem{
				{Type: "text", Text: "The /tmp directory contains file1.txt, file2.txt, and a subdirectory."},
			},
		},
	}

	// Save session with messages
	state := &SessionState{
		Meta: SessionMeta{
			ID:      id,
			Title:   "test restore",
			Model:   "test-model",
			Region:  "us-east-1",
			WorkDir: "/home/test",
			Stats:   Stats{Turns: 1, ToolCalls: 1},
		},
		Messages: messages,
		Todos: []todo.Item{
			{Content: "List files", Status: "completed", Priority: "high"},
		},
		Inkwell: []InkEntry{
			{
				ToolName:   "list_directory",
				ToolUseID:  "tu_001",
				DurationMs: 5,
				IsError:    false,
			},
		},
	}

	if err := store.Save(id, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load and verify
	loaded, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify messages survived
	if len(loaded.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(loaded.Messages))
	}

	// Verify we can reconstruct display messages (what the TUI does)
	for i, msg := range loaded.Messages {
		for _, block := range msg.Blocks {
			switch block.Type {
			case "text":
				if block.Text == "" {
					t.Errorf("message %d: empty text block", i)
				}
			case "tool_use":
				if block.Name == "" {
					t.Errorf("message %d: empty tool_use name", i)
				}
				if block.ToolUseID == "" {
					t.Errorf("message %d: empty tool_use id", i)
				}
			case "tool_result":
				if block.ResultFor == "" {
					t.Errorf("message %d: empty result_for", i)
				}
			default:
				t.Errorf("message %d: unexpected block type %q", i, block.Type)
			}
		}
	}

	// Verify the messages can be converted back to Bedrock types
	bedrockMsgs, err := UnmarshalHistory(loaded.Messages)
	if err != nil {
		t.Fatalf("UnmarshalHistory from loaded session: %v", err)
	}
	if len(bedrockMsgs) != 4 {
		t.Fatalf("expected 4 bedrock messages, got %d", len(bedrockMsgs))
	}

	// Verify roles
	if bedrockMsgs[0].Role != types.ConversationRoleUser {
		t.Errorf("msg 0: expected user, got %v", bedrockMsgs[0].Role)
	}
	if bedrockMsgs[1].Role != types.ConversationRoleAssistant {
		t.Errorf("msg 1: expected assistant, got %v", bedrockMsgs[1].Role)
	}
	if bedrockMsgs[2].Role != types.ConversationRoleUser {
		t.Errorf("msg 2: expected user, got %v", bedrockMsgs[2].Role)
	}
	if bedrockMsgs[3].Role != types.ConversationRoleAssistant {
		t.Errorf("msg 3: expected assistant, got %v", bedrockMsgs[3].Role)
	}

	// Verify tool_use block has valid input
	tuBlock, ok := bedrockMsgs[1].Content[0].(*types.ContentBlockMemberToolUse)
	if !ok {
		t.Fatalf("msg 1: expected tool_use, got %T", bedrockMsgs[1].Content[0])
	}
	if aws.ToString(tuBlock.Value.Name) != "list_directory" {
		t.Errorf("expected list_directory, got %q", aws.ToString(tuBlock.Value.Name))
	}
	if tuBlock.Value.Input == nil {
		t.Fatal("tool_use input should not be nil")
	}

	// Verify todos survived
	if len(loaded.Todos) != 1 {
		t.Fatalf("expected 1 todo, got %d", len(loaded.Todos))
	}
	if loaded.Todos[0].Content != "List files" {
		t.Errorf("expected 'List files', got %q", loaded.Todos[0].Content)
	}

	// Verify inkwell survived
	if len(loaded.Inkwell) != 1 {
		t.Fatalf("expected 1 inkwell entry, got %d", len(loaded.Inkwell))
	}
	if loaded.Inkwell[0].ToolName != "list_directory" {
		t.Errorf("expected 'list_directory', got %q", loaded.Inkwell[0].ToolName)
	}
}

func TestSessionRestoreEmptySession(t *testing.T) {
	// Verify that loading a session with no messages works correctly
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	id, err := store.Create(SessionMeta{Title: "empty"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	loaded, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded.Messages) != 0 {
		t.Errorf("expected 0 messages for fresh session, got %d", len(loaded.Messages))
	}

	// Should still be convertible to Bedrock types
	bedrockMsgs, err := UnmarshalHistory(loaded.Messages)
	if err != nil {
		t.Fatalf("UnmarshalHistory for empty: %v", err)
	}
	if len(bedrockMsgs) != 0 {
		t.Errorf("expected 0 bedrock messages, got %d", len(bedrockMsgs))
	}
}
