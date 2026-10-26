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

### Reviewing changes

In a git project, run `/cr-diff` (or click **Changes** on any project page) to review only what changed, GitHub
style, as a side-by-side source diff of every changed file, Markdown or code:

- **Uncommitted**: your working tree, including untracked files, against the last commit
- **Since base**: everything on your branch or worktree, including uncommitted edits, against the point where it
  split from a base branch you pick (defaults to `main`)
- **Since commit**: everything since a commit you pick, including uncommitted edits (defaults to the commit before
  the latest one)
- **Single commit**: what one commit changed (read-only)

Select lines on the right-hand side to comment on them. These comments work like any other: `/cr-address` picks them
up, and Claude can reply and resolve them. Comments on files that are not in the current diff are listed at the
bottom of the page.

### All threads in one place

Click **Threads** on any project page for every comment thread in the project, grouped by file, like the conversation
tab of a pull request. Filter by **Waiting on you** (Claude replied last), **Waiting on Claude**, or **Resolved**;
reply or resolve right there, expand the file lines around a comment with **Show context**, or jump to the thread
with **Open in file**.

## Requirements

- Linux or macOS
- Claude Code CLI
- A modern web browser

## Installation

### From source (recommended)

Requires [Go](https://go.dev/doc/install) and a C compiler (CGO is used for SQLite). On Linux without Go, the
installer builds in a Docker container instead (`./install.sh --docker` forces this); the installed binary then needs
only glibc 2.34+ and git, not Go or Docker.

```bash
git clone https://github.com/dahoff/claude-review.git
cd claude-review
./install.sh
```

The installer will:
- Build the `claude-review` binary from the checkout and install it to `~/.local/bin/`
- Install the `/cr-review`, `/cr-diff` and `/cr-address` slash commands to `~/.claude/commands/`
- Restart the server if it was running

To upgrade, pull the latest changes and run `./install.sh` again.

### Prebuilt binary

1. Download the binary for your platform from the [latest release](https://github.com/dahoff/claude-review/releases/latest)
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

```bash
claude-review uninstall
```

This stops the server if it's running, removes the `/cr-review`, `/cr-diff` and `/cr-address` slash commands
from `~/.claude/commands/`, and removes the binary from `~/.local/bin/`.

Your comments database and logs in `~/.local/share/claude-review` are kept. To delete them too:

```bash
claude-review uninstall --purge
```

## Architecture

For a detailed overview of the architecture, see [ARCHITECTURE](ARCHITECTURE.md).
