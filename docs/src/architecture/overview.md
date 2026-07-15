# Architecture Overview

Oompa is a single long-running Go binary with a sequential polling loop.

## Design Decisions

- **Polling, not webhooks**: Avoids inbound connectivity requirements, NAT traversal, and webhook secret management. The agent runs behind firewalls and on developer machines without exposing ports. The trade-off is higher latency (bounded by `--poll-interval`) which is acceptable for code maintenance tasks that tolerate minutes of delay.

- **Sequential, not parallel**: One issue at a time prevents race conditions on shared git state (worktrees, branches). A goroutine-per-issue model would require locking around git operations and make debugging harder. The `--max-workers` flag allows opt-in parallelism when the user accepts the complexity.

- **Interfaces over mocks**: All external dependencies (GitHub API, Claude CLI, git) are behind Go interfaces. This enables deterministic unit tests without credentials, network access, or subprocess execution.

- **Stateless on disk**: The agent rebuilds state from GitHub on every startup by scanning labeled issues and matching PRs. This eliminates state file corruption, migration, and backup concerns. The trade-off is a small startup cost from API calls.

- **CLI subprocess, not SDK**: The coding agent is invoked via the CLI (`claude -p` or `opencode`) rather than an SDK. This decouples the agent from release cycles and authentication flows, and allows swapping agents via `--agent`.

## Invariants

- Oompa never merges -- it only creates or updates PRs. A human must approve and merge.
- Force-pushes use `--force-with-lease` only for history-rewriting operations (rebase, squash).
- Bot-posted comments are tagged with `<!-- oompa-bot -->` markers to prevent self-reply loops.

## Directory Structure

```
cmd/oompa/
  main.go              -- entry point, config parsing, subcommand dispatch, polling loop
  status.go            -- `oompa status` subcommand (print-and-exit snapshot)
  tui.go               -- `oompa tui` subcommand (live bubbletea dashboard)
pkg/agent/
  types.go             -- shared types (Issue, ReviewComment, PR, ClaudeResult, IssueWork)
  config.go            -- Config struct
  event.go             -- EventEmitter interface, Event/WorkerState types
  eventserver.go       -- SocketEventServer (Unix socket server)
  eventclient.go       -- EventClient (connects to daemon socket for status/tui)
  github.go            -- GitHubClient interface + go-github implementation
  claude.go            -- CommandRunner interface + coding agent CLI invocation
  worktree.go          -- git worktree management
  state.go             -- in-memory state rebuilt from GitHub on startup
  prompt.go            -- prompt templates
  loop.go              -- Agent struct, NewAgent, CleanupDone, BootstrapWatchedPRs
  issues.go            -- ProcessNewIssues
  review.go            -- ProcessReviewComments
  ci.go                -- ProcessCIFailures
  conflicts.go         -- ProcessConflicts, conflict resolution
  triage.go            -- ProcessTriageJobs, periodic CI investigation
  git_ops.go           -- git push, amend, squash, rebase helpers
  loop_test.go         -- integration tests with all interfaces mocked
```

## Main Loop

```
STARTUP: parse config -> rebuild state from GitHub -> signal handler

LOOP (every poll-interval):
  1. Cleanup: for each active PR
     -> get PR state via GitHub API
     -> if MERGED/CLOSED: remove worktree, remove from state

  2. New Issues: list issues with label via GitHub API
     -> skip if already in state
     -> git fetch + git worktree add -b ai/issue-N
     -> invoke coding agent
     -> push branch, create PR via GitHub API
     -> update in-memory state

  3. Reactions: for each active PR in state
     -> review comments: fetch new comments, invoke agent
     -> CI failures: check runs, classify, fix or report
     -> conflicts: detect and resolve via rebase
     -> rebase: update base branch
```

## Dependencies

- `github.com/google/go-github/v88` -- GitHub API client (behind `GitHubClient` interface)
- Go standard library for everything else

## Error Strategy

The error strategy prioritizes human visibility over automatic recovery:

- **Agent failure**: Label issue `ai-failed`, post comment with error, skip
- **GitHub API failure**: Log and skip, retry on next poll cycle
- **Process restart**: State rebuilt from GitHub on startup
- **Self-reply prevention**: Bot comments tagged with `<!-- oompa-bot -->` markers
