# Introduction

**Oompa** is an autonomous AI-powered code maintenance agent that uses [OpenCode](https://opencode.ai) or [Claude Code](https://docs.anthropic.com/en/docs/claude-code) to implement fixes, address reviews, resolve merge conflicts, fix CI failures, and triage flaky tests -- all without human intervention beyond the final merge.

## What It Does

- **Resolve issues** -- picks up GitHub issues with a configurable label, implements fixes, and opens pull requests.
- **Address reviews** -- reads reviewer comments and iterates on the code until reviewers are satisfied.
- **Fix CI failures** -- detects failing checks, analyzes logs, and pushes fixes.
- **Resolve merge conflicts** -- attempts an automatic rebase and falls back to the coding agent when that fails.
- **Babysit PRs** -- monitors specific PRs for reviews, CI, conflicts, and rebase.
- **Triage periodic CI** -- analyzes nightly/scheduled job failures and creates issues with root-cause analysis.

## Safety First

Oompa never merges; a human must approve and merge every PR. No force-pushes beyond `--force-with-lease` for rebase operations. On failure, issues are labeled `ai-failed` with a comment explaining the error so a human can decide next steps.

## How It Works

Oompa is a single long-running Go binary with a sequential polling loop. On each poll cycle it:

1. **Cleans up** merged/closed PRs (removes worktrees, updates state)
2. **Discovers** new issues with the configured label
3. **Processes** review comments, CI failures, merge conflicts, and rebase requests

The agent is stateless on disk -- on every startup it rebuilds state from GitHub by scanning labeled issues and matching PRs. All external interactions (GitHub API, coding agent CLI, git) are behind interfaces with mock implementations, so tests run without credentials or network access.

## Next Steps

- [Installation](getting-started/installation.md) -- get oompa running
- [Quickstart](getting-started/quickstart.md) -- your first run
- [Configuration](configuration/cli-flags.md) -- customize behavior
