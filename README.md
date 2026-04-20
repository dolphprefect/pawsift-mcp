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

## Debugging Workflow

**PawSift** provides an intelligent abstraction layer over raw Android logs, optimized for AI-assisted debugging. Follow this workflow for the best results:

### 1. Setup the Target
Before you start testing, tell **PawSift** which app you are focusing on:
> *User to LLM:* "Set the target package to `com.your.app.package` and watch for logs."
> *LLM Action:* Calls `set_target_package(package="com.your.app.package")`.

### 2. Verify State
Orient yourself before starting a deep dive:
> *LLM Action:* Calls `get_status()`.
> *Output:* Shows if the watcher is active, the connected device serial, and the current log count.

### 3. Trigger the Issue
Run your app on your device or emulator. **PawSift** will automatically detect the "Process Started" event and start a fresh session.

### 4. Identify the Crash (The "Bird's Eye View")
If the app crashes or behaves unexpectedly, start with a high-level summary to save tokens:
> *User to LLM:* "What just happened? Any crashes?"
> *LLM Action:* Calls `get_error_summary()`.
> *Output:* Returns unique error signatures, counts, and their latest **[ID]**.

### 5. Investigate the Logs (Surgical Follow-up)
Don't query all logs. Use the **[ID]** from the summary to jump straight to the relevant context:
> *LLM Action:* Calls `get_log_context(log_id=1234, lines=20)`.
> *Output:* Returns 20 lines leading up to and following the crash, giving you visibility into state changes, network responses, or UI events.

> **Pro Tip: Suppression & Search**
> - If you see too much system noise (e.g., `WifiHAL`, `AOC`), tell the LLM: *"Ignore system tags and focus on my app logs."*
> - Use `search_logs(query="FATAL EXCEPTION")` to find specific events across the entire history if the current session summary is too broad.

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
- **Database**: `.pawsift/logcat.db` (automatically created)
- **Polling Rate**: 1 second (configurable in `logcat.go`)
- **Version**: Check via `pawsift -version`
