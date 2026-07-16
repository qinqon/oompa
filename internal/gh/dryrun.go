package gh

import (
	"context"
	"log/slog"
	"time"
)

// DryRunClient wraps a Client and logs write operations
// instead of executing them. Read operations pass through to the inner client.
type DryRunClient struct {
	inner  Client
	logger *slog.Logger
}

// NewDryRunClient wraps a Client for dry-run mode.
func NewDryRunClient(inner Client, logger *slog.Logger) *DryRunClient {
	return &DryRunClient{inner: inner, logger: logger}
}

// Write operations — log and skip

func (d *DryRunClient) AddIssueComment(_ context.Context, _, _ string, issueNumber int, body string) error {
	d.logger.Info("[dry-run] would add comment", "issue", issueNumber, "body", truncate(body, 100))
	return nil
}

func (d *DryRunClient) AddLabel(_ context.Context, _, _ string, issueNumber int, label string) error {
	d.logger.Info("[dry-run] would add label", "issue", issueNumber, "label", label)
	return nil
}

func (d *DryRunClient) AssignIssue(_ context.Context, _, _ string, issueNumber int, user string) error {
	d.logger.Info("[dry-run] would assign issue", "issue", issueNumber, "user", user)
	return nil
}

func (d *DryRunClient) UnassignIssue(_ context.Context, _, _ string, issueNumber int, user string) error {
	d.logger.Info("[dry-run] would unassign issue", "issue", issueNumber, "user", user)
	return nil
}

func (d *DryRunClient) CreatePR(_ context.Context, _, _, title, _, _, _ string) (int, error) {
	d.logger.Info("[dry-run] would create PR", "title", title)
	return 0, nil
}

func (d *DryRunClient) CreateIssue(_ context.Context, _, _, title, _ string, labels []string) (int, error) {
	d.logger.Info("[dry-run] would create issue", "title", title, "labels", labels)
	return 0, nil
}

func (d *DryRunClient) AddPRCommentReaction(_ context.Context, _, _ string, commentID int64, reaction string) error {
	d.logger.Info("[dry-run] would add reaction", "comment", commentID, "reaction", reaction)
	return nil
}

func (d *DryRunClient) AddIssueCommentReaction(_ context.Context, _, _ string, commentID int64, reaction string) error {
	d.logger.Info("[dry-run] would add issue comment reaction", "comment", commentID, "reaction", reaction)
	return nil
}

// Read operations — pass through

func (d *DryRunClient) ListLabeledIssues(ctx context.Context, owner, repo, label string) ([]Issue, error) {
	return d.inner.ListLabeledIssues(ctx, owner, repo, label)
}

func (d *DryRunClient) GetPRReviewComments(ctx context.Context, owner, repo string, prNumber int, sinceID int64) ([]ReviewComment, error) {
	return d.inner.GetPRReviewComments(ctx, owner, repo, prNumber, sinceID)
}

func (d *DryRunClient) GetIssueComments(ctx context.Context, owner, repo string, issueNumber int, sinceID int64) ([]ReviewComment, error) {
	return d.inner.GetIssueComments(ctx, owner, repo, issueNumber, sinceID)
}

func (d *DryRunClient) GetPRState(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	return d.inner.GetPRState(ctx, owner, repo, prNumber)
}

func (d *DryRunClient) ListPRsByHead(ctx context.Context, owner, repo, headOwner, branch string) ([]PR, error) {
	return d.inner.ListPRsByHead(ctx, owner, repo, headOwner, branch)
}

func (d *DryRunClient) GetCheckRuns(ctx context.Context, owner, repo, ref string) ([]CheckRun, error) {
	return d.inner.GetCheckRuns(ctx, owner, repo, ref)
}

func (d *DryRunClient) GetCheckRunLog(ctx context.Context, owner, repo string, checkRunID int64) (string, error) {
	return d.inner.GetCheckRunLog(ctx, owner, repo, checkRunID)
}

func (d *DryRunClient) GetPRHeadSHA(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	return d.inner.GetPRHeadSHA(ctx, owner, repo, prNumber)
}

func (d *DryRunClient) HasPRCommentReaction(ctx context.Context, owner, repo string, commentID int64, reaction, user string) (bool, error) {
	return d.inner.HasPRCommentReaction(ctx, owner, repo, commentID, reaction, user)
}

func (d *DryRunClient) GetPRMergeable(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	return d.inner.GetPRMergeable(ctx, owner, repo, prNumber)
}

func (d *DryRunClient) GetPRReviews(ctx context.Context, owner, repo string, prNumber int, sinceID int64) ([]PRReview, error) {
	return d.inner.GetPRReviews(ctx, owner, repo, prNumber, sinceID)
}

func (d *DryRunClient) GetPRHeadCommitDate(ctx context.Context, owner, repo string, prNumber int) (time.Time, error) {
	return d.inner.GetPRHeadCommitDate(ctx, owner, repo, prNumber)
}

func (d *DryRunClient) HasLinkedPR(ctx context.Context, owner, repo string, issueNumber int) (bool, error) {
	return d.inner.HasLinkedPR(ctx, owner, repo, issueNumber)
}

func (d *DryRunClient) GetPR(ctx context.Context, owner, repo string, prNumber int) (PR, error) {
	return d.inner.GetPR(ctx, owner, repo, prNumber)
}

func (d *DryRunClient) IsPRBehind(ctx context.Context, owner, repo string, prNumber int) (bool, error) {
	return d.inner.IsPRBehind(ctx, owner, repo, prNumber)
}

func (d *DryRunClient) SearchIssues(ctx context.Context, query string) ([]Issue, error) {
	return d.inner.SearchIssues(ctx, query)
}

func (d *DryRunClient) GetCommitStatuses(ctx context.Context, owner, repo, ref string) ([]CheckRun, error) {
	return d.inner.GetCommitStatuses(ctx, owner, repo, ref)
}

func (d *DryRunClient) ListWorkflowRuns(ctx context.Context, owner, repo, workflowID, status string, limit int, since time.Time) ([]WorkflowRun, error) {
	return d.inner.ListWorkflowRuns(ctx, owner, repo, workflowID, status, limit, since)
}

func (d *DryRunClient) ListWorkflowJobs(ctx context.Context, owner, repo string, runID int64) ([]WorkflowJob, error) {
	return d.inner.ListWorkflowJobs(ctx, owner, repo, runID)
}

func (d *DryRunClient) GetWorkflowJobLogs(ctx context.Context, owner, repo string, jobID int64) (string, error) {
	return d.inner.GetWorkflowJobLogs(ctx, owner, repo, jobID)
}

func (d *DryRunClient) CountCommitsSince(ctx context.Context, owner, repo string, since time.Time) (int, error) {
	return d.inner.CountCommitsSince(ctx, owner, repo, since)
}

// truncate shortens s to at most maxLen runes for log previews.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
