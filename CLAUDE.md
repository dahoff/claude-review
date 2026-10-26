Updated: 2025-11-19T15:36:00Z

# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

claude-review is a lightweight companion for working on planning documents with Claude Code. It provides a web-based interface for reviewing Markdown files, leaving inline comments, and integrating those comments back into Claude Code sessions.

### Core Workflow
1. User creates a planning document (e.g., PLAN.md) via Claude Code
2. User runs `/cr-review <file>` slash command to start server and open document in browser
3. User highlights portions of the rendered Markdown and adds comments
4. User runs `/cr-address <file>` slash command in Claude Code to pull unresolved comments
5. Claude Code reviews comment threads and can either:
   - Reply to threads to ask clarifying questions or discuss alternatives
   - Make requested changes and resolve threads when complete
6. User can reply to Claude's responses in the browser to continue the discussion
7. Browser automatically reloads to show updated document and new replies

## Architecture

### Backend (Go)
- **main.go**: Entry point with CLI commands (server, register, review, address, resolve, install, uninstall, version)
- **handlers.go**: HTTP route handlers for web UI and REST API
- **db.go**: SQLite database layer for projects and comments
- **markdown.go**: Custom goldmark renderer that adds `data-line-start` and `data-line-end` attributes to HTML elements
- **sse.go**: Server-Sent Events hub for real-time browser updates
- **daemon.go**: Daemon process management (start, stop, status, PID tracking)
- **watcher.go**: File system watcher using fsnotify for automatic reload on file changes
- **notify.go**: HTTP client for notifying server about resolved comments
- **version.go**: Version information
- **install.go**: Slash command installation and full uninstallation (server, slash commands, binary, optionally data)
- **embed.go**: Embedded frontend assets
- **git.go**: Git helpers (branches, commits, default base) and `resolveComparison`, which turns the compare page's
  mode/base/commit query into revisions
- **diff.go**: Changed-file listing, unified-diff parsing into split rows, and folding of unchanged context
- **compare.go**: `/compare/<project>` handler; places comment threads on diff lines, under a file, or in "other files"; builds the
  GitHub-style file tree (folders first, single-child folders merged) that also orders the diff sections

### Frontend
- **frontend/templates/**: Go HTML templates (viewer.html, directory.html, index.html, compare.html)
- **frontend/static/**: CSS and JavaScript for the web UI
- **viewer.js**: Handles text selection, comment creation/editing, SSE connection for live updates
- **theme.js**: Loaded in every page's `<head>`; applies the saved theme (dark by default, light via
  `data-theme="light"` on `<html>`, stored in localStorage) before first paint and adds the toggle button. All colors
  in styles.css are CSS variables defined per theme at the top of the file
- **compare.js**: Compare page: changed-file tree (filter, jump, active highlight), right-side selection to comment,
  inline threads, gap expansion, live reload

### Database Schema
SQLite database stored at `~/.local/share/claude-review/comments.db`:
- **projects**: Tracks registered project directories
- **comments**: Stores inline comments with line ranges, selected text, resolved status, threading support
  - `root_id`: Foreign key to parent comment (NULL for root comments, enables threaded discussions)
  - `author`: Name of comment author (user or agent name)
  - `resolved_by`: Name of who resolved the thread
  - `claimed_at`: Set on root comments by `address --claim` while the agent processes the thread, cleared by its reply; edits and deletes are refused (409) while a claim younger than `claimTTL` exists. Added to existing databases by `migrateDB`

### Custom Slash Commands
- `/cr-review [path]` (in `slash-commands/cr-review.md`): Runs `claude-review review --file <path>` to start the server, register the project, and open the project root (or the given file/directory) in the browser
- `/cr-diff` (in `slash-commands/cr-diff.md`): Runs `claude-review review --diff` to open the project's compare page
- `/cr-address [file]` (in `slash-commands/cr-address.md`): Runs `claude-review address --pending --file <file>` to fetch unresolved comment threads awaiting a response, with full conversation history, for every file in the project when no file is given. Instructs Claude Code to either reply to threads for discussion or make changes and resolve threads when requests are clear

## Development Commands

### Build and Run
```bash
make build              # Build binary to dist/claude-review
make dev                # Run with auto-reload using air
make air                # Alias for make dev
```

### Testing
```bash
make test               # Run tests with summary output
make test-verbose       # Run tests with detailed output
make test-one TEST=TestName  # Run single test by name
make test-ci            # Run tests with coverage report
```

### Linting and Formatting
```bash
make lint               # Run prettier and golangci-lint with auto-fix
make prettier           # Format frontend files only
```

### Installation
```bash
make install            # Build binary and install slash commands to ~/.claude/commands/
```

### Release
```bash
make build-release      # Build binary + tar frontend assets
make release-patch      # Create patch version release (x.y.Z)
make release-minor      # Create minor version release (x.Y.0)
make release-major      # Create major version release (X.0.0)
```

## Key Implementation Details

### Line Number Tracking
The markdown.go renderer walks the Goldmark AST and adds `data-line-start` and `data-line-end` attributes to block-level HTML elements. This enables the frontend to map user text selections back to source line numbers.

### Real-time Updates
The server uses multiple mechanisms for real-time updates:
- **SSE (Server-Sent Events)**: Broadcasts events to connected browsers when comments are resolved
- **File System Watching**: Uses fsnotify to watch Markdown files and automatically reload the browser when files are modified
- **Event Broadcasting**: CLI commands (like `resolve`) notify the server via HTTP POST to `/api/events`, which triggers SSE broadcasts to connected clients

### Diff Review
`/compare/<project>` shows split, side-by-side source diffs for git projects in four modes (query `mode`):
`uncommitted` (working tree incl. untracked files vs HEAD), `base` (working tree vs the merge base with the local branch
`base`, default from origin/HEAD, else main/master), `since` (working tree vs `commit`) and `commit` (`commit` vs its
parent, read-only). With no mode it
picks `base` when the current branch is ahead of the default base. Git runs in the project directory with
`--relative`, so a project below the git root sees only its own files with project-relative paths. Diffs are computed
with full context and folded server-side (3 context lines; lines with comments stay visible). All git calls go through
`gitOutput`; revisions are resolved to SHAs before use and branches must be local branches. The page subscribes to
SSE with `scope=project` and one `watch` parameter per changed file; file watches are reference counted.

### CLI Commands
- `claude-review server`: Start web server on port 4779 in foreground
- `claude-review server --daemon`: Start web server as a background daemon
- `claude-review server --stop`: Stop the running daemon
- `claude-review server --status`: Check if daemon is running and show PID info
- `claude-review register [--project <path>]`: Register a project directory (defaults to current directory)
- `claude-review review [--file <path>]`: Start server, register project, and show the URL for the project root or the given file/directory. The project is detected as the deepest of the git toplevel (the worktree root inside a git worktree) and any registered project containing the target (see `project.go`)
- `claude-review review --diff`: Like `review`, but shows the URL of the project's compare page (`/compare<project>`)
- `claude-review address [--file <path>] [--pending]`: Output unresolved comment threads (grouped by conversation) for a file, or for every file in the project grouped under `# File:` headers. `--pending` skips threads where the agent replied last; `--claim` locks the listed threads against editing until the agent replies
- `claude-review reply --comment-id <id> --message <text>`: Reply to a comment thread (releases the thread's claim)
- `claude-review resolve --file <path>`: Mark all comments as resolved for file (relative or absolute path)
- `claude-review resolve --comment-id <id>`: Mark a specific comment thread as resolved
- `claude-review install`: Install slash commands to ~/.claude/commands/
- `claude-review uninstall [--purge]`: Stop the server, remove slash commands from ~/.claude/commands/ and the binary from ~/.local/bin/; `--purge` also deletes the data directory
- `claude-review version`: Show version information

### Daemon Management
The server can run as a background daemon:
- **PID File**: Stored at `~/.local/share/claude-review/server.pid`
- **Log File**: Daemon logs stored at `~/.local/share/claude-review/server.log`
- **Signal Handling**: Graceful shutdown on SIGTERM/SIGINT with automatic cleanup of PID file
- **Double-fork**: Uses Unix double-fork pattern to fully detach from terminal

## Configuration

- **CGO_ENABLED=1**: Required for go-sqlite3
- **DEBUG_SQL=1**: Environment variable to enable SQL query logging
- Port 4779 is hardcoded for the web server
