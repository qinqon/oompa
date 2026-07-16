package agent

import "time"

// Commit represents a Git commit.
type Commit struct {
	SHA     string
	Subject string
}

// AgentResult represents the parsed result from a coding agent invocation.
type AgentResult struct {
	Result  string  // final text output
	CostUSD float64 // cost (if available)
}

// IssueWork tracks the state of work on a single issue.
// State is held in memory only — it is rebuilt from GitHub on startup by
// BuildStateFromGitHub and never serialized to disk.
type IssueWork struct {
	IssueNumber        int
	IssueTitle         string
	WorktreePath       string
	BranchName         string
	PRNumber           int
	LastCommentID      int64
	LastReviewID       int64
	LastIssueCommentID int64  // cursor for PR conversation comments (Issues API)
	Status             string // implementing, pr-open, failed, done
	CIFixAttempts      int
	LastCIStatus       string          // "", "pending", "success", "failure"
	LastCheckedCISHA   string          // last commit SHA investigated for CI failures
	CheckedCIChecks    map[string]bool // tracks "sha:check" keys investigated (for dedup without comments)
	ReviewNoOpCount    int             // consecutive cycles where reviews produced no push
	SessionCostUSD     float64         // cumulative agent cost for this PR in current session
	LastRebaseTime     time.Time       // when this PR was last rebased (for min-interval guard)
}

// JobRun represents a CI job run (periodic or triggered).
type JobRun struct {
	ID           string    // build ID or run number
	JobName      string    // human-readable job name
	Status       string    // success, failure, pending
	Timestamp    time.Time // when the run started
	LogURL       string    // URL to view logs
	Event        string    // trigger event (e.g. "push", "pull_request", "schedule")
	HeadBranch   string    // branch that triggered the run
	DisplayTitle string    // human-readable title (e.g. PR title)
}
