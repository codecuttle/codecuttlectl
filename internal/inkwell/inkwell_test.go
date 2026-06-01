package inkwell

import (
	"testing"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/session"
)

// --- Classifier Tests ---

func TestClassifyGoCompileError(t *testing.T) {
	output := `# github.com/codecuttle/codecuttlectl/internal/bedrock
internal/bedrock/client.go:200:58: cannot use doc (variable of map type document) as "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document".Interface value`

	ce := Classify("bash_exec", output, true)

	if ce.Language != "go" {
		t.Errorf("expected language 'go', got %q", ce.Language)
	}
	if ce.Class != ClassType {
		t.Errorf("expected class 'type', got %q", ce.Class)
	}
	if ce.File != "internal/bedrock/client.go" {
		t.Errorf("expected file 'internal/bedrock/client.go', got %q", ce.File)
	}
	if ce.Line != 200 {
		t.Errorf("expected line 200, got %d", ce.Line)
	}
}

func TestClassifyGoUndefined(t *testing.T) {
	output := `./main.go:15:2: undefined: someFunction`

	ce := Classify("bash_exec", output, true)

	if ce.Language != "go" {
		t.Errorf("expected language 'go', got %q", ce.Language)
	}
	if ce.Class != ClassImport {
		t.Errorf("expected class 'import', got %q", ce.Class)
	}
}

func TestClassifyGoUnusedImport(t *testing.T) {
	output := `./main.go:4:2: "fmt" imported and not used`

	ce := Classify("bash_exec", output, true)

	if ce.Class != ClassCompile {
		t.Errorf("expected class 'compile', got %q", ce.Class)
	}
}

func TestClassifyPythonSyntaxError(t *testing.T) {
	output := `  File "/tmp/script.py", line 10
    def foo(
          ^
SyntaxError: unexpected EOF while parsing`

	ce := Classify("bash_exec", output, true)

	if ce.Language != "python" {
		t.Errorf("expected language 'python', got %q", ce.Language)
	}
	if ce.Class != ClassSyntax {
		t.Errorf("expected class 'syntax', got %q", ce.Class)
	}
	if ce.File != "/tmp/script.py" {
		t.Errorf("expected file '/tmp/script.py', got %q", ce.File)
	}
	if ce.Line != 10 {
		t.Errorf("expected line 10, got %d", ce.Line)
	}
}

func TestClassifyPythonImportError(t *testing.T) {
	output := `Traceback (most recent call last):
  File "/tmp/app.py", line 1, in <module>
    import nonexistent
ModuleNotFoundError: No module named 'nonexistent'`

	ce := Classify("bash_exec", output, true)

	if ce.Language != "python" {
		t.Errorf("expected language 'python', got %q", ce.Language)
	}
	if ce.Class != ClassImport {
		t.Errorf("expected class 'import', got %q", ce.Class)
	}
}

func TestClassifyFileNotFound(t *testing.T) {
	output := `reading file: open /nonexistent/path.go: no such file or directory`

	ce := Classify("read_file", output, true)

	if ce.Class != ClassNotFound {
		t.Errorf("expected class 'not_found', got %q", ce.Class)
	}
}

func TestClassifyPermissionDenied(t *testing.T) {
	output := `Error: permission denied: /etc/shadow`

	ce := Classify("read_file", output, true)

	if ce.Class != ClassPermission {
		t.Errorf("expected class 'permission', got %q", ce.Class)
	}
}

func TestClassifyNonError(t *testing.T) {
	ce := Classify("bash_exec", "success output", false)
	if ce.Class != ClassUnknown {
		t.Errorf("expected class 'unknown' for non-error, got %q", ce.Class)
	}
}

// --- Diagnosis Tests ---

func TestDiagnoseNoErrors(t *testing.T) {
	entries := []session.InkEntry{
		{ToolName: "read_file", IsError: false},
		{ToolName: "bash_exec", IsError: false},
	}

	diag := Diagnose(entries, 10)

	if diag.IsLooping {
		t.Error("should not detect looping with no errors")
	}
	if diag.ShouldEscalate {
		t.Error("should not escalate with no errors")
	}
}

func TestDiagnoseLooping(t *testing.T) {
	entries := []session.InkEntry{
		{ToolName: "bash_exec", IsError: true, Output: "./main.go:5: undefined: foo"},
		{ToolName: "bash_exec", IsError: true, Output: "./main.go:5: undefined: foo"},
		{ToolName: "bash_exec", IsError: true, Output: "./main.go:5: undefined: foo"},
	}

	diag := Diagnose(entries, 10)

	if !diag.IsLooping {
		t.Error("should detect looping with 3 failures on same tool")
	}
	if diag.LoopCount != 3 {
		t.Errorf("expected loop count 3, got %d", diag.LoopCount)
	}
	if diag.FailedTool != "bash_exec" {
		t.Errorf("expected failed tool 'bash_exec', got %q", diag.FailedTool)
	}
}

func TestDiagnoseEscalation(t *testing.T) {
	entries := make([]session.InkEntry, 5)
	for i := range entries {
		entries[i] = session.InkEntry{
			ToolName: "bash_exec",
			IsError:  true,
			Output:   "./main.go:10: type mismatch",
		}
	}

	diag := Diagnose(entries, 10)

	if !diag.ShouldEscalate {
		t.Error("should escalate after 5 consecutive failures")
	}
}

func TestDiagnoseRecentWindow(t *testing.T) {
	// Old errors outside the lookback window shouldn't affect diagnosis
	entries := []session.InkEntry{
		{ToolName: "bash_exec", IsError: true, Output: "old error 1"},
		{ToolName: "bash_exec", IsError: true, Output: "old error 2"},
		{ToolName: "bash_exec", IsError: true, Output: "old error 3"},
		{ToolName: "read_file", IsError: false, Output: "success"},
		{ToolName: "bash_exec", IsError: false, Output: "success"},
	}

	// Lookback of 2 should only see the last 2 entries (both success)
	diag := Diagnose(entries, 2)

	if diag.IsLooping {
		t.Error("should not detect looping in recent window (only successes)")
	}
}

// --- Reconciler Tests ---

func TestReconcilerNoErrors(t *testing.T) {
	r := NewReconciler()
	entries := []session.InkEntry{
		{ToolName: "read_file", IsError: false},
	}

	advice := r.Advise(entries)

	if advice.InjectPrompt != "" {
		t.Error("should not inject prompt when no errors")
	}
	if advice.ShouldAbort {
		t.Error("should not abort when no errors")
	}
}

func TestReconcilerSingleError(t *testing.T) {
	r := NewReconciler()
	entries := []session.InkEntry{
		{
			ToolName:  "bash_exec",
			IsError:   true,
			Output:    "./main.go:5:2: undefined: myFunc",
			Timestamp: time.Now(),
		},
	}

	advice := r.Advise(entries)

	if advice.InjectPrompt == "" {
		t.Error("should inject corrective prompt after error")
	}
	if advice.ShouldAbort {
		t.Error("should not abort after single error")
	}
}

func TestReconcilerLoopingInjection(t *testing.T) {
	r := NewReconciler()
	entries := make([]session.InkEntry, 3)
	for i := range entries {
		entries[i] = session.InkEntry{
			ToolName:  "bash_exec",
			IsError:   true,
			Output:    "./main.go:5: undefined: myFunc",
			Timestamp: time.Now(),
		}
	}

	advice := r.Advise(entries)

	if advice.InjectPrompt == "" {
		t.Error("should inject prompt for looping")
	}
	if !containsStr(advice.InjectPrompt, "LOOP DETECTED") {
		t.Error("looping prompt should contain 'LOOP DETECTED'")
	}
}

func TestReconcilerEscalation(t *testing.T) {
	r := NewReconciler()
	r.MaxConsecutiveFailures = 5

	entries := make([]session.InkEntry, 7)
	for i := range entries {
		entries[i] = session.InkEntry{
			ToolName:  "bash_exec",
			IsError:   true,
			Output:    "./main.go:5: type error",
			Timestamp: time.Now(),
		}
	}

	advice := r.Advise(entries)

	if !advice.ShouldAbort {
		t.Error("should recommend abort after exceeding max failures + 2")
	}
	if !containsStr(advice.InjectPrompt, "FAILURE LIMIT REACHED") {
		t.Error("escalation prompt should contain 'FAILURE LIMIT REACHED'")
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || findStr(s, substr))
}

func findStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
