package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLoggerEmitsJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, true)

	logger.ToolExec("ses_abc123", "bash_exec", "tu_1", 150, false, "", 1, 0)

	output := buf.String()
	if output == "" {
		t.Fatal("expected output, got empty string")
	}

	// Should be valid JSON
	var event Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &event); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}

	if event.Type != "tool_exec" {
		t.Errorf("expected type 'tool_exec', got %q", event.Type)
	}
	if event.SessionID != "ses_abc123" {
		t.Errorf("expected session_id 'ses_abc123', got %q", event.SessionID)
	}
	if event.ToolName != "bash_exec" {
		t.Errorf("expected tool_name 'bash_exec', got %q", event.ToolName)
	}
	if event.DurationMs != 150 {
		t.Errorf("expected duration_ms 150, got %d", event.DurationMs)
	}
	if event.Level != "info" {
		t.Errorf("expected level 'info', got %q", event.Level)
	}
}

func TestLoggerErrorEvent(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, true)

	logger.ToolExec("ses_abc123", "bash_exec", "tu_2", 50, true, "compile", 2, 1)

	var event Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &event); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if event.Level != "warn" {
		t.Errorf("expected level 'warn' for error, got %q", event.Level)
	}
	if !event.IsError {
		t.Error("expected is_error=true")
	}
	if event.ErrorType != "compile" {
		t.Errorf("expected error_type 'compile', got %q", event.ErrorType)
	}
}

func TestLoggerDisabled(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, false)

	logger.ToolExec("ses_abc123", "bash_exec", "tu_1", 150, false, "", 1, 0)

	if buf.Len() > 0 {
		t.Errorf("expected no output when disabled, got: %s", buf.String())
	}
}

func TestLoggerTokenUsage(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, true)

	logger.TokenUsage("ses_abc123", "us.anthropic.claude-opus-4-6-v1", 5000, 1200, 4000, 500, 1, 0)

	var event Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &event); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if event.Type != "token_usage" {
		t.Errorf("expected type 'token_usage', got %q", event.Type)
	}
	if event.InputTokens != 5000 {
		t.Errorf("expected input_tokens 5000, got %d", event.InputTokens)
	}
	if event.OutputTokens != 1200 {
		t.Errorf("expected output_tokens 1200, got %d", event.OutputTokens)
	}
	if event.CacheReadTokens != 4000 {
		t.Errorf("expected cache_read_tokens 4000, got %d", event.CacheReadTokens)
	}
	if event.CacheWriteTokens != 500 {
		t.Errorf("expected cache_write_tokens 500, got %d", event.CacheWriteTokens)
	}
	if event.ModelID != "us.anthropic.claude-opus-4-6-v1" {
		t.Errorf("expected model_id, got %q", event.ModelID)
	}
}

func TestLoggerApprovalEvent(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, true)

	logger.ApprovalEvent("ses_abc123", "bash_exec", "critical", "denied", 3)

	var event Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &event); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if event.Type != "approval" {
		t.Errorf("expected type 'approval', got %q", event.Type)
	}
	if event.Level != "security" {
		t.Errorf("expected level 'security', got %q", event.Level)
	}
	if event.Risk != "critical" {
		t.Errorf("expected risk 'critical', got %q", event.Risk)
	}
	if event.ApprovalDecision != "denied" {
		t.Errorf("expected approval_decision 'denied', got %q", event.ApprovalDecision)
	}
}

func TestLoggerSessionLifecycle(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, true)

	logger.SessionStart("ses_abc123", "us.anthropic.claude-opus-4-6-v1", "iam_role", "arn:aws:iam::123:role/dev")
	logger.SessionEnd("ses_abc123", 5, 25000, 8000, 45000)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var startEvent Event
	if err := json.Unmarshal([]byte(lines[0]), &startEvent); err != nil {
		t.Fatalf("start event not valid JSON: %v", err)
	}
	if startEvent.Type != "session_start" {
		t.Errorf("expected type 'session_start', got %q", startEvent.Type)
	}
	if startEvent.ModelID != "us.anthropic.claude-opus-4-6-v1" {
		t.Errorf("expected model_id, got %q", startEvent.ModelID)
	}

	var endEvent Event
	if err := json.Unmarshal([]byte(lines[1]), &endEvent); err != nil {
		t.Fatalf("end event not valid JSON: %v", err)
	}
	if endEvent.Type != "session_end" {
		t.Errorf("expected type 'session_end', got %q", endEvent.Type)
	}
}

func TestLoggerConcurrency(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, true)

	// Emit 100 events concurrently to verify thread safety
	done := make(chan struct{}, 100)
	for i := 0; i < 100; i++ {
		go func(n int) {
			logger.ToolExec("ses_abc123", "bash_exec", "tu_"+string(rune('0'+n%10)), int64(n), false, "", 1, 0)
			done <- struct{}{}
		}(i)
	}

	for i := 0; i < 100; i++ {
		<-done
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 100 {
		t.Errorf("expected 100 lines, got %d", len(lines))
	}

	// Each line should be valid JSON
	for i, line := range lines {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestEventTimestamp(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, true)

	before := time.Now().UTC()
	logger.Emit(Event{
		Level:     "info",
		Type:      "test",
		SessionID: "ses_abc123",
	})
	after := time.Now().UTC()

	var event Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &event); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}

	if event.Timestamp.Before(before) || event.Timestamp.After(after) {
		t.Errorf("timestamp %v not between %v and %v", event.Timestamp, before, after)
	}
}

func TestToolDisciplineBlock(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, true)

	logger.ToolDisciplineBlock("ses_abc123", "bash_exec", "Use the 'git' tool instead", 2)

	var event Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &event); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}

	if event.Type != "tool_discipline_block" {
		t.Errorf("expected type 'tool_discipline_block', got %q", event.Type)
	}
	if event.Level != "security" {
		t.Errorf("expected level 'security', got %q", event.Level)
	}
	if !event.WasBlocked {
		t.Error("expected was_blocked=true")
	}
	if event.BlockReason != "Use the 'git' tool instead" {
		t.Errorf("expected block_reason, got %q", event.BlockReason)
	}
}
