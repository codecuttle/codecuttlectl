package pluginhost

import (
	"strings"
	"testing"
)

func TestCapToolOutputSmallPassthrough(t *testing.T) {
	s := "hello\nworld"
	if got := capToolOutput(s); got != s {
		t.Fatalf("small output modified: %q", got)
	}
}

func TestCapToolOutputTruncatesLarge(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 500000; i++ {
		b.WriteString("log line with some celery worker noise\n")
	}
	s := b.String() + "ModuleNotFoundError: No module named 'boto3'\n"
	got := capToolOutput(s)
	if len(got) > MaxToolOutputBytes+512 {
		t.Fatalf("capped output too large: %d bytes", len(got))
	}
	if !strings.Contains(got, "tool output truncated") {
		t.Fatal("missing truncation marker")
	}
	if !strings.Contains(got, "ModuleNotFoundError") {
		t.Fatal("tail (error) not preserved")
	}
	if !strings.HasPrefix(got, "log line") {
		t.Fatal("head not preserved")
	}
}
