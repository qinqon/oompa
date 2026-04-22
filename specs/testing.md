# Testing Strategy

All external dependencies are behind interfaces (`GitHubClient`, `CommandRunner`, `WorktreeManager`), enabling full unit testing without real GitHub, Claude, or git.

## Mock Types (in test files, not exported)

```go
type mockGitHubClient struct {
    issues          []Issue
    prComments      []ReviewComment
    issueComments   []ReviewComment
    prState         string
    prs             []PR
    addedComments   []string
    addedLabels     []string
    removedLabels   []string
    addedReactions  []string
    listIssuesErr   error
    // ...
}

type mockCommandRunner struct {
    calls  []commandCall // records all calls for assertions
    stdout []byte
    stderr []byte
    err    error
}

type mockWorktreeManager struct {
    createdBranches  []string
    removedPaths     []string
    cloneCalled      bool
    createErr        error
    // ...
}
```

## Running Tests

```bash
go test ./...
```

## Verification Checklist

1. Build: `go build -o oompa ./cmd/oompa`
2. Dry run: `./oompa --dry-run --poll-interval 10s` -- logs what it would do without executing
3. Test with a trivial issue (e.g., "Fix typo in comment") labeled `good-for-ai`
4. Verify PR is created with correct format
5. Post a review comment, verify Claude responds and pushes
6. Merge the PR, verify worktree cleanup
