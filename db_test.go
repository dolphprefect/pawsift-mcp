package main

import (
	"testing"
)

func TestDBBasics(t *testing.T) {
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	entry := LogEntry{
		SessionID: 1,
		Timestamp: "05-01 10:00:00.000",
		Level:     "I",
		Tag:       "Test",
		PID:       100,
		TID:       101,
		Message:   "First line",
	}

	id, err := db.InsertLog(entry)
	if err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}
	if id != 1 {
		t.Errorf("Expected ID 1, got %d", id)
	}

	err = db.AppendToLog(id, "Second line")
	if err != nil {
		t.Fatalf("AppendToLog failed: %v", err)
	}

	count, err := db.GetLogCount(1)
	if err != nil {
		t.Fatalf("GetLogCount failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected count 1, got %d", count)
	}

	logs, err := db.QueryLogs(1, "", "", 10)
	if err != nil {
		t.Fatalf("QueryLogs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("Expected 1 log, got %d", len(logs))
	}
	expectedMsg := "First line\nSecond line"
	if logs[0].Message != expectedMsg {
		t.Errorf("Expected message %q, got %q", expectedMsg, logs[0].Message)
	}
}

func TestDBCleanup(t *testing.T) {
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// Insert logs for 5 sessions
	for s := 1; s <= 5; s++ {
		_, _ = db.InsertLog(LogEntry{SessionID: s, Message: "Log"})
	}

	cfg := RetentionConfig{
		MaxLogs:     100,
		MaxSessions: 2,
	}

	err = db.Cleanup(cfg)
	if err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	// Should only have sessions 4 and 5
	var sessionCount int
	err = db.conn.QueryRow("SELECT COUNT(DISTINCT session_id) FROM logs").Scan(&sessionCount)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if sessionCount != 2 {
		t.Errorf("Expected 2 sessions, got %d", sessionCount)
	}

	// Test MaxLogs
	for i := 0; i < 10; i++ {
		_, _ = db.InsertLog(LogEntry{SessionID: 6, Message: "New Log"})
	}
	cfg.MaxLogs = 5
	cfg.MaxSessions = 10
	_ = db.Cleanup(cfg)

	var logCount int
	err = db.conn.QueryRow("SELECT COUNT(*) FROM logs").Scan(&logCount)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if logCount != 5 {
		t.Errorf("Expected 5 logs, got %d", logCount)
	}
}

func TestFoldLogEntries(t *testing.T) {
	entries := []LogEntry{
		{ID: 3, Timestamp: "TS3", Level: "I", Tag: "T", PID: 1, Message: "M"},
		{ID: 2, Timestamp: "TS2", Level: "I", Tag: "T", PID: 1, Message: "M"},
		{ID: 1, Timestamp: "TS1", Level: "I", Tag: "T", PID: 1, Message: "M"},
		{ID: 0, Timestamp: "TS0", Level: "D", Tag: "T", PID: 1, Message: "D"},
	}

	folded := FoldLogEntries(entries, 10)
	if len(folded) != 2 {
		t.Errorf("Expected 2 folded entries, got %d", len(folded))
	}

	if folded[0].Count != 3 {
		t.Errorf("Expected first folded entry count 3, got %d", folded[0].Count)
	}
	if folded[0].StartID != 1 {
		t.Errorf("Expected first folded entry StartID 1, got %d", folded[0].StartID)
	}
}
