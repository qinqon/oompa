# Oompa: Automated GitHub Issue Resolution

## Context

Create a standalone Go program that runs on an external server, watches for GitHub issues labeled `good-for-ai`, spawns headless Claude Code to implement fixes, creates PRs, responds to review comments, and cleans up when PRs are merged/closed.

## Architecture

Single long-running Go binary with a sequential polling loop. No webhooks (avoids inbound connectivity requirements). No goroutine-per-issue (keeps it simple and debuggable). All GitHub interaction via `github.com/google/go-github/v84` behind a `GitHubClient` interface. All Claude interaction via `claude -p` CLI (headless mode) using Google Vertex AI as the backend.

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

Standalone `go.mod` with one external dependency: `github.com/google/go-github/v84` for type-safe GitHub API access. All GitHub interaction goes through a `GitHubClient` interface to enable unit testing with mocks.

## Main Loop

```
STARTUP: parse config → load state from JSON → signal handler

LOOP (every poll-interval):
  1. New Issues: list issues with label via go-github
     → skip if already in state
     → git fetch + git worktree add -b ai/issue-N
     → claude -p "implement fix..."
     → extract PR number from branch via go-github
     → save state

  2. Review Comments: for each active PR in state
     → list PR comments via go-github (filter ID > last seen)
     → skip bot comments
     → claude -p "address review comments..."
     → update lastCommentID

  3. Cleanup: for each active PR
     → get PR state via go-github
     → if MERGED/CLOSED: remove worktree, remove from state
```

## Key Files to Create

### `main.go`
- `Config` struct with fields: `Owner`, `Repo`, `Label`, `CloneDir`, `StatePath`, `PollInterval`, `VertexRegion`, `VertexProject`, `LogLevel`, `DryRun`
- Config from env vars (`OOMPA_*`) with flag overrides, read in `main()`
- `slog` for structured logging
- `signal.NotifyContext` for graceful shutdown
- Constructs concrete implementations of `GitHubClient`, `CommandRunner`, `WorktreeManager`
- Passes them to the loop functions (dependency injection — enables testing)

### `loop.go`
- `Agent` struct holds all dependencies:
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
- `(a *Agent) ProcessNewIssues(ctx)` → `(a *Agent) ProcessReviewComments(ctx)` → `(a *Agent) CleanupDone(ctx)`
- Main loop lives in `main.go`, calls these methods sequentially

### `state.go`
- JSON file at configurable path (default `~/.oompa-state.json`)
- Types:
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
- Load at startup, save after every mutation

### `github.go`
- Defines a `GitHubClient` interface for all GitHub operations (enables mocking in tests)
- Concrete implementation uses `github.com/google/go-github/v84` with token auth: `github.NewClient(nil).WithAuthToken(token)`
- Interface methods:
  ```go
  type GitHubClient interface {
      ListLabeledIssues(ctx context.Context, owner, repo, label string) ([]Issue, error)
      GetPRReviewComments(ctx context.Context, owner, repo string, prNumber int, sinceID int64) ([]ReviewComment, error)
      GetIssueComments(ctx context.Context, owner, repo string, issueNumber int, sinceID int64) ([]ReviewComment, error)
      GetPRState(ctx context.Context, owner, repo string, prNumber int) (string, error)
      AddIssueComment(ctx context.Context, owner, repo string, issueNumber int, body string) error
      AddLabel(ctx context.Context, owner, repo string, issueNumber int, label string) error
      RemoveLabel(ctx context.Context, owner, repo string, issueNumber int, label string) error
      ListPRsByHead(ctx context.Context, owner, repo, branch string) ([]PR, error)
  }
  ```
- go-github mapping:
  - `ListLabeledIssues` → `client.Issues.ListByRepo()` with `Labels: []string{label}, State: "open"`
  - `GetPRReviewComments` → `client.PullRequests.ListComments()`, filter by `comment.ID > sinceID`
  - `GetIssueComments` → `client.Issues.ListComments()`, filter by `comment.ID > sinceID`
  - `GetPRState` → `client.PullRequests.Get()`, return `pr.GetState()` + check `pr.GetMerged()`
  - `AddIssueComment` → `client.Issues.CreateComment()`
  - `AddLabel` → `client.Issues.AddLabelsToIssue()`
  - `RemoveLabel` → `client.Issues.RemoveLabelForIssue()`
  - `ListPRsByHead` → `client.PullRequests.List()` with `Head: branch`

### `claude.go`
- Defines a `CommandRunner` interface for executing external commands (enables mocking in tests):
  ```go
  type CommandRunner interface {
      Run(ctx context.Context, workDir string, name string, args ...string) (stdout []byte, stderr []byte, err error)
  }
  ```
- `ExecRunner` is the concrete implementation using `os/exec`
- `runClaude(ctx, runner CommandRunner, workDir, prompt string, cfg Config) (ClaudeResult, error)`:
  - Invokes: `claude -p --output-format json --dangerously-skip-permissions "prompt"`
  - Sets Vertex env vars on the command: `CLAUDE_CODE_USE_VERTEX=1`, `CLOUD_ML_REGION`, `ANTHROPIC_VERTEX_PROJECT_ID`
  - Parses JSON stdout into `ClaudeResult`

### `worktree.go`
- Defines a `WorktreeManager` interface:
  ```go
  type WorktreeManager interface {
      EnsureRepoCloned(ctx context.Context) error
      CreateWorktree(ctx context.Context, branchName string) (worktreePath string, err error)
      RemoveWorktree(ctx context.Context, worktreePath string) error
  }
  ```
- `GitWorktreeManager` is the concrete implementation using `CommandRunner`:
  - `EnsureRepoCloned` — `git clone` or `git fetch origin`
  - `CreateWorktree` — `git worktree add -b ai/issue-N ... origin/main`
  - `RemoveWorktree` — `git worktree remove --force`

### `prompt.go`
- `buildImplementationPrompt(issue Issue) string` — tells Claude to:
  - Read Claude.md for conventions
  - Implement the fix, run `make lint` and `make test`
  - Commit (no trailing period, 72 char body)
  - Create PR via `gh pr create` with `/kind`, `Fixes #N`, release-note block
- `buildReviewResponsePrompt(work IssueWork, comments []ReviewComment) string` — tells Claude to:
  - Address each review comment
  - Run lint/test, commit, push
  - No force-push

## Configuration

| Env Var | Flag | Default | Description |
|---------|------|---------|-------------|
| `OOMPA_OWNER` | `--owner` | `openperouter` | GitHub repo owner |
| `OOMPA_REPO` | `--repo` | `openperouter` | GitHub repo name |
| `OOMPA_LABEL` | `--label` | `good-for-ai` | Issue label to watch |
| `OOMPA_CLONE_DIR` | `--clone-dir` | `~/oompa-work` | Clone/worktree directory |
| `OOMPA_STATE_PATH` | `--state-path` | `~/.oompa-state.json` | State file |
| `OOMPA_POLL_INTERVAL` | `--poll-interval` | `2m` | Poll frequency |
| `GITHUB_TOKEN` | — | (required) | GitHub PAT for go-github client |
| `CLOUD_ML_REGION` | `--vertex-region` | (required) | GCP Vertex AI region (e.g. `us-east5`) |
| `ANTHROPIC_VERTEX_PROJECT_ID` | `--vertex-project` | (required) | GCP project ID for Vertex |

The server must have Google Cloud ADC configured (`gcloud auth application-default login` or a service account key). `CLAUDE_CODE_USE_VERTEX=1` is set automatically by the agent when invoking Claude. `GITHUB_TOKEN` is used by both the go-github client and passed to Claude for `gh pr create`.

## Error Handling

- **Claude failure**: Add `ai-failed` label to issue, comment with error, skip. Human removes label + re-adds `good-for-ai` to retry.
- **GitHub API failure**: Log and skip, retry on next poll cycle.
- **Process restart**: State file ensures pickup where left off. Worktrees persist on disk.
- **Infinite loop prevention**: Bot's own comments filtered out. No CI-failure auto-retry.

## Safety

- Claude never merges — only creates PRs
- No force-push in prompts
- `--dangerously-skip-permissions` is acceptable since this runs unattended on a trusted server
- Uses Vertex AI — billing goes through your GCP project, controlled by GCP IAM and quotas
- Sequential processing avoids race conditions

## Unit Testing Strategy

All external dependencies are behind interfaces (`GitHubClient`, `CommandRunner`, `WorktreeManager`), enabling full unit testing without real GitHub, Claude, or git.

### Test files and what they cover

**`state_test.go`** — State persistence
- `TestLoadState_Empty` — returns empty state when file doesn't exist
- `TestLoadState_Valid` — round-trip load/save with active issues
- `TestLoadState_Corrupt` — returns empty state on corrupt JSON
- `TestSaveState_CreatesFile` — creates file and parent dirs

**`prompt_test.go`** — Prompt generation
- `TestBuildImplementationPrompt` — verifies issue number, title, body are interpolated; `/kind` and `release-note` instructions present
- `TestBuildReviewResponsePrompt` — verifies each comment's file/line/body is included

**`github_test.go`** — GitHub client (using `net/http/httptest`)
- Spin up an `httptest.Server` that returns canned JSON responses
- Point go-github client at the test server: `client, _ := github.NewClient(nil).WithAuthToken("test")` + override BaseURL
- `TestListLabeledIssues` — returns issues matching label
- `TestGetPRReviewComments_FiltersBySinceID` — only returns comments with ID > sinceID
- `TestGetPRState_Merged` / `_Closed` / `_Open`
- `TestAddIssueComment` — verifies request body
- `TestAddLabel` / `TestRemoveLabel`
- `TestListPRsByHead` — filters by branch

**`claude_test.go`** — Claude invocation (mock `CommandRunner`)
- `TestRunClaude_Success` — mock runner returns valid JSON, verify parsed result
- `TestRunClaude_Failure` — mock runner returns error, verify error wrapping
- `TestRunClaude_VertexEnvVars` — verify the correct env vars are passed to the command
- `TestRunClaude_InvalidJSON` — mock returns non-JSON stdout, verify error

**`worktree_test.go`** — Worktree management (mock `CommandRunner`)
- `TestCreateWorktree` — verifies correct git args and returns expected path
- `TestRemoveWorktree` — verifies `git worktree remove --force` is called
- `TestEnsureRepoCloned_AlreadyCloned` — calls `git fetch` not `git clone`
- `TestEnsureRepoCloned_Fresh` — calls `git clone`

**`loop_test.go`** — Main loop integration (all interfaces mocked)
- `TestProcessNewIssues_SkipsAlreadyTracked` — issue in state is not re-processed
- `TestProcessNewIssues_HappyPath` — creates worktree, runs claude, extracts PR, updates state
- `TestProcessNewIssues_ClaudeFailure` — adds `ai-failed` label, comments on issue
- `TestProcessReviewComments_NoNewComments` — no action taken
- `TestProcessReviewComments_AddressesHumanComments` — runs claude, updates lastCommentID
- `TestProcessReviewComments_SkipsBotComments` — filters out bot's own comments
- `TestCleanupDone_MergedPR` — removes worktree, deletes from state
- `TestCleanupDone_ClosedPR` — removes worktree, deletes from state
- `TestCleanupDone_OpenPR` — no action

### Mock types (in test files, not exported)

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
    // errors to inject
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

### Running tests

```bash
go test ./...
```

## Verification

1. Build: `go build -o oompa .`
2. Dry run: `./oompa --dry-run --poll-interval 10s` — logs what it would do without executing
3. Test with a trivial issue (e.g., "Fix typo in comment") labeled `good-for-ai`
4. Verify PR is created with correct format
5. Post a review comment, verify Claude responds and pushes
6. Merge the PR, verify worktree cleanup
