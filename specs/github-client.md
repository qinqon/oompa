# GitHub Client

Defines a `GitHubClient` interface for all GitHub operations (enables mocking in tests). Concrete implementation uses `github.com/google/go-github/v84` with token auth: `github.NewClient(nil).WithAuthToken(token)`.

## Interface

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
    AddPRCommentReaction(ctx context.Context, owner, repo string, commentID int64, reaction string) error
    GetPR(ctx context.Context, owner, repo string, prNumber int) (PR, error)
}
```

## go-github Mapping

| Method | go-github Call |
|--------|---------------|
| `ListLabeledIssues` | `client.Issues.ListByRepo()` with `Labels: []string{label}, State: "open"` |
| `GetPRReviewComments` | `client.PullRequests.ListComments()`, filter by `comment.ID > sinceID` |
| `GetIssueComments` | `client.Issues.ListComments()`, filter by `comment.ID > sinceID` |
| `GetPRState` | `client.PullRequests.Get()`, return `pr.GetState()` + check `pr.GetMerged()` |
| `AddIssueComment` | `client.Issues.CreateComment()` |
| `AddLabel` | `client.Issues.AddLabelsToIssue()` |
| `RemoveLabel` | `client.Issues.RemoveLabelForIssue()` |
| `ListPRsByHead` | `client.PullRequests.List()` with `Head: branch` (no owner prefix — works for same-repo and fork PRs) |
| `AddPRCommentReaction` | `client.Reactions.CreatePullRequestCommentReaction()` |
| `GetPR` | `client.PullRequests.Get()`, returns `PR{Number, Title, State, Merged, Head}` |
| `GetAuthenticatedUser` | `client.Users.Get("")`, returns name + email with login/noreply fallbacks |
| `CreateIssue` | `client.Issues.Create()`, creates a new issue with title, body, and labels |

## Additional Methods (on concrete type, not interface)

```go
func (g *GoGitHubClient) GetAuthenticatedUser(ctx context.Context) (name, email string, err error)
```

Used at startup to default the `--signed-off-by` value.

## Tests (`github_test.go`)

Use `net/http/httptest` with canned JSON responses. Point go-github client at test server.

- `TestListLabeledIssues` -- returns issues matching label
- `TestGetPRReviewComments_FiltersBySinceID` -- only returns comments with ID > sinceID
- `TestGetPRState_Merged` / `_Closed` / `_Open`
- `TestAddIssueComment` -- verifies request body
- `TestAddLabel` / `TestRemoveLabel`
- `TestListPRsByHead` -- filters by branch
- `TestGetAuthenticatedUser_WithNameAndEmail` -- returns name and email
- `TestGetAuthenticatedUser_FallbackToLogin` -- falls back to login and noreply email
- `TestCreateIssue` -- creates an issue and returns its number
