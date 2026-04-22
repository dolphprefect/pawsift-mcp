package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var logPattern = regexp.MustCompile(`^(\d{2}-\d{2}\s\d{2}:\d{2}:\d{2}\.\d{3})\s+(\d+)\s+(\d+)\s+([VDIWEF])\s+(.*?)\s*:\s+(.*)$`)

type Watcher struct {
	targetPackage  string
	currentSession int
	db             *DB
	lastTimestamp  string
	lastSeenKeys   map[string]bool
	lastEntryID    int64
	lastEntryKey   string
	deviceSerial   string
	lastPoll       time.Time
	isStarted      bool
	retention      RetentionConfig
	cleanupCounter int
	mu             sync.Mutex
}

func NewWatcher(db *DB) *Watcher {
	w := &Watcher{
		db:        db,
		lastSeenKeys: map[string]bool{},
		retention: DefaultRetentionConfig,
	}
	out, _ := exec.Command("adb", "get-serialno").Output()
	w.deviceSerial = strings.TrimSpace(string(out))
	if w.deviceSerial == "unknown" || w.deviceSerial == "" {
		w.deviceSerial = "No device connected"
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
	fmt.Fprintln(os.Stderr, "LOG: Starting Fail-Fast Polling Watcher...")
	w.mu.Lock()
	w.isStarted = true
	w.mu.Unlock()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.pollWithTimeout()
		}
	}
}

func (w *Watcher) pollWithTimeout() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "adb", "logcat", "-v", "threadtime", "-d")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Fprintln(os.Stderr, "LOG: adb poll TIMEOUT (2s)")
		} else {
			fmt.Fprintf(os.Stderr, "LOG: adb poll ERROR: %v\n", err)
		}
		return
	}

	w.mu.Lock()
	w.lastPoll = time.Now()
	w.cleanupCounter++
	shouldCleanup := w.cleanupCounter >= w.retention.CleanupInterval
	if shouldCleanup {
		w.cleanupCounter = 0
	}
	w.mu.Unlock()

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	newLogs := 0
	for scanner.Scan() {
		line := scanner.Text()
		if w.processLine(line) {
			newLogs++
		}
	}

	if shouldCleanup {
		if err := w.db.Cleanup(w.retention); err != nil {
			fmt.Fprintf(os.Stderr, "LOG: Cleanup error: %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "LOG: Cleanup completed successfully")
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
		w.lastSeenKeys = map[string]bool{}
	}
	w.lastSeenKeys[line] = true
	pkg := w.targetPackage
	sessionID := w.currentSession
	w.mu.Unlock()

	level := matches[4]
	tag := strings.TrimSpace(matches[5])
	message := matches[6]
	pid, _ := strconv.Atoi(matches[2])
	tid, _ := strconv.Atoi(matches[3])

	isRestart := tag == "ActivityManager" && pkg != "" && strings.Contains(message, pkg) &&
		(strings.Contains(message, "Start proc") || strings.Contains(message, "Killing") || strings.Contains(message, "Force stopping"))

	if isRestart {
		w.mu.Lock()
		w.currentSession++
		sessionID = w.currentSession
		w.mu.Unlock()
		fmt.Fprintf(os.Stderr, "LOG: Session Restarted (%d) for %s\n", sessionID, pkg)
		w.lastEntryID = 0
		w.lastEntryKey = ""
	}

	// Group consecutive lines with identical (session, level, tag, pid, tid, timestamp)
	// into a single DB entry — captures full exception stack traces as one record.
	entryKey := fmt.Sprintf("%d|%s|%s|%d|%d|%s", sessionID, level, tag, pid, tid, ts)
	if w.lastEntryID != 0 && entryKey == w.lastEntryKey {
		_ = w.db.AppendToLog(w.lastEntryID, message)
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
	w.lastEntryID = id
	w.lastEntryKey = entryKey
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
	w.mu.Unlock()
	return exec.Command("adb", "logcat", "-c").Run()
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
	
	isActive := w.isStarted && time.Since(w.lastPoll) < 5*time.Second
	
	return WatcherInfo{
		TargetPackage:  w.targetPackage,
		CurrentSession: w.currentSession,
		DeviceSerial:   w.deviceSerial,
		IsActive:       isActive,
		LastPoll:       w.lastPoll,
	}
}
