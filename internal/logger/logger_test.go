package logger

import (
	"fmt"
	"strings"
	"testing"
)

func TestMemoryLogger(t *testing.T) {
	mw := InitLogger(false)
	if mw == nil {
		t.Fatal("Expected non-nil MemoryLogWriter")
	}

	// Write 5 sample log messages
	for i := 1; i <= 5; i++ {
		_, _ = mw.Write([]byte(fmt.Sprintf("Log message test %d\n", i)))
	}

	logs := GetLogs()
	if len(logs) < 5 {
		t.Errorf("Expected at least 5 log entries, got %d", len(logs))
	}

	found := false
	for _, l := range logs {
		if strings.Contains(l, "Log message test 3") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Log message 'Log message test 3' not found in logs: %v", logs)
	}
}

func TestLoggerCapacityLimit(t *testing.T) {
	mw := InitLogger(false)
	// Fill beyond 300 capacity
	for i := 0; i < 350; i++ {
		_, _ = mw.Write([]byte(fmt.Sprintf("Capacity test line %d\n", i)))
	}

	logs := GetLogs()
	if len(logs) > 300 {
		t.Errorf("Expected logs capped at 300, got %d", len(logs))
	}
}
