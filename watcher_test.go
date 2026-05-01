package main

import (
	"testing"
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
