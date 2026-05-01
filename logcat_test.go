package main

import (
	"context"
	"io"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type mockLogStream struct {
	io.ReadCloser
	waitErr error
	done    chan struct{}
}

func (s *mockLogStream) Wait() error {
	if s.done != nil {
		<-s.done
	}
	return s.waitErr
}

type mockAdbRunner struct {
	serial    string
	serialErr error
	stream    LogStream
	streamErr error
	clearErr  error
}

func (m *mockAdbRunner) GetSerial() (string, error) {
	return m.serial, m.serialErr
}

func (m *mockAdbRunner) StreamLogs(ctx context.Context, serial string) (LogStream, error) {
	return m.stream, m.streamErr
}

func (m *mockAdbRunner) ClearLogs(serial string) error {
	return m.clearErr
}

func TestProcessLine(t *testing.T) {
	db, _ := InitDB(":memory:")
	defer db.Close()
	adb := &mockAdbRunner{serial: "SERIAL123"}
	w := NewWatcher(db, adb)

	tests := []struct {
		name     string
		line     string
		expected bool
	}{
		{
			name:     "Valid log line",
			line:     "05-01 10:00:00.123  1234  5678 I TestTag : Hello World",
			expected: true,
		},
		{
			name:     "Invalid log line",
			line:     "Just some garbage text",
			expected: false,
		},
		{
			name:     "PID/TID overflow",
			line:     "05-01 10:00:00.124 9999999999999999999 9999999999999999999 D LargeInt : Boom",
			expected: true, // Should fall back to 0
		},
		{
			name:     "Duplicate log line (exact timestamp)",
			line:     "05-01 10:00:00.123  1234  5678 I TestTag : Hello World",
			expected: false, // Already processed
		},
		{
			name:     "Older log line",
			line:     "04-30 23:59:59.999  1234  5678 I OldTag : Past",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := w.processLine(tt.line)
			if result != tt.expected {
				t.Errorf("processLine() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestParseLogLineEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantOK  bool
		wantTS  string
		wantPID int
		wantTID int
		wantLvl string
		wantTag string
		wantMsg string
	}{
		{
			name:   "Empty string",
			line:   "",
			wantOK: false,
		},
		{
			name:   "Shorter than timestamp prefix",
			line:   "short",
			wantOK: false,
		},
		{
			name:   "Only timestamp",
			line:   "05-01 10:00:00.123",
			wantOK: false,
		},
		{
			name:   "Missing message body (no colon)",
			line:   "05-01 10:00:00.123  1234  5678 I NoColon",
			wantOK: false,
		},
		{
			name:   "Invalid timestamp body",
			line:   "not-a-real-timestamp 1234  5678 I Tag : Msg",
			wantOK: false,
		},
		{
			name:    "Multiple colons in message body",
			line:    "05-01 10:00:00.123  1234  5678 I Tag : level=ERROR: code=42: msg=crash",
			wantOK:  true,
			wantTS:  "05-01 10:00:00.123",
			wantPID: 1234,
			wantTID: 5678,
			wantLvl: "I",
			wantTag: "Tag",
			wantMsg: "level=ERROR: code=42: msg=crash",
		},
		{
			name:    "Missing spaces between PID and TID",
			line:    "05-01 10:00:00.123  1234 5678 W Tag : msg",
			wantOK:  true,
			wantTS:  "05-01 10:00:00.123",
			wantPID: 1234,
			wantTID: 5678,
			wantLvl: "W",
			wantTag: "Tag",
			wantMsg: "msg",
		},
		{
			name:   "Invalid level character",
			line:   "05-01 10:00:00.123  1234  5678 X Tag : msg",
			wantOK: false,
		},
		{
			name:   "Empty tag (tag is just colon)",
			line:   "05-01 10:00:00.123  1234  5678 I  : msg",
			wantOK: false,
		},
		{
			name:    "Standard valid line",
			line:    "05-01 10:00:00.123  1234  5678 E MyApp : Something broke",
			wantOK:  true,
			wantTS:  "05-01 10:00:00.123",
			wantPID: 1234,
			wantTID: 5678,
			wantLvl: "E",
			wantTag: "MyApp",
			wantMsg: "Something broke",
		},
		{
			name:   "Zero-length message (nothing after colon-space)",
			line:   "05-01 10:00:00.123  1234  5678 I Tag : ",
			wantOK: true,
		},
		{
			name:    "Tag with trailing space before colon",
			line:    "05-01 10:00:00.123  1234  5678 D TagName : Msg",
			wantOK:  true,
			wantTag: "TagName",
			wantMsg: "Msg",
		},
		{
			name:   "Garbage text",
			line:   "Just some garbage text that is longer than 18 chars",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, pid, tid, level, tag, msg, ok := parseLogLine(tt.line)
			if ok != tt.wantOK {
				t.Errorf("parseLogLine() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if tt.wantTS != "" && ts != tt.wantTS {
				t.Errorf("ts = %q, want %q", ts, tt.wantTS)
			}
			if tt.wantPID != 0 && pid != tt.wantPID {
				t.Errorf("pid = %d, want %d", pid, tt.wantPID)
			}
			if tt.wantTID != 0 && tid != tt.wantTID {
				t.Errorf("tid = %d, want %d", tid, tt.wantTID)
			}
			if tt.wantLvl != "" && level != tt.wantLvl {
				t.Errorf("level = %q, want %q", level, tt.wantLvl)
			}
			if tt.wantTag != "" && tag != tt.wantTag {
				t.Errorf("tag = %q, want %q", tag, tt.wantTag)
			}
			if tt.wantMsg != "" && msg != tt.wantMsg {
				t.Errorf("msg = %q, want %q", msg, tt.wantMsg)
			}
		})
	}
}

func TestHashDedupCorrectness(t *testing.T) {
	db, _ := InitDB(":memory:")
	defer db.Close()
	adb := &mockAdbRunner{serial: "SERIAL123"}
	w := NewWatcher(db, adb)

	// Same exact line, same timestamp — second should be rejected
	line := "05-01 10:00:00.000  100  200 I TagA : first message"
	if !w.processLine(line) {
		t.Error("expected first occurrence to be accepted")
	}
	if w.processLine(line) {
		t.Error("expected identical duplicate to be rejected")
	}

	// Same timestamp, message differs by one character at the end
	lineSimilar := "05-01 10:00:00.000  100  200 I TagA : first messagf"
	if !w.processLine(lineSimilar) {
		t.Error("expected similar line (1 char diff) to be accepted (different hash)")
	}

	// New timestamp, same first line — should be accepted
	lineNewTS := "05-01 10:00:01.000  100  200 I TagA : first message"
	if !w.processLine(lineNewTS) {
		t.Error("expected same line at new timestamp to be accepted")
	}

	// Different tag, same timestamp — should be accepted
	lineDiffTag := "05-01 10:00:01.000  100  200 I TagB : first message"
	if !w.processLine(lineDiffTag) {
		t.Error("expected different tag at same timestamp to be accepted")
	}
}

func TestFastAtoiEdgeCases(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"0", 0},
		{"1", 1},
		{"12345", 12345},
		{"9999999999999999999", 0}, // overflow -> 0
		{"99999999999999999999", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := fastAtoi(tt.input)
			if got != tt.want {
				t.Errorf("fastAtoi(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestWatcherRestartDetection(t *testing.T) {
	db, _ := InitDB(":memory:")
	defer db.Close()
	adb := &mockAdbRunner{serial: "SERIAL123"}
	w := NewWatcher(db, adb)
	w.SetTargetPackage("com.example.app")

	line := "05-01 10:00:00.123  1234  5678 I ActivityManager : Start proc com.example.app for activity ..."
	w.processLine(line)

	if w.GetCurrentSession() != 1 {
		t.Errorf("Expected session ID 1 after restart, got %d", w.GetCurrentSession())
	}
}

func TestStreamWithRetryContextCancel(t *testing.T) {
	db, _ := InitDB(":memory:")
	defer db.Close()

	// Mock stream that closes when context is done
	pr, pw := io.Pipe()
	doneChan := make(chan struct{})
	stream := &mockLogStream{ReadCloser: pr, done: doneChan}
	adb := &mockAdbRunner{serial: "SERIAL", stream: stream}

	w := NewWatcher(db, adb)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error)
	go func() {
		done <- w.streamWithRetry(ctx)
	}()

	// Wait a bit, then cancel
	time.Sleep(100 * time.Millisecond)
	cancel()
	pw.Close()
	close(doneChan)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("streamWithRetry returned error on cancel: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("streamWithRetry did not return after context cancellation")
	}
}
