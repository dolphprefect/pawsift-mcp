# PawSift 🐾

**PawSift** is a specialized Model Context Protocol (MCP) server that bridges Android Logcat to LLMs. It provides a token-efficient, session-aware interface for real-time log analysis, using a polling-based SQLite ingestor to sift through raw output and surface only what matters.

## Features

- **Zero-Touch Session Tracking**: Automatically detects app restarts via `ActivityManager` and rotates sessions.
- **Token Efficient**: Pre-aggregates errors, folds repetitive consecutive logs, and uses **Hierarchical Mapping** to group identical messages under sub-headers.
- **Surgical Querying**: Filter logs by level, tag, and search terms with strict line limits to protect the context window.
- **Tag Discovery**: Quickly list all active tags in the current session.
- **Contextual Windows**: Fetch logs surrounding a specific event for precise debugging.
- **Global Search**: Search the entire log history across all sessions with a single command.
- **Status Dashboard**: A specialized heartbeat tool to monitor watcher health, session IDs, and log backlog.
- **Automatic Retention Policy**: Enforces max log limits (default 10k logs, 3 sessions) with continuous cleanup during polling—prevents unbounded database growth.
- **Configurable Cleanup**: Adjust retention limits on-the-fly via `set_retention_policy()` without restarting.
- **Optimized Queries**: Database indexes on tag, message, timestamp, and composite filters for fast lookups even with large log volumes.
- **Maintenance**: Built-in tools to clear both local and device log buffers.
- **Go-Based**: Fast, single-binary distribution with no CGO dependencies.

## Installation

### Prebuilt binary (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/DolphRoger/pawsift/main/install.sh | sh
```

Downloads the right binary for your OS and architecture, installs it to `~/.local/bin/pawsift`, and **automatically registers** the server in your Gemini CLI and Claude Code (CLI) configuration files.

Supports: Linux amd64/arm64, macOS amd64/arm64.

### From source

```bash
make deploy
```

Builds from source and does the same install + registration as above.

## Uninstallation

```bash
make uninstall
```

## Tools

| Tool | Description |
|---|---|
| `pawsift_set_target_package` | Configures the watcher to monitor a specific Android application. When this package starts, the session ID is automatically rotated to isolate new logs. |
| `pawsift_get_status` | Returns the current status dashboard: target package, session ID, watcher activity, connected device, and current log count. |
| `pawsift_get_error_summary` | Returns a count-first Markdown list of unique ERROR and FATAL logs with their latest `[ID]`. Use the ID for surgical context retrieval. |
| `pawsift_get_tag_summary` | Returns a count-first Markdown list of all unique log tags in the current session. |
| `pawsift_query_logs` | Retrieves filtered logs (by level and/or tag) using Hierarchical Mapping. Supports folding of consecutive identical messages. |
| `pawsift_get_log_context` | Retrieves lines surrounding a specific log ID. Essential for seeing what led up to a crash or event. |
| `pawsift_search_logs` | Performs a global search across the entire log history using Hierarchical Mapping. |
| `pawsift_clear_logs` | Permanently deletes all logs from the database and clears the Android device logcat buffer. |
| `pawsift_set_retention_policy` | Configure retention limits: maximum total logs, maximum sessions to keep, and cleanup interval in seconds. |

## Debugging Workflow

**PawSift** provides an intelligent abstraction layer over raw Android logs, optimized for AI-assisted debugging. Follow this workflow for the best results:

### 1. Setup the Target
Before you start testing, tell **PawSift** which app you are focusing on:
> *User to LLM:* "Set the target package to `com.your.app.package` and watch for logs."
> *LLM Action:* Calls `pawsift_set_target_package(package="com.your.app.package")`.

### 2. Verify State
Orient yourself before starting a deep dive:
> *LLM Action:* Calls `pawsift_get_status()`.
> *Output:* Shows if the watcher is active, the connected device serial, and the current log count.

### 3. Trigger the Issue
Run your app on your device or emulator. **PawSift** will automatically detect the "Process Started" event and start a fresh session.

### 4. Identify the Crash (The "Bird's Eye View")
If the app crashes or behaves unexpectedly, start with a high-level summary to save tokens:
> *User to LLM:* "What just happened? Any crashes?"
> *LLM Action:* Calls `pawsift_get_error_summary()`.
> *Output:* Returns unique error signatures, counts, and their latest **[ID]**.

### 5. Investigate the Logs (Surgical Follow-up)
Don't query all logs. Use the **[ID]** from the summary to jump straight to the relevant context:
> *LLM Action:* Calls `pawsift_get_log_context(log_id=1234, lines=20)`.
> *Output:* Returns 20 lines leading up to and following the crash, giving you visibility into state changes, network responses, or UI events.

> **Pro Tip: Suppression & Search**
> - If you see too much system noise (e.g., `WifiHAL`, `AOC`), tell the LLM: *"Ignore system tags and focus on my app logs."*
> - Use `pawsift_search_logs(query="FATAL EXCEPTION")` to find specific events across the entire history if the current session summary is too broad.

## Retention Policy Management

**PawSift** automatically manages database growth with a configurable retention policy. By default:
- **Max 10,000 logs** are kept across all sessions
- **Last 3 sessions** are retained; older ones are deleted
- **Cleanup runs every 30 seconds** during polling to enforce limits

### Adjusting Limits On-the-Fly

Use `pawsift_set_retention_policy()` to tune limits without restarting:

```
pawsift_set_retention_policy(max_logs=5000, max_sessions=2, cleanup_interval=15)
```

**Use cases:**
- **Long debugging session**: Reduce limits (5k logs, 2 sessions, 15s cleanup) to keep the database lean
- **Quick reproduction**: Increase limits (50k logs, 5 sessions, 60s cleanup) if you need more historical context
- **Tight constraints**: Minimal mode (1k logs, 1 session, 10s cleanup) for resource-constrained environments

After each cleanup cycle, disk space is reclaimed via `VACUUM`.

## Configuration for MCP Clients

`make deploy` handles this automatically for Gemini CLI and Claude Code (CLI). For manual setup or other clients, use the following:

```json
{
  "mcpServers": {
    "pawsift": {
      "command": "/home/YOUR_USER/.local/bin/pawsift"
    }
  }
}
```

## Housekeeping

- **Binary Location**: `build/pawsift`
- **Database**: `.pawsift/logcat.db` (automatically created, SQLite with WAL mode and indexes for fast queries)
- **Polling Rate**: 1 second (configurable in `logcat.go`)
- **Retention Defaults**: 10,000 max logs, 3 max sessions, 30-second cleanup interval (configurable via `set_retention_policy()`)
- **Version**: Check via `pawsift -version`
