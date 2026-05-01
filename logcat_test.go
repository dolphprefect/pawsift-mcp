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
