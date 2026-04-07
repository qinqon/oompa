# Main Loop

## Agent Struct

```go
type Agent struct {
    gh        GitHubClient
    runner    CommandRunner
    worktrees WorktreeManager
    state     *State
    cfg       Config
    logger    *slog.Logger
}
```

## Methods

- `(a *Agent) ProcessNewIssues(ctx)` -- find labeled issues, create worktrees, run Claude, create PRs
- `(a *Agent) ProcessReviewComments(ctx)` -- check for new review comments, run Claude to address them
- `(a *Agent) CleanupDone(ctx)` -- remove worktrees for merged/closed PRs

Main loop lives in `main.go`, calls these methods sequentially.

## Tests (`loop_test.go`)

All interfaces mocked:

- `TestProcessNewIssues_SkipsAlreadyTracked` -- issue in state is not re-processed
- `TestProcessNewIssues_HappyPath` -- creates worktree, runs claude, extracts PR, updates state
- `TestProcessNewIssues_ClaudeFailure` -- adds `ai-failed` label, comments on issue
- `TestProcessReviewComments_NoNewComments` -- no action taken
- `TestProcessReviewComments_AddressesHumanComments` -- runs claude, updates lastCommentID
- `TestProcessReviewComments_SkipsBotComments` -- filters out bot's own comments
- `TestCleanupDone_MergedPR` -- removes worktree, deletes from state
- `TestCleanupDone_ClosedPR` -- removes worktree, deletes from state
- `TestCleanupDone_OpenPR` -- no action
