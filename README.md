# PawSift 🐾

**PawSift** is a high-performance Model Context Protocol (MCP) server that bridges Android Logcat to LLMs. It provides a token-efficient, session-aware interface for real-time log analysis, using a polling-based SQLite ingestor to sift through raw output and surface only what matters.

## Features

- **High-Throughput Processing**: Regex-free manual string parser and hash-based dedup (using `hash/maphash`) for hot-path log processing at 5,000+ logs/sec.
- **Zero-Touch Session Tracking**: Automatically detects app restarts via `ActivityManager` and rotates sessions.
- **Token Efficient**: Pre-aggregates errors, folds repetitive consecutive logs, and uses **Hierarchical Mapping** to group identical messages under sub-headers.
- **Surgical Querying**: Filter logs by level, tag, and search terms with strict line limits to protect the context window.
- **Tag Discovery**: Quickly list all active tags in the current session.
- **Contextual Windows**: Fetch logs surrounding a specific event for precise debugging.
- **Global Search**: Search the entire log history across all sessions with a single command.
- **Status Dashboard**: A specialized heartbeat tool to monitor watcher health, session IDs, and log backlog.
- **Automatic Retention Policy**: Enforces max log limits (default 10k logs, 3 sessions) with continuous cleanup during polling—prevents unbounded database growth.
- **Configurable Cleanup**: Adjust retention limits on-the-fly via `set_retention_policy()` without restarting.
- **WAL Mode SQLite**: Write-Ahead Logging for concurrent read/write access without `database is locked` errors.
- **Optimized Queries**: Database indexes on tag, message, timestamp, and composite filters for fast lookups even with large log volumes.
- **Maintenance**: Built-in tools to clear both local and device log buffers.
- **Go-Based**: Fast, single-binary distribution with no CGO dependencies.

## Installation

### Prebuilt binary (recommended)

**Linux / macOS**
```bash
curl -fsSL https://raw.githubusercontent.com/dolphprefect/pawsift-mcp/main/install.sh | sh
```

**Windows (PowerShell)**
```powershell
irm https://raw.githubusercontent.com/dolphprefect/pawsift-mcp/main/install.ps1 | iex
```

Downloads the right binary for your OS and architecture, installs it to `~/.local/bin/pawsift` (or `%USERPROFILE%\.local\bin\pawsift.exe` on Windows), and **automatically registers** the server in your Gemini CLI and Claude Code (CLI) configuration files.

Supports: Linux amd64/arm64, macOS amd64/arm64, Windows amd64.

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

## Token Efficiency

Android logs are extremely verbose. A single app launch can produce thousands of lines, most of them repetitive noise. Feeding raw logcat into an LLM context is wasteful and often hits limits. PawSift addresses this at two levels.

### Folding

When `fold=true` (the default in `pawsift_query_logs` and `pawsift_search_logs`), consecutive identical log messages are collapsed into a single entry annotated with a count and time range:

```
- [1042-1089] **D** 09:14.201 - 09:14.812 Choreographer: Skipped 48 frames (48x)
```

Without folding, that same stretch would emit 48 separate lines. A busy app with repeated WiFi probes, sensor polling, or animation callbacks can compress 200+ raw lines down to a handful of folded entries — a 10–50× reduction in tokens for those spans.

### Hierarchical Mapping

Beyond folding, results are structured using **Hierarchical Mapping**: logs are grouped first by tag and PID (`### Tag (PID)`), then by unique message (`#### Message`), with individual occurrences listed underneath. This means the LLM receives a structured summary rather than a flat stream:

```
### MyApp (12345)
#### Failed to load resource
- [301] **E** 09:15.001
- [318] **E** 09:15.430

### NetworkManager (987)
#### Socket timeout
- [412] **W** 09:15.102
```

Repeated messages from the same source appear once as a header with their occurrences listed below, rather than duplicating the message text on every line.

### ID-Based Surgical Access

Every log entry carries a stable `[ID]`. The summary tools (`pawsift_get_error_summary`, `pawsift_get_tag_summary`) return only counts and IDs — not the full log body. Once you have an ID of interest, `pawsift_get_log_context` fetches just the surrounding window. This two-step pattern (summarise → zoom) avoids loading the full log history into context entirely.

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

## Testing

**PawSift** has comprehensive test coverage including unit tests, edge case parsing, concurrency stress tests, and data race detection.

### Running Tests

```bash
make test        # Standard test suite
make test-race   # With Go race detector (recommended before releases)
```

### Test Coverage

| Layer | Tests |
|---|---|
| **Parser Edge Cases** | 13 subtests: empty input, truncated lines, missing colons, multiple colons, variable spacing, invalid level chars, overflow values |
| **Hash Dedup** | Identical-line rejection, single-char diff acceptance, timestamp-boundary reset |
| **fastAtoi** | Boundary: empty string, zero, normal values, overflow (returns 0 safely) |
| **WAL Mode** | Verifies `PRAGMA journal_mode=wal` and `PRAGMA synchronous=1` on file-backed databases |
| **Concurrent Load** | 10,000 log lines pumped through `processLine` in a goroutine with concurrent DB readers — verified under the race detector |
| **Streaming** | Context cancellation, retry logic, session restart detection |
| **DB Fundamentals** | CRUD operations, cleanup, folding, retention policy enforcement |
| **Tool Handlers** | Full integration test of all MCP tool endpoints |
| **Render/Format** | Log entry rendering, indentation, folding output |

19 test functions across 6 files (21 top-level functions including subtests), all passing cleanly under `-race`.

### Race Detector

All tests are verified with `go test -race` to guarantee no data races in the streaming pipeline, dedup map, and concurrent DB access patterns.

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
- **Channel Buffer**: 10,000 lines (prevents scanner backpressure during DB write contention)
- **Dedup Strategy**: `uint64` hash via `hash/maphash` (zero-allocation, avoids storing full log strings)
- **Parser**: Regex-free manual string slicing (`processLine` uses `strings.IndexByte` / `strings.Cut`)
- **Retention Defaults**: 10,000 max logs, 3 max sessions, 30-second cleanup interval (configurable via `set_retention_policy()`)
- **Version**: Check via `pawsift -version`
