# State Management

JSON file at configurable path (default `~/.ai-agent-state.json`).

## Types

```go
type State struct {
    ActiveIssues map[int]*IssueWork `json:"activeIssues"`
}

type IssueWork struct {
    IssueNumber   int       `json:"issueNumber"`
    IssueTitle    string    `json:"issueTitle"`
    WorktreePath  string    `json:"worktreePath"`
    BranchName    string    `json:"branchName"`
    PRNumber      int       `json:"prNumber"`
    LastCommentID int64     `json:"lastCommentID"`
    Status        string    `json:"status"` // implementing, pr-open, failed, done
    CreatedAt     time.Time `json:"createdAt"`
}
```

## Behavior

- Load at startup
- Save after every mutation

## Tests (`state_test.go`)

- `TestLoadState_Empty` -- returns empty state when file doesn't exist
- `TestLoadState_Valid` -- round-trip load/save with active issues
- `TestLoadState_Corrupt` -- returns empty state on corrupt JSON
- `TestSaveState_CreatesFile` -- creates file and parent dirs
