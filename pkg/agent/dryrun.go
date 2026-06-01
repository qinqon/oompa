package agent

import (
	"context"
	"log/slog"
	"time"
)

// DryRunGitHubClient wraps a GitHubClient and logs write operations
// instead of executing them. Read operations pass through to the inner client.
type DryRunGitHubClient struct {
	inner  GitHubClient
	logger *slog.Logger
}

// NewDryRunGitHubClient wraps a GitHubClient for dry-run mode.
func NewDryRunGitHubClient(inner GitHubClient, logger *slog.Logger) *DryRunGitHubClient {
	return &DryRunGitHubClient{inner: inner, logger: logger}
}

// Write operations — log and skip

func (d *DryRunGitHubClient) AddIssueComment(_ context.Context, _, _ string, issueNumber int, body string) error {
	d.logger.Info("[dry-run] would add comment", "issue", issueNumber, "body", truncateString(body, 100))
	return nil
}

func (d *DryRunGitHubClient) AddLabel(_ context.Context, _, _ string, issueNumber int, label string) error {
	d.logger.Info("[dry-run] would add label", "issue", issueNumber, "label", label)
	return nil
}

func (d *DryRunGitHubClient) RemoveLabel(_ context.Context, _, _ string, issueNumber int, label string) error {
	d.logger.Info("[dry-run] would remove label", "issue", issueNumber, "label", label)
	return nil
}

func (d *DryRunGitHubClient) AssignIssue(_ context.Context, _, _ string, issueNumber int, user string) error {
	d.logger.Info("[dry-run] would assign issue", "issue", issueNumber, "user", user)
	return nil
}

func (d *DryRunGitHubClient) UnassignIssue(_ context.Context, _, _ string, issueNumber int, user string) error {
	d.logger.Info("[dry-run] would unassign issue", "issue", issueNumber, "user", user)
	return nil
}

func (d *DryRunGitHubClient) CreatePR(_ context.Context, _, _, title, _, _, _ string) (int, error) {
	d.logger.Info("[dry-run] would create PR", "title", title)
	return 0, nil
}

func (d *DryRunGitHubClient) CreateIssue(_ context.Context, _, _, title, _ string, labels []string) (int, error) {
	d.logger.Info("[dry-run] would create issue", "title", title, "labels", labels)
	return 0, nil
}

func (d *DryRunGitHubClient) AddPRCommentReaction(_ context.Context, _, _ string, commentID int64, reaction string) error {
	d.logger.Info("[dry-run] would add reaction", "comment", commentID, "reaction", reaction)
	return nil
}

func (d *DryRunGitHubClient) ReplyToPRComment(_ context.Context, _, _ string, prNumber int, commentID int64, body string) error {
	d.logger.Info("[dry-run] would reply to comment", "pr", prNumber, "comment", commentID, "body", truncateString(body, 100))
	return nil
}

// Read operations — pass through

func (d *DryRunGitHubClient) ListLabeledIssues(ctx context.Context, owner, repo, label string) ([]Issue, error) {
	return d.inner.ListLabeledIssues(ctx, owner, repo, label)
}

func (d *DryRunGitHubClient) GetPRReviewComments(ctx context.Context, owner, repo string, prNumber int, sinceID int64) ([]ReviewComment, error) {
	return d.inner.GetPRReviewComments(ctx, owner, repo, prNumber, sinceID)
}

func (d *DryRunGitHubClient) GetIssueComments(ctx context.Context, owner, repo string, issueNumber int, sinceID int64) ([]ReviewComment, error) {
	return d.inner.GetIssueComments(ctx, owner, repo, issueNumber, sinceID)
}

func (d *DryRunGitHubClient) GetPRState(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	return d.inner.GetPRState(ctx, owner, repo, prNumber)
}

func (d *DryRunGitHubClient) ListPRsByHead(ctx context.Context, owner, repo, headOwner, branch string) ([]PR, error) {
	return d.inner.ListPRsByHead(ctx, owner, repo, headOwner, branch)
}

func (d *DryRunGitHubClient) GetCheckRuns(ctx context.Context, owner, repo, ref string) ([]CheckRun, error) {
	return d.inner.GetCheckRuns(ctx, owner, repo, ref)
}

func (d *DryRunGitHubClient) GetCheckRunLog(ctx context.Context, owner, repo string, checkRunID int64) (string, error) {
	return d.inner.GetCheckRunLog(ctx, owner, repo, checkRunID)
}

func (d *DryRunGitHubClient) GetPRHeadSHA(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	return d.inner.GetPRHeadSHA(ctx, owner, repo, prNumber)
}

func (d *DryRunGitHubClient) HasPRCommentReaction(ctx context.Context, owner, repo string, commentID int64, reaction, user string) (bool, error) {
	return d.inner.HasPRCommentReaction(ctx, owner, repo, commentID, reaction, user)
}

func (d *DryRunGitHubClient) GetPRMergeable(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	return d.inner.GetPRMergeable(ctx, owner, repo, prNumber)
}

func (d *DryRunGitHubClient) GetPRReviews(ctx context.Context, owner, repo string, prNumber int, sinceID int64) ([]PRReview, error) {
	return d.inner.GetPRReviews(ctx, owner, repo, prNumber, sinceID)
}

func (d *DryRunGitHubClient) GetPRHeadCommitDate(ctx context.Context, owner, repo string, prNumber int) (time.Time, error) {
	return d.inner.GetPRHeadCommitDate(ctx, owner, repo, prNumber)
}

func (d *DryRunGitHubClient) HasLinkedPR(ctx context.Context, owner, repo string, issueNumber int) (bool, error) {
	return d.inner.HasLinkedPR(ctx, owner, repo, issueNumber)
}

func (d *DryRunGitHubClient) GetPR(ctx context.Context, owner, repo string, prNumber int) (PR, error) {
	return d.inner.GetPR(ctx, owner, repo, prNumber)
}

func (d *DryRunGitHubClient) IsPRBehind(ctx context.Context, owner, repo string, prNumber int) (bool, error) {
	return d.inner.IsPRBehind(ctx, owner, repo, prNumber)
}

func (d *DryRunGitHubClient) SearchIssues(ctx context.Context, query string) ([]Issue, error) {
	return d.inner.SearchIssues(ctx, query)
}

func (d *DryRunGitHubClient) GetCommitStatuses(ctx context.Context, owner, repo, ref string) ([]CheckRun, error) {
	return d.inner.GetCommitStatuses(ctx, owner, repo, ref)
}

func (d *DryRunGitHubClient) ListWorkflowRuns(ctx context.Context, owner, repo, workflowID, status string, limit int) ([]WorkflowRun, error) {
	return d.inner.ListWorkflowRuns(ctx, owner, repo, workflowID, status, limit)
}

func (d *DryRunGitHubClient) ListWorkflowJobs(ctx context.Context, owner, repo string, runID int64) ([]WorkflowJob, error) {
	return d.inner.ListWorkflowJobs(ctx, owner, repo, runID)
}

func (d *DryRunGitHubClient) GetWorkflowJobLogs(ctx context.Context, owner, repo string, jobID int64) (string, error) {
	return d.inner.GetWorkflowJobLogs(ctx, owner, repo, jobID)
}

func (d *DryRunGitHubClient) CountCommitsSince(ctx context.Context, owner, repo string, since time.Time) (int, error) {
	return d.inner.CountCommitsSince(ctx, owner, repo, since)
}


