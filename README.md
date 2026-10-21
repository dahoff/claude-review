[![codecov](https://codecov.io/gh/Ch00k/claude-review/graph/badge.svg?token=VFe48YCOtf)](https://codecov.io/gh/Ch00k/claude-review)

# claude-review

**claude-review** is a lightweight companion for working on documents with Claude Code. It lets you review a Markdown
document in the browser, leave inline comments, and hand those comments back to the same Claude Code session that
created the document.

![claude-review interface showing inline comments on a Markdown document](example.png)

## Motivation

When working with Claude Code to generate documents in Markdown format, the typical workflow involves switching back and
forth between reviewing the Markdown document and the Claude Code session. You read through the document, switch back to
the Claude Code session, and manually describe which sections need changes - often copying and pasting text snippets to
provide context.

**claude-review** streamlines this process by enabling inline comments directly in the browser, similar to Atlassian
Confluence or Google Docs. You can highlight any portion of the rendered Markdown and add contextual feedback. The same
Claude Code instance that generated the document can then fetch these comments, understand exactly what needs to change,
and update the document automatically. Comments support threaded discussions - Claude Code can reply to your feedback to
ask clarifying questions or confirm understanding before making changes. Since the browser view refreshes on file
changes, you see your edits immediately without any manual intervention.

This keeps you in flow: the agent retains full context of the document it generated, your comments are precisely
anchored to specific sections, threaded discussions enable back-and-forth clarification, and the feedback loop happens
in seconds rather than minutes.

## How it fits into your workflow

1. Ask Claude Code to create a Markdown document (e.g. `PLAN.md`)
2. Run `/cr-review` in Claude Code and open the URL it returns. It opens the project root (the git root, or the
   worktree root when run inside a git worktree), where you can browse to any Markdown file. Pass a path such as
   `/cr-review PLAN.md` to open a specific file or directory
3. Highlight portions of any document and add contextual comments. The file listing shows how many unresolved
   threads each file has
4. Run `/cr-address` in your Claude Code session (or `/cr-address PLAN.md` for a single file)
   - Claude Code picks up comments from every file you commented on in the project
   - Claude Code will see all comment threads and their replies
   - It can discuss your feedback by replying to threads
   - It can make changes and resolve threads when complete
5. Continue the discussion by adding replies to comment threads in the browser
   - Use **Edit** on your latest message in a thread to change it. Editing is locked while Claude is processing
     the thread, and unlocks once it replies
6. Repeat steps 4-5 until the document matches your intent

## Requirements

- Linux or macOS
- Claude Code CLI
- A modern web browser

## Installation

### Automated (recommended)

```bash
curl -fsSL https://github.com/Ch00k/claude-review/releases/latest/download/install.sh | bash
```

The installer will:
- Download and install the `claude-review` binary to `~/.local/bin/`
- Install the `/cr-review` and `/cr-address` slash commands to `~/.claude/commands/`

### Manual

1. Download the binary for your platform from the [latest release](https://github.com/Ch00k/claude-review/releases/latest)
2. Make it executable and move it to your PATH:
   ```bash
   chmod +x claude-review-<os>-<arch>
   mv claude-review-<os>-<arch> ~/.local/bin/claude-review
   ```
3. Install the slash commands:
   ```bash
   claude-review install
   ```

Make sure `~/.local/bin` is in your `PATH`:
```bash
export PATH="$HOME/.local/bin:$PATH"
```

## Uninstallation

To completely remove claude-review from your system:

1. Stop the daemon if it's running:
   ```bash
   claude-review server --stop
   ```

2. Uninstall the slash commands:
   ```bash
   claude-review uninstall
   ```

3. Remove the binary:
   ```bash
   rm ~/.local/bin/claude-review
   ```

4. (Optional) Remove the data directory (contains comments database, PID file, and logs):
   ```bash
   rm -rf ~/.local/share/claude-review
   ```

## Architecture

For a detailed overview of the architecture, see [ARCHITECTURE](ARCHITECTURE.md).
