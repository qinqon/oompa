package gh

import "time"

// Issue represents a GitHub issue.
type Issue struct {
	Number    int
	Title     string
	Body      string
	Labels    []string
	Assignees []string
}

// ReviewComment represents a comment on a PR or issue.
type ReviewComment struct {
	ID          int64
	InReplyToID int64
	User        string
	Body        string
	Path        string
	Line        int
	CreatedAt   time.Time
}

// PR represents a GitHub pull request.
type PR struct {
	Number int
	Title  string
	State  string
	Merged bool
	Head   string
}

// PRReview represents a GitHub pull request review (approve, request changes, comment).
type PRReview struct {
	ID          int64
	User        string
	State       string // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING
	Body        string
	SubmittedAt time.Time
}

// CheckRun represents a GitHub Actions check run or a commit status entry.
// For check runs: ID is the check-run/job ID, Output contains log text.
// For commit statuses (e.g. Prow): ID is 0, Output contains the target_url.
// Callers must check ID != 0 before calling GetCheckRunLog.
type CheckRun struct {
	ID          int64
	Name        string
	Status      string    // queued, in_progress, completed
	Conclusion  string    // success, failure, neutral, cancelled, skipped, timed_out, action_required
	Output      string    // summary/text from the check run, or target_url for commit statuses
	HTMLURL     string    // direct link to the check run page on GitHub
	CompletedAt time.Time // when the check run completed (zero if not completed or unknown)
}

// WorkflowRun represents a GitHub Actions workflow run.
type WorkflowRun struct {
	ID           int64
	Status       string // queued, in_progress, completed
	Conclusion   string // success, failure, cancelled, skipped, timed_out, action_required, neutral
	CreatedAt    time.Time
	HTMLURL      string
	Event        string // e.g. "push", "pull_request", "schedule"
	HeadBranch   string // branch that triggered the run
	DisplayTitle string // human-readable title (e.g. PR title)
}

// WorkflowJob represents a GitHub Actions workflow job.
type WorkflowJob struct {
	ID         int64
	Name       string
	Conclusion string // success, failure, cancelled, skipped, etc.
}
