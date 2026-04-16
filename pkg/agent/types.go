package agent

import "time"

// Issue represents a GitHub issue.
type Issue struct {
	Number int
	Title  string
	Body   string
	Labels []string
}

// ReviewComment represents a comment on a PR or issue.
type ReviewComment struct {
	ID          int64
	InReplyToID int64
	User        string
	Body        string
	Path        string
	Line        int
}

// PR represents a GitHub pull request.
type PR struct {
	Number int
	Title  string
	State  string
	Merged bool
	Head   string
}

// ClaudeResult represents the parsed JSON output from Claude CLI.
type ClaudeResult struct {
	Result string `json:"result"`
	Cost   float64 `json:"cost_usd"`
}

// IssueWork tracks the state of work on a single issue.
type IssueWork struct {
	IssueNumber      int       `json:"issueNumber"`
	IssueTitle       string    `json:"issueTitle"`
	WorktreePath     string    `json:"worktreePath"`
	BranchName       string    `json:"branchName"`
	PRNumber         int       `json:"prNumber"`
	LastCommentID    int64     `json:"lastCommentID"`
	LastReviewID     int64     `json:"lastReviewID"`
	Status           string    `json:"status"` // implementing, pr-open, failed, done
	CIFixAttempts    int       `json:"ciFixAttempts"`
	LastCIStatus     string    `json:"lastCIStatus"`     // "", "pending", "success", "failure"
	LastCheckedCISHA string    `json:"lastCheckedCISHA"` // last commit SHA investigated for CI failures
	CreatedAt        time.Time `json:"createdAt"`
}

// PRReview represents a GitHub pull request review (approve, request changes, comment).
type PRReview struct {
	ID          int64
	User        string
	State       string // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING
	Body        string
	SubmittedAt time.Time
}

// CheckRun represents a GitHub Actions check run.
type CheckRun struct {
	ID         int64
	Name       string
	Status     string // queued, in_progress, completed
	Conclusion string // success, failure, neutral, cancelled, skipped, timed_out, action_required
	Output     string // summary/text from the check run
}
