package main

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestWatcherCleaningFlag(t *testing.T) {
	db, _ := InitDB(":memory:")
	defer db.Close()
	adb := &mockAdbRunner{serial: "SERIAL123"}
	w := NewWatcher(db, adb)

	if w.isCleaning.Load() {
		t.Fatal("Expected isCleaning to be false initially")
	}

	if !w.isCleaning.CompareAndSwap(false, true) {
		t.Fatal("Failed to set isCleaning to true")
	}

	if w.isCleaning.CompareAndSwap(false, true) {
		t.Fatal("Expected second CompareAndSwap to fail")
	}

	w.isCleaning.Store(false)
	if !w.isCleaning.CompareAndSwap(false, true) {
		t.Fatal("Failed to reset isCleaning")
	}
}

func TestRetentionPolicyUpdate(t *testing.T) {
	db, _ := InitDB(":memory:")
	defer db.Close()
	adb := &mockAdbRunner{serial: "SERIAL123"}
	w := NewWatcher(db, adb)

	w.SetRetentionPolicy(5000, 5, 60)
	cfg := w.GetRetentionPolicy()

	if cfg.MaxLogs != 5000 || cfg.MaxSessions != 5 || cfg.CleanupInterval != 60 {
		t.Errorf("Retention policy not updated correctly: %+v", cfg)
	}
}

func TestConcurrentHighLoad(t *testing.T) {
	// Use file-backed DB so WAL mode is active, enabling concurrent reads
	dbPath := t.TempDir() + "/stress.db"
	db, _ := InitDB(dbPath)
	defer db.Close()
	adb := &mockAdbRunner{serial: "SERIAL123"}
	w := NewWatcher(db, adb)

	n := 10000

	// Pump 10k lines through processLine in a single goroutine so timestamps
	// remain ordered. Concurrent readers simulate MCP handler queries during
	// streaming.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			line := fmt.Sprintf("05-01 10:%02d:%02d.%03d  %04d  %04d I Tag%d : Message %d",
				i/60000, (i/1000)%60, i%1000, i, i, i%100, i)
			w.processLine(line)
		}
	}()

	// Concurrent readers simulating MCP tool queries during streaming.
	var readWg sync.WaitGroup
	for r := 0; r < 4; r++ {
		readWg.Add(1)
		go func() {
			defer readWg.Done()
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					_, _ = db.GetLogCount(0)
					_, _ = db.QueryLogs(0, "I", "Tag0", 10)
				}
			}
		}()
	}

	<-done
	readWg.Wait()

	count, err := db.GetLogCount(0)
	if err != nil {
		t.Fatalf("GetLogCount failed: %v", err)
	}
	if count != n {
		t.Errorf("Expected %d logs in DB, got %d", n, count)
	}
}
