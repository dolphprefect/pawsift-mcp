package main

import (
	"strings"
	"testing"
)

func TestIndentTrace(t *testing.T) {
	tests := []struct {
		name     string
		trace    string
		expected string
	}{
		{
			name:     "Empty trace",
			trace:    "",
			expected: "",
		},
		{
			name:     "Single line trace",
			trace:    "at com.example.App.main(App.java:10)",
			expected: "  at com.example.App.main(App.java:10)\n",
		},
		{
			name:     "Multi-line trace",
			trace:    "at com.example.App.main(App.java:10)\nat java.lang.Thread.run(Thread.java:748)",
			expected: "  at com.example.App.main(App.java:10)\n  at java.lang.Thread.run(Thread.java:748)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := indentTrace(tt.trace)
			if result != tt.expected {
				t.Errorf("indentTrace() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestRenderLogEntries(t *testing.T) {
	entries := []LogEntry{
		{
			ID:        1,
			Timestamp: "05-01 10:00:00.123",
			Level:     "E",
			Tag:       "AndroidRuntime",
			PID:       1234,
			Message:   "FATAL EXCEPTION\njava.lang.NullPointerException",
		},
		{
			ID:        2,
			Timestamp: "05-01 10:00:00.124",
			Level:     "E",
			Tag:       "AndroidRuntime",
			PID:       1234,
			Message:   "FATAL EXCEPTION\njava.lang.NullPointerException",
		},
		{
			ID:        3,
			Timestamp: "05-01 10:00:01.000",
			Level:     "D",
			Tag:       "MyApp",
			PID:       1234,
			Message:   "Debug message",
		},
	}

	var sb strings.Builder
	renderLogEntries(&sb, entries, 2)
	result := sb.String()

	// Check for expected components
	expectedComponents := []string{
		"### AndroidRuntime (1234)",
		"#### FATAL EXCEPTION",
		"- [1] **E** 10:00:00.123",
		"> - [2] **E** 10:00:00.124",
		"### MyApp (1234)",
		"#### Debug message",
		"- [3] **D** 10:00:01.000",
	}

	for _, comp := range expectedComponents {
		if !strings.Contains(result, comp) {
			t.Errorf("renderLogEntries() output missing %q", comp)
		}
	}
}

func TestRenderFoldedLogEntries(t *testing.T) {
	entries := []FoldedLog{
		{
			StartID:   1,
			EndID:     5,
			StartTime: "05-01 10:00:00.000",
			EndTime:   "05-01 10:00:05.000",
			Level:     "I",
			Tag:       "Network",
			PID:       1234,
			Message:   "Request sent",
			Count:     5,
		},
		{
			StartID:   6,
			EndID:     6,
			StartTime: "05-01 10:00:06.000",
			EndTime:   "05-01 10:00:06.000",
			Level:     "W",
			Tag:       "Network",
			PID:       1234,
			Message:   "Retry",
			Count:     1,
		},
	}

	var sb strings.Builder
	renderFoldedLogEntries(&sb, entries)
	result := sb.String()

	expectedComponents := []string{
		"### Network (1234)",
		"- [1-5] **I** 10:00:00.000 - 10:00:05.000 Request sent (5x)",
		"- [6] **W** 10:00:06.000 Retry",
	}

	for _, comp := range expectedComponents {
		if !strings.Contains(result, comp) {
			t.Errorf("renderFoldedLogEntries() output missing %q", comp)
		}
	}
}
