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

- `(a *Agent) CleanupDone(ctx)` -- remove worktrees for merged/closed PRs
- `(a *Agent) ProcessNewIssues(ctx)` -- find labeled issues, create worktrees, run Claude, push branch, create PR via GitHub API
- `(a *Agent) ProcessReviewComments(ctx)` -- check for new review comments, run Claude to address them
- `(a *Agent) isAllowedReviewer(user string) bool` -- checks if user is in reviewers whitelist (empty = allow all)
- `(a *Agent) HasWatchedPRs() bool` -- returns true if `cfg.WatchPRs` is non-empty
- `(a *Agent) ShouldRunReaction(reaction string) bool` -- returns true if `cfg.Reactions` is empty (all enabled) or contains the given reaction name
- `(a *Agent) BootstrapWatchedPRs(ctx)` -- creates IssueWork entries for directly-specified PR numbers (calls `GetPR` to fetch details, skips merged/closed/already-tracked)

Main loop lives in `cmd/oompa/main.go`, calls these methods sequentially. CleanupDone runs first so that closed/merged PRs are removed from state before ProcessNewIssues checks for new work.

### Watch mode
When `--watch-prs` is set, `BootstrapWatchedPRs` runs instead of `ProcessNewIssues`. The `--reactions` flag controls which processing phases run (reviews, ci, conflicts). Both flags can be used independently or together.

### ProcessNewIssues behavior
- Skips issues already in state (unless `prNumber == 0` and status is `implementing`, in which case it re-checks for the PR)
- Cleans up stale worktrees/branches before creating new ones
- After Claude finishes, the agent pushes the branch and creates the PR (Claude does NOT push or create PRs)

### ProcessReviewComments behavior
- Filters comments through the reviewers whitelist
- Adds :eyes: reaction to each comment before invoking Claude
- Claude uses judgment: implements valid suggestions and pushes back on bad ones
- Always replies to every comment

## Tests (`loop_test.go`)

All interfaces mocked:

- `TestProcessNewIssues_SkipsAlreadyTracked` -- issue in state is not re-processed
- `TestProcessNewIssues_HappyPath` -- creates worktree, runs claude, agent pushes and creates PR, updates state
- `TestProcessNewIssues_ClaudeFailure` -- adds `ai-failed` label, comments on issue
- `TestProcessReviewComments_NoNewComments` -- no action taken
- `TestProcessReviewComments_AddressesHumanComments` -- runs claude, updates lastCommentID
- `TestProcessNewIssues_RechecksForPR` -- re-checks for PR when prNumber is 0
- `TestProcessReviewComments_SkipsNonWhitelistedUsers` -- skips comments from users not in whitelist
- `TestProcessReviewComments_AllowsAllWhenWhitelistEmpty` -- allows all when whitelist is empty
- `TestCleanupDone_MergedPR` -- removes worktree, deletes from state
- `TestCleanupDone_ClosedPR` -- removes worktree, deletes from state
- `TestCleanupDone_OpenPR` -- no action
- `TestShouldRunReaction_EmptyAllowsAll` -- empty Reactions allows all reaction types
- `TestShouldRunReaction_Filtered` -- only configured reactions are allowed
- `TestBootstrapWatchedPRs_HappyPath` -- creates IssueWork entries for open watched PRs
- `TestBootstrapWatchedPRs_SkipsClosedPR` -- does not track merged/closed PRs
- `TestBootstrapWatchedPRs_SkipsAlreadyTracked` -- does not duplicate existing entries
- `TestHasWatchedPRs` -- returns false when empty, true when configured
