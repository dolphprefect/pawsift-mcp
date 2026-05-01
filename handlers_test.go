package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestHandlersComprehensive(t *testing.T) {
	db, _ := InitDB(":memory:")
	defer db.Close()
	adb := &mockAdbRunner{serial: "SERIAL123"}
	watcher := NewWatcher(db, adb)
	handlers := GetHandlers(db, watcher)

	// Helper to call a tool
	call := func(name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
		req := mcp.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = args
		return handlers[name](context.Background(), req)
	}

	t.Run("pawsift_set_target_package", func(t *testing.T) {
		res, _ := call("pawsift_set_target_package", map[string]interface{}{"package": "com.example.app"})
		if res.IsError {
			t.Fatal("Tool returned error")
		}
		if watcher.targetPackage != "com.example.app" {
			t.Errorf("Expected package com.example.app, got %s", watcher.targetPackage)
		}
	})

	t.Run("pawsift_get_error_summary_empty", func(t *testing.T) {
		res, _ := call("pawsift_get_error_summary", nil)
		text := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, "No errors found") {
			t.Errorf("Expected 'No errors found', got %q", text)
		}
	})

	t.Run("pawsift_query_logs_empty", func(t *testing.T) {
		res, _ := call("pawsift_query_logs", nil)
		text := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, "No logs found") {
			t.Errorf("Expected 'No logs found', got %q", text)
		}
	})

	t.Run("pawsift_set_retention_policy", func(t *testing.T) {
		res, _ := call("pawsift_set_retention_policy", map[string]interface{}{
			"max_logs":         500,
			"max_sessions":     5,
			"cleanup_interval": 60,
		})
		if res.IsError {
			t.Fatal("Tool returned error")
		}
		policy := watcher.GetRetentionPolicy()
		if policy.MaxLogs != 500 || policy.MaxSessions != 5 || policy.CleanupInterval != 60 {
			t.Errorf("Policy not updated correctly: %+v", policy)
		}
	})

	t.Run("pawsift_get_status", func(t *testing.T) {
		res, _ := call("pawsift_get_status", nil)
		text := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, "com.example.app") {
			t.Errorf("Status should show target package, got %q", text)
		}
		if !strings.Contains(text, "SERIAL123") {
			t.Errorf("Status should show device serial, got %q", text)
		}
	})

	// Add some data to test queries
	db.InsertLog(LogEntry{SessionID: 0, Level: "E", Tag: "Test", Message: "Error message"})
	db.InsertLog(LogEntry{SessionID: 0, Level: "I", Tag: "Test", Message: "Info message"})

	t.Run("pawsift_get_error_summary_with_data", func(t *testing.T) {
		res, _ := call("pawsift_get_error_summary", nil)
		text := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, "Error message") {
			t.Errorf("Expected error summary to contain message, got %q", text)
		}
	})

	t.Run("pawsift_query_logs_with_data", func(t *testing.T) {
		res, _ := call("pawsift_query_logs", map[string]interface{}{"level": "E"})
		text := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, "Error message") {
			t.Errorf("Expected query results to contain error message, got %q", text)
		}
		if strings.Contains(text, "Info message") {
			t.Error("Query results should not contain Info message when filtered by Level E")
		}
	})

	t.Run("pawsift_search_logs", func(t *testing.T) {
		res, _ := call("pawsift_search_logs", map[string]interface{}{"query": "Info"})
		text := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, "Info message") {
			t.Errorf("Expected search results to contain 'Info message', got %q", text)
		}
	})

	t.Run("pawsift_get_log_context", func(t *testing.T) {
		res, _ := call("pawsift_get_log_context", map[string]interface{}{"log_id": 1, "lines": 5})
		text := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, "Error message") {
			t.Errorf("Expected context to contain log ID 1, got %q", text)
		}
	})

	t.Run("pawsift_clear_logs", func(t *testing.T) {
		// Mock clear logs on device is already covered by mockAdbRunner
		res, _ := call("pawsift_clear_logs", nil)
		if res.IsError {
			t.Fatal("Tool returned error")
		}
		count, _ := db.GetLogCount(0)
		if count != 0 {
			t.Errorf("Logs not cleared from DB, count is %d", count)
		}
	})
}
