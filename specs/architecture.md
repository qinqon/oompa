# Architecture

Single long-running Go binary with a sequential polling loop.

## Design Decisions

- **Polling, not webhooks**: Avoids inbound connectivity requirements, NAT traversal, and webhook secret management. The agent runs behind firewalls and on developer machines without exposing ports. The trade-off is higher latency (bounded by `--poll-interval`) which is acceptable for code maintenance tasks that tolerate minutes of delay.
- **Sequential, not parallel**: One issue at a time prevents race conditions on shared git state (worktrees, branches). A goroutine-per-issue model would require locking around git operations and make debugging harder. The `--max-workers` flag allows opt-in parallelism when the user accepts the complexity.
- **Interfaces over mocks**: All external dependencies (GitHub API, Claude CLI, git) are behind Go interfaces. This enables deterministic unit tests without credentials, network access, or subprocess execution. Tests verify business logic, not integration plumbing.
- **Stateless on disk**: The agent rebuilds state from GitHub on every startup by scanning labeled issues and matching PRs. This eliminates state file corruption, migration, and backup concerns. The trade-off is a small startup cost from API calls.
- **CLI subprocess, not SDK**: Claude is invoked via the `claude -p` CLI rather than an SDK. This decouples the agent from Claude's release cycle and authentication flow, and allows swapping to OpenCode or other CLI agents via `--agent`.

## Invariants

- Claude never merges -- it only creates or updates PRs. A human must approve and merge.
- Force-pushes use `--force-with-lease` only for history-rewriting operations (rebase, squash) to maintain clean commit history.
- Bot-posted comments are tagged with `<!-- oompa-bot -->` markers to prevent self-reply loops.

## Constraints

- All GitHub interaction via `github.com/google/go-github/v84` behind a `GitHubClient` interface
- All Claude interaction via `claude -p` CLI (headless mode) using Google Vertex AI as the backend
- Standalone `go.mod` with one external dependency: `github.com/google/go-github/v84`
- All external dependencies behind interfaces for testability

## Directory Structure

```
cmd/oompa/
  main.go              -- entry point, config parsing, subcommand dispatch, polling loop
  status.go            -- `oompa status` subcommand (print-and-exit snapshot)
  tui.go               -- `oompa tui` subcommand (live bubbletea dashboard)
pkg/agent/
  types.go             -- shared types (Issue, ReviewComment, PR, ClaudeResult, IssueWork)
  config.go            -- Config struct
  event.go             -- EventEmitter interface, Event/WorkerState types, NoopEmitter, RingBuffer
  event_test.go        -- tests for event model and ring buffer
  eventserver.go       -- SocketEventServer (Unix socket server, client registry, broadcasting)
  eventserver_test.go  -- tests for socket server
  eventclient.go       -- EventClient (connects to daemon socket for status/tui)
  github.go            -- GitHubClient interface + go-github implementation
  github_test.go       -- tests for GitHub client (httptest mock server)
  claude.go            -- CommandRunner interface + Claude Code CLI invocation
  claude_test.go       -- tests for Claude invocation (mock CommandRunner)
  worktree.go          -- git worktree management
  worktree_test.go     -- tests for worktree logic
  state.go             -- JSON file state persistence
  state_test.go        -- tests for state load/save
  prompt.go            -- prompt templates
  prompt_test.go       -- tests for prompt generation
  loop.go              -- Agent struct, NewAgent, CleanupDone, BootstrapWatchedPRs, helpers
  issues.go            -- ProcessNewIssues
  review.go            -- ProcessReviewComments
  ci.go                -- ProcessCIFailures
  conflicts.go         -- ProcessConflicts, ProcessRebase, conflict resolution
  triage.go            -- ProcessTriageJobs, periodic CI investigation
  git_ops.go           -- git push, amend, squash, rebase helpers
  loop_test.go         -- integration tests with all interfaces mocked
go.mod                 -- standalone module
```

## Main Loop

```
STARTUP: parse config -> load state from JSON -> signal handler

LOOP (every poll-interval):
  1. Cleanup: for each active PR
     -> get PR state via go-github
     -> if MERGED/CLOSED: remove worktree, remove from state

  2. New Issues: list issues with label via go-github
     -> skip if already in state
     -> git fetch + git worktree add -b ai/issue-N
     -> claude -p "implement fix..."
     -> extract PR number from branch via go-github
     -> save state

  3. Review Comments: for each active PR in state
     -> list PR comments via go-github (filter ID > last seen)
     -> skip bot comments
     -> claude -p "address review comments..."
     -> update lastCommentID
```
