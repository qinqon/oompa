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
    ListPRsByHead(ctx context.Context, owner, repo, headOwner, branch string) ([]PR, error)
    HasLinkedPR(ctx context.Context, owner, repo string, issueNumber int) (bool, error)
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
| `ListPRsByHead` | `client.PullRequests.List()` with `Head: headOwner:branch` (falls back to bare `branch` when the fork repo name differs from the base repo). See "Duplicate-PR guard queries" below. |
| `HasLinkedPR` | `client.Issues.ListIssueTimeline()`, true when any `cross-referenced` event points to an **open** PR. See "Duplicate-PR guard queries" below. |
| `AddPRCommentReaction` | `client.Reactions.CreatePullRequestCommentReaction()` |
| `GetPR` | `client.PullRequests.Get()`, returns `PR{Number, Title, State, Merged, Head}` |
| `GetAuthenticatedUser` | `client.Users.Get("")`, returns name + email with login/noreply fallbacks |
| `CreateIssue` | `client.Issues.Create()`, creates a new issue with title, body, and labels |

## Duplicate-PR Guard Queries

`ListPRsByHead` and `HasLinkedPR` are the guards that prevent the issue fixer
from opening duplicate PRs (issue #264), so both must see the *complete*
picture, not just the first API page:

- `ListPRsByHead` cannot trust GitHub's `head` filter, which misbehaves in two
  distinct ways: the `owner:branch` form matches nothing when the fork repo
  has a different name than the base repo (hence the bare-`branch` fallback),
  and the bare form may be ignored entirely (e.g. when the branch cannot be
  resolved on the upstream repo), returning the repo's unfiltered PR list.
  Results are therefore always verified client-side (`head.ref == branch`)
  and queried per state, sorted `updated`/`desc`:
  - `state=open`: paginated fully (`PerPage: 100`) — the open set is small on
    any repo. A match here short-circuits the closed scan, because an open PR
    already decides the caller's outcome.
  - `state=closed`: paginated up to `maxClosedPRPages` (5) pages — sorting by
    most recent update keeps recently merged or rejected fix PRs inside the
    window regardless of when they were created, without walking thousands of
    historical PRs on every poll.
  - `Merged` is derived from `merged_at != nil` because the *list* endpoint
    never populates the `merged` boolean (only the *get* endpoint does).
- `HasLinkedPR` paginates the issue timeline fully (`PerPage: 100`). Every
  label, assignment, comment and rename is a timeline event, so
  `cross-referenced` events easily land beyond the first page on active
  issues. Returns early on the first open cross-referenced PR.

## Additional Methods (on concrete type, not interface)

```go
func (g *GoGitHubClient) GetAuthenticatedUser(ctx context.Context) (name, email string, err error)
```

Used at startup to default the `--signed-off-by` value.

## ETag Caching

Both constructors wrap the underlying HTTP transport with `CachingTransport` (see [specs/etag.md](etag.md)). This transparently adds `If-None-Match` headers to GET requests and serves cached responses on `304 Not Modified`, which do not count against GitHub's rate limit.

## Tests (`github_test.go`)

Use `net/http/httptest` with canned JSON responses. Point go-github client at test server.

- `TestListLabeledIssues` -- returns issues matching label
- `TestGetPRReviewComments_FiltersBySinceID` -- only returns comments with ID > sinceID
- `TestGetPRState_Merged` / `_Closed` / `_Open`
- `TestAddIssueComment` -- verifies request body
- `TestAddLabel` / `TestRemoveLabel`
- `TestListPRsByHead` -- filters by branch
- `TestListPRsByHead_PaginatesOpenPRs` -- follows Link headers across open-PR pages
- `TestListPRsByHead_FindsClosedPRAcrossPages` -- finds a merged PR (via `merged_at`) beyond page 1 of the closed scan
- `TestListPRsByHead_CapsClosedPRScan` -- stops after `maxClosedPRPages` pages of closed PRs
- `TestHasLinkedPR_FindsOpenPR` -- true for an open cross-referenced PR
- `TestHasLinkedPR_IgnoresClosedPR` -- false when the only cross-referenced PR is closed
- `TestHasLinkedPR_NoLinkedPRs` -- false when no cross-references exist
- `TestHasLinkedPR_PaginatesTimeline` -- finds a cross-referenced PR beyond the first timeline page
- `TestGetAuthenticatedUser_WithNameAndEmail` -- returns name and email
- `TestGetAuthenticatedUser_FallbackToLogin` -- falls back to login and noreply email
- `TestCreateIssue` -- creates an issue and returns its number
