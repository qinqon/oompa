# Worktree Management

## Interface

```go
type WorktreeManager interface {
    EnsureRepoCloned(ctx context.Context) error
    CreateWorktree(ctx context.Context, branchName string) (worktreePath string, err error)
    RemoveWorktree(ctx context.Context, worktreePath string) error
}
```

## Concrete Implementation

`GitWorktreeManager` uses `CommandRunner`:

- `EnsureRepoCloned` -- `git clone` or `git fetch origin`
- `CreateWorktree` -- `git worktree add -b ai/issue-N ... origin/main`
- `RemoveWorktree` -- `git worktree remove --force`

## Tests (`worktree_test.go`)

Mock `CommandRunner`:

- `TestCreateWorktree` -- verifies correct git args and returns expected path
- `TestRemoveWorktree` -- verifies `git worktree remove --force` is called
- `TestEnsureRepoCloned_AlreadyCloned` -- calls `git fetch` not `git clone`
- `TestEnsureRepoCloned_Fresh` -- calls `git clone`
