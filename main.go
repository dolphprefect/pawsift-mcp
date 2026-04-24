package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const Version = "1.5.2"

func splitMessage(msg string) (first, rest string) {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return msg[:i], msg[i+1:]
	}
	return msg, ""
}

func indentTrace(trace string) string {
	if trace == "" {
		return ""
	}
	var sb strings.Builder
	for _, line := range strings.Split(trace, "\n") {
		sb.WriteString("  ")
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func main() {
	versionFlag := flag.Bool("version", false, "Print the version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println(Version)
		os.Exit(0)
	}

	if err := os.MkdirAll(".pawsift", 0755); err != nil {
		log.Fatalf("Failed to create .pawsift directory: %v", err)
	}

	db, err := InitDB(".pawsift/logcat.db")
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	watcher := NewWatcher(db)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := watcher.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Watcher error: %v\n", err)
		}
	}()

	s := server.NewMCPServer(
		"PawSift",
		Version,
		server.WithLogging(),
	)

	// Set Target Package Tool
	s.AddTool(mcp.NewTool("pawsift_set_target_package",
		mcp.WithDescription("Configures the watcher to monitor a specific Android application. When this package starts, the server automatically increments the session ID to isolate new logs."),
		mcp.WithString("package",
			mcp.Description("The Android package name (e.g., com.example.app)"),
			mcp.Required(),
		),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		pkg := request.GetString("package", "")
		if pkg == "" {
			return mcp.NewToolResultError("Invalid package name"), nil
		}
		watcher.SetTargetPackage(pkg)
		return mcp.NewToolResultText(fmt.Sprintf("Now tracking sessions for: %s", pkg)), nil
	})

	// Get Error Summary Tool
	s.AddTool(mcp.NewTool("pawsift_get_error_summary",
		mcp.WithDescription("Returns a Count-First Markdown list of unique ERROR and FATAL logs with their latest [ID]. Each item follows the format: '- [ID] **Count x** [Tag] Message'. Use the [ID] for surgical context retrieval."),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID := watcher.GetCurrentSession()
		summary, err := db.GetErrorSummary(sessionID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
		}
		return mcp.NewToolResultText(summary), nil
	})

	// Get Tag Summary Tool
	s.AddTool(mcp.NewTool("pawsift_get_tag_summary",
		mcp.WithDescription("Returns a Count-First Markdown list of all unique log tags in the current session. Each item follows the format: '- **Count** Tag'."),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID := watcher.GetCurrentSession()
		summary, err := db.GetTagSummary(sessionID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
		}
		return mcp.NewToolResultText(summary), nil
	})

	// Query Logs Tool
	s.AddTool(mcp.NewTool("pawsift_query_logs",
		mcp.WithDescription("Retrieves filtered logs using Hierarchical Mapping (groups identical messages under sub-headers to save tokens). AVOID using this for crash investigation if you already have an error [ID]—use get_log_context() instead."),
		mcp.WithString("level", mcp.Description("Filter by log level: V (Verbose), D (Debug), I (Info), W (Warn), E (Error), F (Fatal)")),
		mcp.WithString("tag", mcp.Description("Filter by tag name (substring match)")),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of lines to return. Hard cap at 50 to protect context."),
			mcp.DefaultNumber(25),
		),
		mcp.WithBoolean("fold",
			mcp.Description("If true, consecutive identical logs are folded into a single entry with a count. Defaults to true."),
			mcp.DefaultBool(true),
		),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID := watcher.GetCurrentSession()
		level := request.GetString("level", "")
		tag := request.GetString("tag", "")
		limit := request.GetInt("limit", 25)
		fold := request.GetBool("fold", true)
		if limit > 50 {
			limit = 50
		}

		result := ""
		lastHeader := ""
		lastMsg := ""

		if !fold {
			entries, err := db.QueryLogs(sessionID, level, tag, limit)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
			}

			for _, e := range entries {
				header := fmt.Sprintf("%s (%d)", e.Tag, e.PID)
				if header != lastHeader {
					result += fmt.Sprintf("### %s\n", header)
					lastHeader = header
					lastMsg = ""
				}

				ts := e.Timestamp
				if len(ts) > 6 {
					ts = ts[6:]
				}

				firstLine, trace := splitMessage(e.Message)
				if firstLine != lastMsg {
					result += fmt.Sprintf("#### %s\n", firstLine)
					lastMsg = firstLine
				}
				result += fmt.Sprintf("- [%d] **%s** %s\n", e.ID, e.Level, ts)
				result += indentTrace(trace)
			}
		} else {
			entries, err := db.QueryFoldedLogs(sessionID, level, tag, limit)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
			}

			for _, e := range entries {
				header := fmt.Sprintf("%s (%d)", e.Tag, e.PID)
				if header != lastHeader {
					result += fmt.Sprintf("### %s\n", header)
					lastHeader = header
				}

				startTs := e.StartTime
				if len(startTs) > 6 {
					startTs = startTs[6:]
				}
				endTs := e.EndTime
				if len(endTs) > 6 {
					endTs = endTs[6:]
				}

				firstLine, trace := splitMessage(e.Message)
				if e.Count > 1 {
					result += fmt.Sprintf("- [%d-%d] **%s** %s - %s %s (%dx)\n", e.StartID, e.EndID, e.Level, startTs, endTs, firstLine, e.Count)
				} else {
					result += fmt.Sprintf("- [%d] **%s** %s %s\n", e.StartID, e.Level, startTs, firstLine)
					result += indentTrace(trace)
				}
			}
		}

		if result == "" {
			return mcp.NewToolResultText("No logs found matching criteria in the current session."), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	// Get Log Context Tool
	s.AddTool(mcp.NewTool("pawsift_get_log_context",
		mcp.WithDescription("Retrieves lines surrounding a specific log ID. Essential for seeing what led up to a crash or event."),
		mcp.WithNumber("log_id",
			mcp.Description("The ID of the log entry (from query_logs) to center the window on"),
			mcp.Required(),
		),
		mcp.WithNumber("lines",
			mcp.Description("Number of lines to fetch before and after (default 15)"),
			mcp.DefaultNumber(15),
		),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logID := request.GetInt("log_id", 0)
		if logID == 0 {
			return mcp.NewToolResultError("Invalid log_id"), nil
		}
		lines := request.GetInt("lines", 15)

		entries, err := db.GetLogContext(int64(logID), lines)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
		}

		result := ""
		lastHeader := ""
		lastMsg := ""

		for _, e := range entries {
			header := fmt.Sprintf("%s (%d)", e.Tag, e.PID)
			if header != lastHeader {
				result += fmt.Sprintf("### %s\n", header)
				lastHeader = header
				lastMsg = ""
			}

			ts := e.Timestamp
			if len(ts) > 6 {
				ts = ts[6:]
			}

			firstLine, trace := splitMessage(e.Message)
			if firstLine != lastMsg {
				result += fmt.Sprintf("#### %s\n", firstLine)
				lastMsg = firstLine
			}

			prefix := "- "
			if e.ID == int64(logID) {
				prefix = "> - "
			}
			result += fmt.Sprintf("%s[%d] **%s** %s\n", prefix, e.ID, e.Level, ts)
			result += indentTrace(trace)
		}
		return mcp.NewToolResultText(result), nil
	})

	// Search Logs Tool
	s.AddTool(mcp.NewTool("pawsift_search_logs",
		mcp.WithDescription("Performs a global search using Hierarchical Mapping (groups identical messages under sub-headers to save tokens). Use fold=false to see individual timestamps and IDs for repetitive events."),
		mcp.WithString("query", mcp.Description("Substring to search for in log messages"), mcp.Required()),
		mcp.WithNumber("limit", mcp.Description("Max results (default 25)"), mcp.DefaultNumber(25)),
		mcp.WithBoolean("fold", mcp.Description("If true, consecutive identical logs are folded into a single entry with a count. Defaults to true."), mcp.DefaultBool(true)),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := request.GetString("query", "")
		limit := request.GetInt("limit", 25)
		fold := request.GetBool("fold", true)
		if limit > 50 {
			limit = 50
		}


		result := ""
		lastHeader := ""
		lastMsg := ""

		if !fold {
			entries, err := db.SearchLogs(query, limit)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
			}

			for _, e := range entries {
				header := fmt.Sprintf("%s (%d)", e.Tag, e.PID)
				if header != lastHeader {
					result += fmt.Sprintf("### %s\n", header)
					lastHeader = header
					lastMsg = ""
				}

				ts := e.Timestamp
				if len(ts) > 6 {
					ts = ts[6:]
				}

				firstLine, trace := splitMessage(e.Message)
				if firstLine != lastMsg {
					result += fmt.Sprintf("#### %s\n", firstLine)
					lastMsg = firstLine
				}
				result += fmt.Sprintf("- [%d] **%s** %s\n", e.ID, e.Level, ts)
				result += indentTrace(trace)
			}
		} else {
			entries, err := db.SearchFoldedLogs(query, limit)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
			}

			for _, e := range entries {
				header := fmt.Sprintf("%s (%d)", e.Tag, e.PID)
				if header != lastHeader {
					result += fmt.Sprintf("### %s\n", header)
					lastHeader = header
				}

				startTs := e.StartTime
				if len(startTs) > 6 {
					startTs = startTs[6:]
				}
				endTs := e.EndTime
				if len(endTs) > 6 {
					endTs = endTs[6:]
				}

				firstLine, trace := splitMessage(e.Message)
				if e.Count > 1 {
					result += fmt.Sprintf("- [%d-%d] **%s** %s - %s %s (%dx)\n", e.StartID, e.EndID, e.Level, startTs, endTs, firstLine, e.Count)
				} else {
					result += fmt.Sprintf("- [%d] **%s** %s %s\n", e.StartID, e.Level, startTs, firstLine)
					result += indentTrace(trace)
				}
			}
		}

		if result == "" {
			return mcp.NewToolResultText("No logs found matching the query."), nil
		}

		// Add a hint if we hit the limit
		result += "\n*Note: Output capped at the most recent results. Use a more specific query if needed.*"
		return mcp.NewToolResultText(result), nil
	})

	// Clear Logs Tool
	s.AddTool(mcp.NewTool("pawsift_clear_logs",
		mcp.WithDescription("Permanently deletes all logs from the database AND clears the Android device logcat buffer. Use this to start fresh or fix startup lag."),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := db.ClearLogs(); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Database error: %v", err)), nil
		}
		if err := watcher.ClearDeviceLogs(); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to clear device logs: %v", err)), nil
		}
		return mcp.NewToolResultText("All logs have been cleared successfully (DB and Device)."), nil
	})

	// Set Retention Policy Tool
	s.AddTool(mcp.NewTool("pawsift_set_retention_policy",
		mcp.WithDescription("Configure log retention limits: maximum total logs to keep, maximum sessions to retain, and cleanup interval in seconds."),
		mcp.WithNumber("max_logs",
			mcp.Description("Maximum total log entries to keep (e.g., 10000)"),
			mcp.DefaultNumber(10000),
		),
		mcp.WithNumber("max_sessions",
			mcp.Description("Maximum number of sessions to keep (e.g., 3)"),
			mcp.DefaultNumber(3),
		),
		mcp.WithNumber("cleanup_interval",
			mcp.Description("Cleanup interval in seconds (e.g., 30)"),
			mcp.DefaultNumber(30),
		),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	})

	// Get Status Tool
	s.AddTool(mcp.NewTool("pawsift_get_status",
		mcp.WithDescription("Returns the current status of the PawSift dashboard, including the target package, session ID, watcher activity, and connected device info."),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		info := watcher.GetInfo()
		count, _ := db.GetLogCount(info.CurrentSession)

		status := "### PawSift Status Dashboard\n"
		pkg := info.TargetPackage
		if pkg == "" {
			pkg = "None (System-wide)"
		}
		status += fmt.Sprintf("- **Target Package**: %s\n", pkg)
		status += fmt.Sprintf("- **Session ID**: %d\n", info.CurrentSession)
		
		activeStatus := "🔴 Inactive"
		if info.IsActive {
			activeStatus = "🟢 Active"
		}
		status += fmt.Sprintf("- **Watcher Status**: %s\n", activeStatus)
		status += fmt.Sprintf("- **Connected Device**: %s\n", info.DeviceSerial)
		status += fmt.Sprintf("- **Current Session Logs**: %d rows\n", count)

		return mcp.NewToolResultText(status), nil
	})

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
	}
}
