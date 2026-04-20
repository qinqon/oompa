# Ambient Code Workflows

These workflows are designed for the [Ambient Code platform](https://github.com/ambient-code/platform).
Each subdirectory is a self-contained workflow selectable in the Ambient GUI.

## Workflows

| Workflow | Description |
|----------|-------------|
| `issue-resolver` | Watches for labeled issues, implements fixes, creates PRs |
| `pr-babysitter` | Monitors specific PRs: CI + conflicts + rebase (no reviews) |
| `ci-fixer` | Monitors specific PRs: CI failures only |
| `review-responder` | Monitors specific PRs: review comments only |

## Prerequisites

The following tools must be installed and available in PATH on the runner:

- `claude` (Claude Code CLI)
- `gh` (GitHub CLI)

## How It Works

1. Select a workflow subdirectory in the Ambient GUI.
2. The Ambient platform starts a Claude Code session with the workflow directory as CWD.
3. Claude greets you and asks for the required information (repository, PR numbers, etc.).
4. Once you provide the details, Claude downloads the runner script and starts the agent.
5. The runner script downloads the latest `ai-agent` binary from GitHub releases and runs it in a restart loop with auto-update support.
