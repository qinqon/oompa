package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// mockGitHubClient implements GitHubClient for testing.
type mockGitHubClient struct {
	issues              []Issue
	prComments          []ReviewComment
	issueComments       []ReviewComment
	prState             string
	prs                 []PR
	addedComments       []string
	addedCommentTargets []int // issue/PR number each comment was posted to
	addedLabels         []string
	addedReactions      []string
	checkRuns           []CheckRun
	commitStatuses      []CheckRun       // commit status failures (returned by GetCommitStatuses)
	checkRunLogs        map[int64]string // maps check run ID to full log content
	prHeadSHAs          []string         // returns these in sequence; if empty returns "abc123"
	prsAfterNCalls      int              // only return PRs after this many ListPRsByHead calls
	prsCallCount        int
	listPRsErr          error                     // error to return from ListPRsByHead
	linkedPR            bool                      // return value for HasLinkedPR
	linkedPRErr         error                     // error to return from HasLinkedPR
	mergeableState      string                    // mergeable state to return from GetPRMergeable (default: "clean")
	prBehind            bool                      // whether IsPRBehind returns true
	createdIssues       []Issue                   // tracks issues created via CreateIssue
	nextIssueNumber     int                       // next issue number to return (defaults to 1)
	searchResults       []Issue                   // results to return from SearchIssues
	workflowRuns        []WorkflowRun             // workflow runs to return from ListWorkflowRuns
	prReviews           []PRReview                // reviews to return from GetPRReviews
	headCommitDate      time.Time                 // date to return from GetPRHeadCommitDate
	recentCommits       int                       // number of recent commits returned by CountCommitsSince
	countCommitsErr     error                     // error to return from CountCommitsSince
	hasEyesReaction     map[int64]map[string]bool // commentID -> user -> has eyes reaction

	listIssuesCalled bool // tracks whether ListLabeledIssues was called
	listIssuesErr    error
}

func (m *mockGitHubClient) ListLabeledIssues(_ context.Context, _, _, _ string) ([]Issue, error) {
	m.listIssuesCalled = true
	return m.issues, m.listIssuesErr
}

func (m *mockGitHubClient) GetPRReviewComments(_ context.Context, _, _ string, _ int, sinceID int64) ([]ReviewComment, error) {
	var filtered []ReviewComment
	for _, c := range m.prComments {
		if c.ID > sinceID {
			filtered = append(filtered, c)
		}
	}
	return filtered, nil
}

func (m *mockGitHubClient) GetIssueComments(_ context.Context, _, _ string, _ int, sinceID int64) ([]ReviewComment, error) {
	var filtered []ReviewComment
	for _, c := range m.issueComments {
		if c.ID > sinceID {
			filtered = append(filtered, c)
		}
	}
	return filtered, nil
}

func (m *mockGitHubClient) GetPRState(_ context.Context, _, _ string, _ int) (string, error) {
	return m.prState, nil
}

func (m *mockGitHubClient) AddIssueComment(_ context.Context, _, _ string, issueNum int, body string) error {
	m.addedComments = append(m.addedComments, body)
	m.addedCommentTargets = append(m.addedCommentTargets, issueNum)
	return nil
}

func (m *mockGitHubClient) AddLabel(_ context.Context, _, _ string, _ int, label string) error {
	m.addedLabels = append(m.addedLabels, label)
	return nil
}

func (m *mockGitHubClient) ListPRsByHead(_ context.Context, _, _, _, _ string) ([]PR, error) {
	m.prsCallCount++
	if m.listPRsErr != nil {
		return nil, m.listPRsErr
	}
	if m.prsAfterNCalls > 0 && m.prsCallCount <= m.prsAfterNCalls {
		return nil, nil
	}
	return m.prs, nil
}

func (m *mockGitHubClient) AddPRCommentReaction(_ context.Context, _, _ string, commentID int64, reaction string) error {
	m.addedReactions = append(m.addedReactions, fmt.Sprintf("%d:%s", commentID, reaction))
	return nil
}

func (m *mockGitHubClient) AddIssueCommentReaction(_ context.Context, _, _ string, commentID int64, reaction string) error {
	m.addedReactions = append(m.addedReactions, fmt.Sprintf("issue:%d:%s", commentID, reaction))
	return nil
}

func (m *mockGitHubClient) GetCheckRuns(_ context.Context, _, _, _ string) ([]CheckRun, error) {
	return m.checkRuns, nil
}

func (m *mockGitHubClient) GetCommitStatuses(_ context.Context, _, _, _ string) ([]CheckRun, error) {
	return m.commitStatuses, nil
}

func (m *mockGitHubClient) GetCheckRunLog(_ context.Context, _, _ string, checkRunID int64) (string, error) {
	if m.checkRunLogs != nil {
		if log, ok := m.checkRunLogs[checkRunID]; ok {
			return log, nil
		}
	}
	return "", nil
}

func (m *mockGitHubClient) GetPRHeadSHA(_ context.Context, _, _ string, _ int) (string, error) {
	if len(m.prHeadSHAs) > 0 {
		sha := m.prHeadSHAs[0]
		m.prHeadSHAs = m.prHeadSHAs[1:]
		return sha, nil
	}
	return "abc123", nil
}

func (m *mockGitHubClient) HasPRCommentReaction(_ context.Context, _, _ string, commentID int64, reaction, user string) (bool, error) {
	if reaction == "eyes" && m.hasEyesReaction != nil {
		if byUser, ok := m.hasEyesReaction[commentID]; ok {
			return byUser[user], nil
		}
	}
	return false, nil
}

func (m *mockGitHubClient) GetPRMergeable(_ context.Context, _, _ string, _ int) (string, error) {
	if m.mergeableState != "" {
		return m.mergeableState, nil
	}
	return "clean", nil
}

func (m *mockGitHubClient) GetPRReviews(_ context.Context, _, _ string, _ int, sinceID int64) ([]PRReview, error) {
	var filtered []PRReview
	for _, r := range m.prReviews {
		if r.ID > sinceID {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
}

func (m *mockGitHubClient) GetPRHeadCommitDate(_ context.Context, _, _ string, _ int) (time.Time, error) {
	return m.headCommitDate, nil
}

func (m *mockGitHubClient) CreatePR(_ context.Context, _, _, _, _, _, _ string) (int, error) {
	return 100, nil
}

func (m *mockGitHubClient) HasLinkedPR(_ context.Context, _, _ string, _ int) (bool, error) {
	if m.linkedPRErr != nil {
		return false, m.linkedPRErr
	}
	return m.linkedPR, nil
}

func (m *mockGitHubClient) GetPR(_ context.Context, _, _ string, prNumber int) (PR, error) {
	for _, p := range m.prs {
		if p.Number == prNumber {
			return p, nil
		}
	}
	return PR{}, fmt.Errorf("PR %d not found", prNumber)
}

func (m *mockGitHubClient) IsPRBehind(_ context.Context, _, _ string, _ int) (bool, error) {
	return m.prBehind, nil
}

func (m *mockGitHubClient) AssignIssue(_ context.Context, _, _ string, _ int, _ string) error {
	return nil
}

func (m *mockGitHubClient) UnassignIssue(_ context.Context, _, _ string, _ int, _ string) error {
	return nil
}

func (m *mockGitHubClient) CreateIssue(_ context.Context, _, _, title, body string, labels []string) (int, error) {
	if m.nextIssueNumber == 0 {
		m.nextIssueNumber = 1
	}
	issueNum := m.nextIssueNumber
	m.nextIssueNumber++
	m.createdIssues = append(m.createdIssues, Issue{
		Number: issueNum,
		Title:  title,
		Body:   body,
		Labels: labels,
	})
	return issueNum, nil
}

func (m *mockGitHubClient) SearchIssues(_ context.Context, _ string) ([]Issue, error) {
	return m.searchResults, nil
}

func (m *mockGitHubClient) ListWorkflowRuns(_ context.Context, _, _, _, _ string, _ int, _ time.Time) ([]WorkflowRun, error) {
	return m.workflowRuns, nil
}

func (m *mockGitHubClient) ListWorkflowJobs(_ context.Context, _, _ string, _ int64) ([]WorkflowJob, error) {
	return nil, nil
}

func (m *mockGitHubClient) GetWorkflowJobLogs(_ context.Context, _, _ string, _ int64) (string, error) {
	return "", nil
}

func (m *mockGitHubClient) CountCommitsSince(_ context.Context, _, _ string, _ time.Time) (int, error) {
	return m.recentCommits, m.countCommitsErr
}

// mockWorktreeManager implements WorktreeManager for testing.
type mockWorktreeManager struct {
	createdBranches []string
	removedPaths    []string
	cloneCalled     bool
	createErr       error
}

func (m *mockWorktreeManager) EnsureRepoCloned(_ context.Context) error {
	m.cloneCalled = true
	return nil
}

func (m *mockWorktreeManager) CreateWorktree(_ context.Context, branchName string) (string, error) {
	if m.createErr != nil {
		return "", m.createErr
	}
	m.createdBranches = append(m.createdBranches, branchName)
	return "/tmp/worktrees/" + branchName, nil
}

func (m *mockWorktreeManager) SyncWorktree(_ context.Context, _ string) error {
	return nil
}

func (m *mockWorktreeManager) RemoveWorktree(_ context.Context, worktreePath string) error {
	m.removedPaths = append(m.removedPaths, worktreePath)
	return nil
}

func (m *mockWorktreeManager) DefaultBranch() string { return "main" }

func (m *mockWorktreeManager) OriginDefaultBranch() string { return "origin/main" }

func (m *mockWorktreeManager) PushRemote() string { return "origin" }

// commandCall records one invocation seen by mockCommandRunner.
type commandCall struct {
	WorkDir string
	Name    string
	Args    []string
	Stdin   string
}

type mockCommandRunner struct {
	mu            sync.Mutex
	calls         []commandCall
	stdout        []byte
	stderr        []byte
	err           error
	claudeResults [][]byte
	claudeIndex   int
}

func (m *mockCommandRunner) Run(_ context.Context, workDir, name string, args ...string) (stdout, stderr []byte, err error) {
	m.mu.Lock()
	m.calls = append(m.calls, commandCall{WorkDir: workDir, Name: name, Args: args})
	stdout = m.stdout
	if name == "claude" && len(m.claudeResults) > 0 {
		if m.claudeIndex < len(m.claudeResults) {
			stdout = m.claudeResults[m.claudeIndex]
		} else {
			stdout = m.claudeResults[len(m.claudeResults)-1]
		}
		m.claudeIndex++
	}
	stderr, err = m.stderr, m.err
	m.mu.Unlock()
	return stdout, stderr, err
}

func (m *mockCommandRunner) RunWithStdin(_ context.Context, workDir, stdin, name string, args ...string) (stdout, stderr []byte, err error) {
	m.mu.Lock()
	m.calls = append(m.calls, commandCall{WorkDir: workDir, Name: name, Args: args, Stdin: stdin})
	stdout = m.stdout
	if name == "claude" && len(m.claudeResults) > 0 {
		if m.claudeIndex < len(m.claudeResults) {
			stdout = m.claudeResults[m.claudeIndex]
		} else {
			stdout = m.claudeResults[len(m.claudeResults)-1]
		}
		m.claudeIndex++
	}
	stderr, err = m.stderr, m.err
	m.mu.Unlock()
	return stdout, stderr, err
}

// discardLogger returns a logger that drops all output, keeping tests quiet.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// agentOpt customizes an Agent built by newTestAgent.
type agentOpt func(*Agent)

// withCfg mutates the canonical test Config before the test runs.
func withCfg(mutate func(*Config)) agentOpt {
	return func(a *Agent) { mutate(&a.cfg) }
}

// withCodeAgent replaces the default code agent.
func withCodeAgent(ca CodeAgent) agentOpt {
	return func(a *Agent) { a.codeAgent = ca }
}

// newTestAgent builds an Agent through the production constructor with
// canonical test doubles: owner/repo config, fresh state, and a discard
// logger. Options apply after construction.
func newTestAgent(gh *mockGitHubClient, runner CommandRunner, wt WorktreeManager, opts ...agentOpt) *Agent {
	cfg := Config{Owner: "owner", Repo: "repo", Label: "good-for-ai", FlakyLabel: "flaky-test", GitHubUser: "test-bot"}
	a := NewAgent(gh, runner, wt, NewState(), cfg, discardLogger(), &ClaudeCodeAgent{})
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// trackWork registers the canonical in-flight fixture (issue 42 with PR 100
// open on branch ai/issue-42) in the agent state, applies any mutations, and
// returns the tracked work for later assertions.
func trackWork(a *Agent, mutate ...func(*IssueWork)) *IssueWork {
	w := &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       StatusPROpen,
		WorktreePath: "/tmp/worktree",
	}
	for _, m := range mutate {
		m(w)
	}
	a.state.ActiveIssues[IssueKey(a.cfg.Owner, a.cfg.Repo, w.IssueNumber)] = w
	return w
}

// countCalls returns how many recorded commands invoked the named binary.
func countCalls(calls []commandCall, name string) int {
	count := 0
	for _, c := range calls {
		if c.Name == name {
			count++
		}
	}
	return count
}

// sequentialMockCodeAgent returns different results for sequential calls.
type sequentialMockCodeAgent struct {
	results   []mockCodeAgentCall
	callCount int
	prompts   []string
}

type mockCodeAgentCall struct {
	result AgentResult
	err    error
}

func (m *sequentialMockCodeAgent) Run(_ context.Context, _ CommandRunner, _, prompt string, _ *slog.Logger, _ bool) (AgentResult, error) {
	idx := m.callCount
	m.callCount++
	m.prompts = append(m.prompts, prompt)
	if idx < len(m.results) {
		return m.results[idx].result, m.results[idx].err
	}
	return AgentResult{}, fmt.Errorf("unexpected call %d", idx)
}
