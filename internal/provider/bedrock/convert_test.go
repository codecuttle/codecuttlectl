package bedrockprov

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/provider"
)

// TestProviderMsgToBedrock_EmptyToolInput is a regression test for the Bedrock
// ValidationException: "The value at messages.N.content.M.toolUse.input is empty."
// Zero-argument tool calls (e.g. reload_plugins) may carry empty, nil, or literal
// "null" input; these must serialize as {} — never as JSON null.
func TestProviderMsgToBedrock_EmptyToolInput(t *testing.T) {
	cases := []struct {
		name  string
		input []byte
	}{
		{"nil input", nil},
		{"empty input", []byte("")},
		{"literal null", []byte("null")},
		{"invalid json", []byte("{not json")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := providerMsgToBedrock(provider.Message{
				Role: provider.RoleAssistant,
				Content: []provider.ContentBlock{
					provider.ToolUseBlock{ToolUseID: "tu1", Name: "reload_plugins", Input: tc.input},
				},
			})

			if len(msg.Content) != 1 {
				t.Fatalf("expected 1 content block, got %d", len(msg.Content))
			}
			tu, ok := msg.Content[0].(*types.ContentBlockMemberToolUse)
			if !ok {
				t.Fatalf("expected ToolUse block, got %T", msg.Content[0])
			}
			if tu.Value.Input == nil {
				t.Fatal("toolUse.Input document is nil")
			}

			data, err := tu.Value.Input.MarshalSmithyDocument()
			if err != nil {
				t.Fatalf("marshaling input document: %v", err)
			}
			if string(data) != "{}" {
				t.Errorf("expected toolUse.input to serialize as {}, got %q", string(data))
			}
		})
	}
}

// TestProviderMsgToBedrock_ValidToolInput ensures normal inputs are preserved.
func TestProviderMsgToBedrock_ValidToolInput(t *testing.T) {
	msg := providerMsgToBedrock(provider.Message{
		Role: provider.RoleAssistant,
		Content: []provider.ContentBlock{
			provider.ToolUseBlock{ToolUseID: "tu1", Name: "read_file", Input: []byte(`{"path":"/foo"}`)},
		},
	})

	tu, ok := msg.Content[0].(*types.ContentBlockMemberToolUse)
	if !ok {
		t.Fatalf("expected ToolUse block, got %T", msg.Content[0])
	}
	data, err := tu.Value.Input.MarshalSmithyDocument()
	if err != nil {
		t.Fatalf("marshaling input document: %v", err)
	}
	if string(data) != `{"path":"/foo"}` {
		t.Errorf("unexpected input serialization: %q", string(data))
	}
}
