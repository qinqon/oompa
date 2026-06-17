package agent

import (
	"fmt"
	"time"
)

// Config holds all agent configuration.
type Config struct {
	Owner        string
	Repo         string
	Label        string
	CloneDir     string
	PollInterval time.Duration
	LogLevel     string
	LogFile       string
	DryRun        bool
	OneShot       bool
	SignedOffBy   string
	AssistedBy    string   // Assisted-by trailer value for commits (e.g. "Claude <noreply@anthropic.com>")
	GitHubUser      string   // authenticated GitHub username (for reaction checks)
	GitHubToken     string   // GitHub token (passed to Claude for gh CLI access)
	GitHubHeadOwner string   // owner for PR head filtering (fork owner for PAT, repo owner for App)
	ForkOwner       string   // owner of the fork repo for pushing (empty = push to upstream)
	ForkRepo        string   // name of the fork repo (empty = same as Repo)
	GitAuthorName   string   // git commit author name
	GitAuthorEmail  string   // git commit author email
	Reviewers       []string // whitelist of users/bots whose reviews to address
	WatchPRs          []int    // PR numbers to monitor directly (bypasses issue discovery)
	Reactions         []string // which reactions to run: "reviews", "ci", "conflicts" (empty = all)
	CreateFlakyIssues bool     // when true, create issues for unrelated CI failures (opt-in)
	FlakyLabel        string   // label applied to flaky CI issues (default: "flaky-test")
	OnlyAssigned      bool     // when true, only process issues assigned to the agent user
	TriageJobs          []string       // CI job URLs to monitor for periodic job triage
	TriageWorkflow      string         // GHA workflow file for lane-level triage (relative to repo)
	TriageLanePatterns  []string       // glob patterns for matrix job names (lane-level filtering)
	TriageLookback      time.Duration  // time window to check for failed triage runs (0 = latest only)
	Role              string   // role identifier: "prs", "issues", "triage" (set by BuildRoleEntries)
	Agent             string   // coding agent backend: "claudecode" or "opencode"
	AgentModel        string   // model override for OpenCode (empty = default)
	Version           string   // build version (commit SHA) for comment watermarks
	SkipFix           bool     // when true, investigate and comment but never fix or push code changes
	SkipComments      []string // comment categories to suppress: ci-unrelated, ci-infrastructure, ci-related, conflict, rebase, flaky, issue-in-progress
	SkipChecks        []string // CI check names to ignore entirely (filtered before investigation)
	MaxReviewNoOps    int      // consecutive no-op review cycles before pausing review processing (default: 3)
	MaxPRSessionCost  float64  // max cumulative agent cost per PR per session before pausing (default: 0 = unlimited)
	SlackWebhookURL   string   // Slack Incoming Webhook URL for per-cycle reporting (empty = disabled)
	RebaseInterval    time.Duration // minimum time between rebases (default: 4h)

	// GitHub App authentication (alternative to GITHUB_TOKEN)
	GitHubAppID             int64
	GitHubAppPrivateKey     []byte
	GitHubAppInstallationID int64
}

// DefaultAssistedBy returns the default Assisted-by trailer value for the given agent backend.
func DefaultAssistedBy(agentBackend string) string {
	if agentBackend == "" {
		return ""
	}
	switch agentBackend {
	case "claudecode":
		return "Claude <noreply@anthropic.com>"
	case "opencode":
		return "Claude <noreply@anthropic.com>"
	default:
		return fmt.Sprintf("%s <noreply@anthropic.com>", agentBackend)
	}
}
