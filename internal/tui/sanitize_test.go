package tui

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestSanitizeHistory_NoChange(t *testing.T) {
	msgs := []types.Message{
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "hi"}}},
		{Role: types.ConversationRoleAssistant, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "hello"}}},
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "how are you"}}},
		{Role: types.ConversationRoleAssistant, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "fine"}}},
	}

	result := sanitizeHistory(msgs)
	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}
}

func TestSanitizeHistory_ConsecutiveUser(t *testing.T) {
	msgs := []types.Message{
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "do the thing"}}},
		{Role: types.ConversationRoleAssistant, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "ok doing it"}}},
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "you stopped"}}},
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "continue please"}}},
	}

	result := sanitizeHistory(msgs)
	if len(result) != 5 {
		t.Fatalf("expected 5 messages (1 injected), got %d", len(result))
	}

	// Verify the injected message is an assistant message between the two user messages
	if result[2].Role != types.ConversationRoleUser {
		t.Errorf("msg[2] role=%s, want user", result[2].Role)
	}
	if result[3].Role != types.ConversationRoleAssistant {
		t.Errorf("msg[3] role=%s, want assistant (injected)", result[3].Role)
	}
	if result[4].Role != types.ConversationRoleUser {
		t.Errorf("msg[4] role=%s, want user", result[4].Role)
	}

	// Check the injected text
	text := result[3].Content[0].(*types.ContentBlockMemberText).Value
	if text == "" {
		t.Error("injected message should have text content")
	}
}

func TestSanitizeHistory_ConsecutiveAssistant(t *testing.T) {
	msgs := []types.Message{
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "hi"}}},
		{Role: types.ConversationRoleAssistant, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "part 1"}}},
		{Role: types.ConversationRoleAssistant, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "part 2"}}},
	}

	result := sanitizeHistory(msgs)
	if len(result) != 4 {
		t.Fatalf("expected 4 messages (1 injected), got %d", len(result))
	}
	if result[2].Role != types.ConversationRoleUser {
		t.Errorf("msg[2] role=%s, want user (injected)", result[2].Role)
	}
	if result[3].Role != types.ConversationRoleAssistant {
		t.Errorf("msg[3] role=%s, want assistant", result[3].Role)
	}
}

func TestSanitizeHistory_MultipleConsecutive(t *testing.T) {
	msgs := []types.Message{
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "a"}}},
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "b"}}},
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "c"}}},
	}

	result := sanitizeHistory(msgs)
	// a, [inject assistant], b, [inject assistant], c = 5
	if len(result) != 5 {
		t.Fatalf("expected 5 messages (2 injected), got %d", len(result))
	}

	// Verify strict alternation
	for i := 1; i < len(result); i++ {
		if result[i].Role == result[i-1].Role {
			t.Errorf("consecutive same role at %d and %d: %s", i-1, i, result[i].Role)
		}
	}
}

func TestSanitizeHistory_Empty(t *testing.T) {
	result := sanitizeHistory(nil)
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestSanitizeHistory_Single(t *testing.T) {
	msgs := []types.Message{
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "hi"}}},
	}
	result := sanitizeHistory(msgs)
	if len(result) != 1 {
		t.Errorf("expected 1, got %d", len(result))
	}
}
