package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var logPattern = regexp.MustCompile(`^(\d{2}-\d{2}\s\d{2}:\d{2}:\d{2}\.\d{3})\s+(\d+)\s+(\d+)\s+([VDIWEF])\s+(.*?)\s*:\s+(.*)$`)

// LogStream abstracts a logcat output stream
type LogStream interface {
	io.ReadCloser
	Wait() error
}

// AdbRunner abstracts interaction with the adb executable
type AdbRunner interface {
	GetSerial() (string, error)
	StreamLogs(ctx context.Context, serial string) (LogStream, error)
	ClearLogs(serial string) error
}

// RealAdbRunner is the production implementation using exec.Command
type RealAdbRunner struct{}

func (r *RealAdbRunner) GetSerial() (string, error) {
	out, err := exec.Command("adb", "get-serialno").Output()
	return strings.TrimSpace(string(out)), err
}

type realLogStream struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (s *realLogStream) Wait() error {
	return s.cmd.Wait()
}

func (r *RealAdbRunner) StreamLogs(ctx context.Context, serial string) (LogStream, error) {
	args := []string{"logcat", "-v", "threadtime"}
	if serial != "" && serial != "No device connected" {
		args = append([]string{"-s", serial}, args...)
	}
	cmd := exec.CommandContext(ctx, "adb", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &realLogStream{ReadCloser: stdout, cmd: cmd}, nil
}

func (r *RealAdbRunner) ClearLogs(serial string) error {
	args := []string{"logcat", "-c"}
	if serial != "" && serial != "No device connected" {
		args = append([]string{"-s", serial}, args...)
	}
	return exec.Command("adb", args...).Run()
}

type Watcher struct {
	targetPackage  string
	currentSession int
	db             *DB
	adb            AdbRunner
	lastTimestamp  string
	lastSeenKeys   map[string]bool
	lastEntryID    int64
	lastEntryKey   string
	deviceSerial   string
	lastPoll       time.Time
	isStarted      bool
	retention      RetentionConfig
	cmd            LogStream
	isAdbRunning   bool
	isCleaning     atomic.Bool
	mu             sync.Mutex
}

func NewWatcher(db *DB, adb AdbRunner) *Watcher {
	w := &Watcher{
		db:            db,
		adb:           adb,
		lastSeenKeys:  map[string]bool{},
		retention:     DefaultRetentionConfig,
		lastTimestamp: db.GetLastTimestamp(),
	}
	serial, err := adb.GetSerial()
	if err != nil || serial == "unknown" || serial == "" {
		w.deviceSerial = "No device connected"
	} else {
		w.deviceSerial = serial
	}
	return w
}

func (w *Watcher) SetTargetPackage(pkg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.targetPackage = pkg
	fmt.Fprintf(os.Stderr, "LOG: Target package set to: %s\n", pkg)
}

func (w *Watcher) Start(ctx context.Context) error {
	fmt.Fprintln(os.Stderr, "LOG: Starting Streaming Watcher...")
	w.mu.Lock()
	w.isStarted = true
	w.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if err := w.streamWithRetry(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "LOG: Watcher stream error: %v. Restarting in 2s...\n", err)
			}
			// Always wait 2s before restarting to prevent CPU-maxing tight loops on EOF
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(2 * time.Second):
				continue
			}
		}
	}
}

func (w *Watcher) streamWithRetry(ctx context.Context) error {
	localCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := w.adb.StreamLogs(localCtx, w.deviceSerial)
	if err != nil {
		return err
	}

	w.mu.Lock()
	w.cmd = stream
	w.isAdbRunning = true
	w.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		err := stream.Wait()
		w.mu.Lock()
		w.isAdbRunning = false
		w.mu.Unlock()
		done <- err
	}()

	lines := make(chan string, 100)
	scanner := bufio.NewScanner(stream)
	go func() {
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-localCtx.Done():
				return
			}
		}
		close(lines)
	}()

	// Use a fast static ticker and check the retention interval on every tick.
	// This ensures we pick up config changes quickly.
	cleanupTicker := time.NewTicker(5 * time.Second)
	defer cleanupTicker.Stop()
	lastCleanup := time.Now()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-done:
			// If context is canceled, we don't care about the exit error
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("adb process exited: %w", err)
			}
		case <-cleanupTicker.C:
			w.mu.Lock()
			retention := w.retention
			w.mu.Unlock()

			interval := time.Duration(retention.CleanupInterval) * time.Second
			if time.Since(lastCleanup) >= interval {
				// Prevent overlapping cleanups
				if w.isCleaning.CompareAndSwap(false, true) {
					lastCleanup = time.Now()
					go func(r RetentionConfig) {
						defer w.isCleaning.Store(false)
						if err := w.db.Cleanup(r); err != nil {
							fmt.Fprintf(os.Stderr, "LOG: Cleanup error: %v\n", err)
						} else {
							fmt.Fprintln(os.Stderr, "LOG: Cleanup completed successfully")
						}
					}(retention)
				}
			}
		case line, ok := <-lines:
			if !ok {
				if err := scanner.Err(); err != nil {
					// scanner error: read /dev/stdin: file already closed is expected on close
					if !strings.Contains(err.Error(), "file already closed") {
						return fmt.Errorf("scanner error: %w", err)
					}
				}
				return nil
			}
			w.processLine(line)

			w.mu.Lock()
			w.lastPoll = time.Now()
			w.mu.Unlock()
		}
	}
}

func (w *Watcher) processLine(line string) bool {
	matches := logPattern.FindStringSubmatch(line)
	if len(matches) < 7 {
		return false
	}

	ts := matches[1]
	w.mu.Lock()

	if ts < w.lastTimestamp {
		w.mu.Unlock()
		return false
	}
	if ts == w.lastTimestamp && w.lastSeenKeys[line] {
		w.mu.Unlock()
		return false
	}
	if ts > w.lastTimestamp {
		w.lastTimestamp = ts
		clear(w.lastSeenKeys)
	}
	w.lastSeenKeys[line] = true
	pkg := w.targetPackage
	sessionID := w.currentSession

	level := matches[4]
	tag := strings.TrimSpace(matches[5])
	message := matches[6]
	pid, err := strconv.Atoi(matches[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: PID overflow or parse error: %v. Falling back to 0.\n", err)
		pid = 0
	}
	tid, err := strconv.Atoi(matches[3])
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: TID overflow or parse error: %v. Falling back to 0.\n", err)
		tid = 0
	}

	isRestart := (tag == "ActivityManager" || tag == "ActivityTaskManager") && pkg != "" && strings.Contains(message, pkg) &&
		(strings.Contains(message, "Start proc") || strings.Contains(message, "Killing") || strings.Contains(message, "Force stopping"))

	if isRestart {
		w.currentSession++
		sessionID = w.currentSession
		fmt.Fprintf(os.Stderr, "LOG: Session Restarted (%d) for %s\n", sessionID, pkg)
		w.lastEntryID = 0
		w.lastEntryKey = ""
	}

	lastID := w.lastEntryID
	lastKey := w.lastEntryKey
	w.mu.Unlock()

	// Group consecutive lines with identical (session, level, tag, pid, tid, timestamp)
	// into a single DB entry — captures full exception stack traces as one record.
	entryKey := fmt.Sprintf("%d|%s|%s|%d|%d|%s", sessionID, level, tag, pid, tid, ts)
	if lastID != 0 && entryKey == lastKey {
		_ = w.db.AppendToLog(lastID, message)
		return true
	}

	entry := LogEntry{
		SessionID: sessionID,
		Timestamp: ts,
		Level:     level,
		Tag:       tag,
		PID:       pid,
		TID:       tid,
		Message:   message,
	}

	id, _ := w.db.InsertLog(entry)

	w.mu.Lock()
	w.lastEntryID = id
	w.lastEntryKey = entryKey
	w.mu.Unlock()

	return true
}

func (w *Watcher) GetCurrentSession() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.currentSession
}

func (w *Watcher) SetRetentionPolicy(maxLogs, maxSessions, cleanupInterval int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.retention.MaxLogs = maxLogs
	w.retention.MaxSessions = maxSessions
	w.retention.CleanupInterval = cleanupInterval
	fmt.Fprintf(os.Stderr, "LOG: Retention policy updated - MaxLogs=%d, MaxSessions=%d, CleanupInterval=%ds\n",
		maxLogs, maxSessions, cleanupInterval)
}

func (w *Watcher) GetRetentionPolicy() RetentionConfig {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.retention
}

func (w *Watcher) ClearDeviceLogs() error {
	w.mu.Lock()
	w.lastTimestamp = ""
	clear(w.lastSeenKeys)
	serial := w.deviceSerial
	w.mu.Unlock()

	args := []string{"logcat", "-c"}
	if serial != "" && serial != "No device connected" {
		args = append([]string{"-s", serial}, args...)
	}
	return exec.Command("adb", args...).Run()
}

type WatcherInfo struct {
	TargetPackage  string
	CurrentSession int
	DeviceSerial   string
	IsActive       bool
	LastPoll       time.Time
}

func (w *Watcher) GetInfo() WatcherInfo {
	w.mu.Lock()
	defer w.mu.Unlock()

	isActive := w.isStarted && w.isAdbRunning

	return WatcherInfo{
		TargetPackage:  w.targetPackage,
		CurrentSession: w.currentSession,
		DeviceSerial:   w.deviceSerial,
		IsActive:       isActive,
		LastPoll:       w.lastPoll,
	}
}
