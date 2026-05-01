package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestToolHandlers(t *testing.T) {
	db, _ := InitDB(":memory:")
	defer db.Close()
	adb := &mockAdbRunner{serial: "SERIAL123"}
	watcher := NewWatcher(db, adb)
	handlers := GetHandlers(db, watcher)

	t.Run("pawsift_set_target_package", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"package": "com.test.app"}
		
		res, err := handlers["pawsift_set_target_package"](context.Background(), req)
		if err != nil {
			t.Fatalf("Handler error: %v", err)
		}
		if res.IsError {
			t.Fatalf("Tool error: %v", res.Content[0].(mcp.TextContent).Text)
		}
		
		text := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, "com.test.app") {
			t.Errorf("Expected response to contain package name, got %q", text)
		}
	})

	t.Run("pawsift_get_status", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		res, err := handlers["pawsift_get_status"](context.Background(), req)
		if err != nil {
			t.Fatalf("Handler error: %v", err)
		}
		
		text := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, "PawSift Status Dashboard") {
			t.Errorf("Expected status dashboard header, got %q", text)
		}
	})

	t.Run("pawsift_set_retention_policy_invalid", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{
			"max_logs": 10, // below minimum 100
		}
		
		res, err := handlers["pawsift_set_retention_policy"](context.Background(), req)
		if err != nil {
			t.Fatalf("Handler error: %v", err)
		}
		if !res.IsError {
			t.Fatal("Expected error for invalid max_logs, but got success")
		}
	})
}
