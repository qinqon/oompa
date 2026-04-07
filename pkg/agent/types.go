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
	ID   int64
	User string
	Body string
	Path string
	Line int
}

// PR represents a GitHub pull request.
type PR struct {
	Number int
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
	IssueNumber   int       `json:"issueNumber"`
	IssueTitle    string    `json:"issueTitle"`
	WorktreePath  string    `json:"worktreePath"`
	BranchName    string    `json:"branchName"`
	PRNumber      int       `json:"prNumber"`
	LastCommentID int64     `json:"lastCommentID"`
	Status        string    `json:"status"` // implementing, pr-open, failed, done
	CreatedAt     time.Time `json:"createdAt"`
}
