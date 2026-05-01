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

const Version = "2.0.2"

func renderLogEntries(sb *strings.Builder, entries []LogEntry, centerID int64) {
	lastHeader := ""
	lastMsg := ""

	for _, e := range entries {
		header := fmt.Sprintf("%s (%d)", e.Tag, e.PID)
		if header != lastHeader {
			sb.WriteString(fmt.Sprintf("### %s\n", header))
			lastHeader = header
			lastMsg = ""
		}

		ts := e.Timestamp
		if len(ts) > 6 {
			ts = ts[6:]
		}

		firstLine, trace, _ := strings.Cut(e.Message, "\n")
		if firstLine != lastMsg {
			sb.WriteString(fmt.Sprintf("#### %s\n", firstLine))
			lastMsg = firstLine
		}

		prefix := "- "
		if e.ID == centerID {
			prefix = "> - "
		}
		sb.WriteString(fmt.Sprintf("%s[%d] **%s** %s\n", prefix, e.ID, e.Level, ts))
		sb.WriteString(indentTrace(trace))
	}
}

func renderFoldedLogEntries(sb *strings.Builder, entries []FoldedLog) {
	lastHeader := ""

	for _, e := range entries {
		header := fmt.Sprintf("%s (%d)", e.Tag, e.PID)
		if header != lastHeader {
			sb.WriteString(fmt.Sprintf("### %s\n", header))
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

		firstLine, _, _ := strings.Cut(e.Message, "\n")
		if e.Count > 1 {
			sb.WriteString(fmt.Sprintf("- [%d-%d] **%s** %s - %s %s (%dx)\n", e.StartID, e.EndID, e.Level, startTs, endTs, firstLine, e.Count))
		} else {
			sb.WriteString(fmt.Sprintf("- [%d] **%s** %s %s\n", e.StartID, e.Level, startTs, firstLine))
		}
	}
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

func SetupTools(s *server.MCPServer, db *DB, watcher *Watcher) {
	handlers := GetHandlers(db, watcher)
	
	s.AddTool(mcp.NewTool("pawsift_set_target_package",
		mcp.WithDescription("Configures the watcher to monitor a specific Android application. When this package starts, the server automatically increments the session ID to isolate new logs."),
		mcp.WithString("package",
			mcp.Description("The Android package name (e.g., com.example.app)"),
			mcp.Required(),
		),
	), server.ToolHandlerFunc(handlers["pawsift_set_target_package"]))

	s.AddTool(mcp.NewTool("pawsift_get_error_summary",
		mcp.WithDescription("Returns a Count-First Markdown list of unique ERROR and FATAL logs with their latest [ID]. Each item follows the format: '- [ID] **Count x** [Tag] Message'. Use the [ID] for surgical context retrieval."),
	), server.ToolHandlerFunc(handlers["pawsift_get_error_summary"]))

	s.AddTool(mcp.NewTool("pawsift_get_tag_summary",
		mcp.WithDescription("Returns a Count-First Markdown list of all unique log tags in the current session. Each item follows the format: '- **Count** Tag'."),
	), server.ToolHandlerFunc(handlers["pawsift_get_tag_summary"]))

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
	), server.ToolHandlerFunc(handlers["pawsift_query_logs"]))

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
	), server.ToolHandlerFunc(handlers["pawsift_get_log_context"]))

	s.AddTool(mcp.NewTool("pawsift_search_logs",
		mcp.WithDescription("Performs a global search using Hierarchical Mapping (groups identical messages under sub-headers to save tokens). Use fold=false to see individual timestamps and IDs for repetitive events."),
		mcp.WithString("query", mcp.Description("Substring to search for in log messages"), mcp.Required()),
		mcp.WithNumber("limit", mcp.Description("Max results (default 25)"), mcp.DefaultNumber(25)),
		mcp.WithBoolean("fold", mcp.Description("If true, consecutive identical logs are folded into a single entry with a count. Defaults to true."), mcp.DefaultBool(true)),
	), server.ToolHandlerFunc(handlers["pawsift_search_logs"]))

	s.AddTool(mcp.NewTool("pawsift_clear_logs",
		mcp.WithDescription("Permanently deletes all logs from the database AND clears the Android device logcat buffer. Use this to start fresh or fix startup lag."),
	), server.ToolHandlerFunc(handlers["pawsift_clear_logs"]))

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
	), server.ToolHandlerFunc(handlers["pawsift_set_retention_policy"]))

	s.AddTool(mcp.NewTool("pawsift_get_status",
		mcp.WithDescription("Returns the current status of the PawSift dashboard, including the target package, session ID, watcher activity, and connected device info."),
	), server.ToolHandlerFunc(handlers["pawsift_get_status"]))
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

	watcher := NewWatcher(db, &RealAdbRunner{})
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

	SetupTools(s, db, watcher)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
	}
}
