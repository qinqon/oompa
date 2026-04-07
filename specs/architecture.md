# Architecture

Single long-running Go binary with a sequential polling loop.

- No webhooks (avoids inbound connectivity requirements)
- No goroutine-per-issue (keeps it simple and debuggable)
- All GitHub interaction via `github.com/google/go-github/v84` behind a `GitHubClient` interface
- All Claude interaction via `claude -p` CLI (headless mode) using Google Vertex AI as the backend
- Standalone `go.mod` with one external dependency: `github.com/google/go-github/v84`
- All external dependencies behind interfaces for testability

## Directory Structure

```
main.go           -- entry point, config, main loop
github.go         -- GitHubClient interface + go-github implementation
github_test.go    -- tests for GitHub client (httptest mock server)
claude.go         -- CommandRunner interface + Claude Code CLI invocation
claude_test.go    -- tests for Claude invocation (mock CommandRunner)
worktree.go       -- git worktree management
worktree_test.go  -- tests for worktree logic
state.go          -- JSON file state persistence
state_test.go     -- tests for state load/save
prompt.go         -- prompt templates
prompt_test.go    -- tests for prompt generation
loop.go           -- main loop logic (processNewIssues, processReviewComments, cleanupDone)
loop_test.go      -- integration tests with all interfaces mocked
go.mod            -- standalone module
```

## Main Loop

```
STARTUP: parse config -> load state from JSON -> signal handler

LOOP (every poll-interval):
  1. New Issues: list issues with label via go-github
     -> skip if already in state
     -> git fetch + git worktree add -b ai/issue-N
     -> claude -p "implement fix..."
     -> extract PR number from branch via go-github
     -> save state

  2. Review Comments: for each active PR in state
     -> list PR comments via go-github (filter ID > last seen)
     -> skip bot comments
     -> claude -p "address review comments..."
     -> update lastCommentID

  3. Cleanup: for each active PR
     -> get PR state via go-github
     -> if MERGED/CLOSED: remove worktree, remove from state
```
