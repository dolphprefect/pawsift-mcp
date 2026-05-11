package main

import (
	"bufio"
	"context"
	"fmt"
	"hash/maphash"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var lineHashSeed = maphash.MakeSeed()

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
	lastSeenKeys   map[uint64]bool
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
		lastSeenKeys:  map[uint64]bool{},
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

	w.mu.Lock()
	serial := w.deviceSerial
	w.mu.Unlock()

	if serial == "" || serial == "No device connected" {
		newSerial, err := w.adb.GetSerial()
		if err != nil || newSerial == "unknown" || newSerial == "" {
			return fmt.Errorf("no device connected")
		}
		w.mu.Lock()
		w.deviceSerial = newSerial
		serial = newSerial
		w.mu.Unlock()
		fmt.Fprintf(os.Stderr, "LOG: Device detected: %s\n", serial)
	}

	stream, err := w.adb.StreamLogs(localCtx, serial)
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

	lines := make(chan string, 10000)
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

func parseLogLine(line string) (ts string, pid, tid int, level, tag, msg string, ok bool) {
	n := len(line)
	if n < 18 {
		return
	}

	// Timestamp: "MM-DD HH:MM:SS.mmm" at position 0-17 (fixed-width in threadtime format)
	ts = line[:18]
	idx := 18

	// Skip spaces to PID
	for idx < n && line[idx] == ' ' {
		idx++
	}
	pidStart := idx
	for idx < n && line[idx] >= '0' && line[idx] <= '9' {
		idx++
	}
	if pidStart == idx {
		return
	}
	pid = fastAtoi(line[pidStart:idx])

	// Skip spaces to TID
	for idx < n && line[idx] == ' ' {
		idx++
	}
	tidStart := idx
	for idx < n && line[idx] >= '0' && line[idx] <= '9' {
		idx++
	}
	if tidStart == idx {
		return
	}
	tid = fastAtoi(line[tidStart:idx])

	// Skip spaces to LEVEL
	for idx < n && line[idx] == ' ' {
		idx++
	}
	if idx >= n {
		return
	}
	lvl := line[idx]
	if lvl != 'V' && lvl != 'D' && lvl != 'I' && lvl != 'W' && lvl != 'E' && lvl != 'F' {
		return
	}
	level = string(lvl)
	idx++

	// Skip spaces to TAG
	for idx < n && line[idx] == ' ' {
		idx++
	}
	if idx >= n {
		return
	}
	tagStart := idx

	// Find ": " to split TAG and MESSAGE
	colonIdx := strings.Index(line[tagStart:], ": ")
	if colonIdx < 0 {
		return
	}
	tag = strings.TrimSpace(line[tagStart : tagStart+colonIdx])
	if tag == "" {
		return
	}
	msg = line[tagStart+colonIdx+2:]

	ok = true
	return
}

// fastAtoi converts a digit-only string to int. Returns 0 on overflow.
func fastAtoi(s string) int {
	const maxInt = int(^uint(0) >> 1)
	n := 0
	for i := 0; i < len(s); i++ {
		d := int(s[i] - '0')
		if n > maxInt/10 {
			return 0
		}
		n = n*10 + d
	}
	return n
}

func hashLine(line string) uint64 {
	return maphash.String(lineHashSeed, line)
}

func (w *Watcher) processLine(line string) bool {
	ts, pid, tid, level, tag, msg, ok := parseLogLine(line)
	if !ok {
		return false
	}

	w.mu.Lock()

	if ts < w.lastTimestamp {
		w.mu.Unlock()
		return false
	}
	if ts == w.lastTimestamp {
		h := hashLine(line)
		if w.lastSeenKeys[h] {
			w.mu.Unlock()
			return false
		}
		w.lastSeenKeys[h] = true
	} else {
		w.lastTimestamp = ts
		clear(w.lastSeenKeys)
		h := hashLine(line)
		w.lastSeenKeys[h] = true
	}

	pkg := w.targetPackage
	sessionID := w.currentSession

	isRestart := (tag == "ActivityManager" || tag == "ActivityTaskManager") && pkg != "" && strings.Contains(msg, pkg) &&
		(strings.Contains(msg, "Start proc") || strings.Contains(msg, "Killing") || strings.Contains(msg, "Force stopping"))

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
		_ = w.db.AppendToLog(lastID, msg)
		return true
	}

	entry := LogEntry{
		SessionID: sessionID,
		Timestamp: ts,
		Level:     level,
		Tag:       tag,
		PID:       pid,
		TID:       tid,
		Message:   msg,
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

	return w.adb.ClearLogs(serial)
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
