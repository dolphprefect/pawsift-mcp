package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

type ToolHandler func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)

func GetHandlers(db *DB, watcher *Watcher) map[string]ToolHandler {
	return map[string]ToolHandler{
		"pawsift_set_target_package": func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pkg := request.GetString("package", "")
			if pkg == "" {
				return mcp.NewToolResultError("Invalid package name"), nil
			}
			watcher.SetTargetPackage(pkg)
			return mcp.NewToolResultText(fmt.Sprintf("Now tracking sessions for: %s", pkg)), nil
		},
		"pawsift_get_error_summary": func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			sessionID := watcher.GetCurrentSession()
			summary, err := db.GetErrorSummary(sessionID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
			}
			return mcp.NewToolResultText(summary), nil
		},
		"pawsift_get_tag_summary": func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			sessionID := watcher.GetCurrentSession()
			summary, err := db.GetTagSummary(sessionID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
			}
			return mcp.NewToolResultText(summary), nil
		},
		"pawsift_query_logs": func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			sessionID := watcher.GetCurrentSession()
			level := request.GetString("level", "")
			tag := request.GetString("tag", "")
			limit := request.GetInt("limit", 25)
			fold := request.GetBool("fold", true)
			if limit > 50 {
				limit = 50
			}

			var sb strings.Builder

			if !fold {
				entries, err := db.QueryLogs(sessionID, level, tag, limit)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
				}
				renderLogEntries(&sb, entries, 0)
			} else {
				entries, err := db.QueryFoldedLogs(sessionID, level, tag, limit)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
				}
				renderFoldedLogEntries(&sb, entries)
			}

			if sb.Len() == 0 {
				return mcp.NewToolResultText("No logs found matching criteria in the current session."), nil
			}
			return mcp.NewToolResultText(sb.String()), nil
		},
		"pawsift_get_log_context": func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			logID := request.GetInt("log_id", 0)
			if logID == 0 {
				return mcp.NewToolResultError("Invalid log_id"), nil
			}
			lines := request.GetInt("lines", 15)

			entries, err := db.GetLogContext(int64(logID), lines)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
			}

			var sb strings.Builder
			renderLogEntries(&sb, entries, int64(logID))

			if sb.Len() == 0 {
				return mcp.NewToolResultText("Log entry not found."), nil
			}
			return mcp.NewToolResultText(sb.String()), nil
		},
		"pawsift_search_logs": func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query := request.GetString("query", "")
			limit := request.GetInt("limit", 25)
			fold := request.GetBool("fold", true)
			if limit > 50 {
				limit = 50
			}

			var sb strings.Builder

			if !fold {
				entries, err := db.SearchLogs(query, limit)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
				}
				renderLogEntries(&sb, entries, 0)
			} else {
				entries, err := db.SearchFoldedLogs(query, limit)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
				}
				renderFoldedLogEntries(&sb, entries)
			}

			if sb.Len() == 0 {
				return mcp.NewToolResultText("No logs found matching the query."), nil
			}

			// Add a hint if we hit the limit
			sb.WriteString("\n*Note: Output capped at the most recent results. Use a more specific query if needed.*")
			return mcp.NewToolResultText(sb.String()), nil
		},
		"pawsift_clear_logs": func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if err := db.ClearLogs(); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
			}
			if err := watcher.ClearDeviceLogs(); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Failed to clear device logs: %v", err)), nil
			}
			return mcp.NewToolResultText("All logs have been cleared successfully (DB and Device)."), nil
		},
		"pawsift_set_retention_policy": func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			maxLogs := request.GetInt("max_logs", 10000)
			maxSessions := request.GetInt("max_sessions", 3)
			cleanupInterval := request.GetInt("cleanup_interval", 30)

			if maxLogs < 100 {
				return mcp.NewToolResultError("max_logs must be at least 100"), nil
			}
			if maxSessions < 1 {
				return mcp.NewToolResultError("max_sessions must be at least 1"), nil
			}
			if cleanupInterval < 5 {
				return mcp.NewToolResultError("cleanup_interval must be at least 5 seconds"), nil
			}

			watcher.SetRetentionPolicy(maxLogs, maxSessions, cleanupInterval)
			return mcp.NewToolResultText(fmt.Sprintf(
				"Retention policy configured:\n- **Max Logs**: %d\n- **Max Sessions**: %d\n- **Cleanup Interval**: %d seconds",
				maxLogs, maxSessions, cleanupInterval)), nil
		},
		"pawsift_get_status": func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			info := watcher.GetInfo()
			count, _ := db.GetLogCount(info.CurrentSession)

			var sb strings.Builder
			sb.WriteString("### PawSift Status Dashboard\n")

			pkg := info.TargetPackage
			if pkg == "" {
				pkg = "None (System-wide)"
			}
			sb.WriteString(fmt.Sprintf("- **Target Package**: %s\n", pkg))
			sb.WriteString(fmt.Sprintf("- **Session ID**: %d\n", info.CurrentSession))

			activeStatus := "🔴 Inactive"
			if info.IsActive {
				activeStatus = "🟢 Active"
			}
			sb.WriteString(fmt.Sprintf("- **Watcher Status**: %s\n", activeStatus))
			sb.WriteString(fmt.Sprintf("- **Connected Device**: %s\n", info.DeviceSerial))
			sb.WriteString(fmt.Sprintf("- **Current Session Logs**: %d rows\n", count))

			return mcp.NewToolResultText(sb.String()), nil
		},
	}
}
