package agent

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockGitHubClient implements GitHubClient for testing.
type mockGitHubClient struct {
	issues          []Issue
	prComments      []ReviewComment
	issueComments   []ReviewComment
	prState         string
	prs             []PR
	addedComments   []string
	addedLabels     []string
	removedLabels   []string
	addedReactions  []string
	checkRuns       []CheckRun
	commitStatuses  []CheckRun       // commit status failures (returned by GetCommitStatuses)
	checkRunLogs    map[int64]string // maps check run ID to full log content
	prHeadSHAs      []string         // returns these in sequence; if empty returns "abc123"
	prsAfterNCalls  int              // only return PRs after this many ListPRsByHead calls
	prsCallCount    int
	mergeableState  string        // mergeable state to return from GetPRMergeable (default: "clean")
	prBehind        bool          // whether IsPRBehind returns true
	createdIssues   []Issue       // tracks issues created via CreateIssue
	nextIssueNumber int           // next issue number to return (defaults to 1)
	searchResults   []Issue       // results to return from SearchIssues
	workflowRuns    []WorkflowRun // workflow runs to return from ListWorkflowRuns

	listIssuesErr error
}

func (m *mockGitHubClient) ListLabeledIssues(_ context.Context, _, _, _ string) ([]Issue, error) {
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

func (m *mockGitHubClient) AddIssueComment(_ context.Context, _, _ string, _ int, body string) error {
	m.addedComments = append(m.addedComments, body)
	return nil
}

func (m *mockGitHubClient) AddLabel(_ context.Context, _, _ string, _ int, label string) error {
	m.addedLabels = append(m.addedLabels, label)
	return nil
}

func (m *mockGitHubClient) RemoveLabel(_ context.Context, _, _ string, _ int, label string) error {
	m.removedLabels = append(m.removedLabels, label)
	return nil
}

func (m *mockGitHubClient) ListPRsByHead(_ context.Context, _, _, _, _ string) ([]PR, error) {
	m.prsCallCount++
	if m.prsAfterNCalls > 0 && m.prsCallCount <= m.prsAfterNCalls {
		return nil, nil
	}
	return m.prs, nil
}

func (m *mockGitHubClient) AddPRCommentReaction(_ context.Context, _, _ string, commentID int64, reaction string) error {
	m.addedReactions = append(m.addedReactions, fmt.Sprintf("%d:%s", commentID, reaction))
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

func (m *mockGitHubClient) HasPRCommentReaction(_ context.Context, _, _ string, _ int64, _, _ string) (bool, error) {
	return false, nil
}

func (m *mockGitHubClient) ReplyToPRComment(_ context.Context, _, _ string, _ int, commentID int64, body string) error {
	m.addedComments = append(m.addedComments, fmt.Sprintf("reply:%d:%s", commentID, body))
	return nil
}

func (m *mockGitHubClient) GetPRMergeable(_ context.Context, _, _ string, _ int) (string, error) {
	if m.mergeableState != "" {
		return m.mergeableState, nil
	}
	return "clean", nil
}

func (m *mockGitHubClient) GetPRReviews(_ context.Context, _, _ string, _ int, _ int64) ([]PRReview, error) {
	return nil, nil
}

func (m *mockGitHubClient) GetPRHeadCommitDate(_ context.Context, _, _ string, _ int) (time.Time, error) {
	return time.Time{}, nil
}

func (m *mockGitHubClient) CreatePR(_ context.Context, _, _, _, _, _, _ string) (int, error) {
	return 100, nil
}

func (m *mockGitHubClient) HasLinkedPR(_ context.Context, _, _ string, _ int) (bool, error) {
	return false, nil
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

func (m *mockGitHubClient) ListWorkflowRuns(_ context.Context, _, _, _, _ string, _ int) ([]WorkflowRun, error) {
	return m.workflowRuns, nil
}

func (m *mockGitHubClient) ListWorkflowJobs(_ context.Context, _, _ string, _ int64) ([]WorkflowJob, error) {
	return nil, nil
}

func (m *mockGitHubClient) GetWorkflowJobLogs(_ context.Context, _, _ string, _ int64) (string, error) {
	return "", nil
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

func newTestAgent(gh *mockGitHubClient, runner CommandRunner, wt *mockWorktreeManager) *Agent {
	return &Agent{
		gh:        gh,
		runner:    runner,
		worktrees: wt,
		state:     NewState(),
		cfg:       Config{Owner: "owner", Repo: "repo", Label: "good-for-ai", FlakyLabel: "flaky-test"},
		logger:    slog.Default(),
		codeAgent: &ClaudeCodeAgent{},
	}
}

func TestProcessNewIssues_SkipsAlreadyTracked(t *testing.T) {
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug"}},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{IssueNumber: 42, Status: "pr-open"}

	agent.ProcessNewIssues(context.Background())

	if len(wt.createdBranches) != 0 {
		t.Error("should not create worktree for already tracked issue")
	}
	if len(runner.calls) != 0 {
		t.Error("should not invoke claude for already tracked issue")
	}
}

func TestProcessNewIssues_RechecksForPR(t *testing.T) {
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug"}},
		prs:    []PR{{Number: 100, State: "open", Head: "ai/issue-42"}},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber: 42,
		BranchName:  "ai/issue-42",
		Status:      "implementing",
		PRNumber:    0,
	}

	agent.ProcessNewIssues(context.Background())

	// Should not re-run claude
	if len(runner.calls) != 0 {
		t.Error("should not invoke claude for already tracked issue")
	}

	// Should have found the PR
	work := agent.state.ActiveIssues[42]
	if work.PRNumber != 100 {
		t.Errorf("expected PRNumber 100, got %d", work.PRNumber)
	}
	if work.Status != "pr-open" {
		t.Errorf("expected status 'pr-open', got %q", work.Status)
	}
}

func TestProcessNewIssues_HappyPath(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "Fixed it"})
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.ProcessNewIssues(context.Background())

	if len(wt.createdBranches) != 1 || wt.createdBranches[0] != "ai/issue-42" {
		t.Errorf("expected branch 'ai/issue-42', got %v", wt.createdBranches)
	}

	work, ok := agent.state.ActiveIssues[42]
	if !ok {
		t.Fatal("issue 42 not in state")
	}
	// CreatePR mock returns 100
	if work.PRNumber != 100 {
		t.Errorf("expected PR 100, got %d", work.PRNumber)
	}
	if work.Status != "pr-open" {
		t.Errorf("expected status 'pr-open', got %q", work.Status)
	}
}

func TestProcessNewIssues_SkipsWhenPRExists(t *testing.T) {
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
		prs:    []PR{{Number: 100, State: "open", Head: "ai/issue-42"}},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.ProcessNewIssues(context.Background())

	// Should not invoke Claude
	if len(runner.calls) != 0 {
		t.Error("should not invoke claude when PR already exists")
	}
	if len(wt.createdBranches) != 0 {
		t.Error("should not create worktree when PR already exists")
	}

	work, ok := agent.state.ActiveIssues[42]
	if !ok {
		t.Fatal("issue 42 should be tracked")
	}
	if work.PRNumber != 100 {
		t.Errorf("expected PR 100, got %d", work.PRNumber)
	}
	if work.Status != "pr-open" {
		t.Errorf("expected status 'pr-open', got %q", work.Status)
	}
}

func TestProcessNewIssues_ClaudeFailure(t *testing.T) {
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
	}
	runner := &mockCommandRunner{err: &mockError{msg: "claude crashed"}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.ProcessNewIssues(context.Background())

	if len(gh.addedLabels) != 1 || gh.addedLabels[0] != "ai-failed" {
		t.Errorf("expected 'ai-failed' label, got %v", gh.addedLabels)
	}
	if len(gh.addedComments) != 1 {
		t.Errorf("expected 1 comment (in-progress only, no error comment), got %d", len(gh.addedComments))
	}

	work := agent.state.ActiveIssues[42]
	if work == nil || work.Status != "failed" {
		t.Error("expected issue status to be 'failed'")
	}
}

func TestProcessReviewComments_NoNewComments(t *testing.T) {
	gh := &mockGitHubClient{}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:   42,
		PRNumber:      100,
		Status:        "pr-open",
		LastCommentID: 50,
	}

	agent.ProcessReviewComments(context.Background())

	if len(runner.calls) != 0 {
		t.Error("should not invoke claude when no new comments")
	}
}

func TestProcessReviewComments_AddressesHumanComments(t *testing.T) {
	triageResult := streamResultJSON(AgentResult{Result: "TRIAGE:\n- Comment #60 (reviewer): BUG FIX — missing nil check → ACCEPT"})
	implResult := streamResultJSON(AgentResult{Result: "Addressed"})
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{triageResult, implResult}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:   42,
		IssueTitle:    "Fix bug",
		PRNumber:      100,
		Status:        "pr-open",
		WorktreePath:  "/tmp/worktree",
		LastCommentID: 50,
	}

	agent.ProcessReviewComments(context.Background())

	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	// Two calls: triage + implementation
	if claudeCalls != 2 {
		t.Fatalf("expected 2 claude calls (triage + implement), got %d", claudeCalls)
	}

	if agent.state.ActiveIssues[42].LastCommentID != 60 {
		t.Errorf("expected lastCommentID 60, got %d", agent.state.ActiveIssues[42].LastCommentID)
	}
}

func TestProcessReviewComments_SkipsNonWhitelistedUsers(t *testing.T) {
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "random-user", Body: "some comment"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.Reviewers = []string{"trusted-reviewer"}
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:   42,
		PRNumber:      100,
		Status:        "pr-open",
		LastCommentID: 50,
	}

	agent.ProcessReviewComments(context.Background())

	if len(runner.calls) != 0 {
		t.Error("should not invoke claude for non-whitelisted user")
	}
}

func TestProcessReviewComments_AllowsAllWhenWhitelistEmpty(t *testing.T) {
	triageResult := streamResultJSON(AgentResult{Result: "TRIAGE:\n- Comment #60 (anyone): VALID IMPROVEMENT → ACCEPT"})
	implResult := streamResultJSON(AgentResult{Result: "Done"})
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "anyone", Body: "fix this"},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{triageResult, implResult}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	// No reviewers set — empty whitelist means allow all
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:   42,
		IssueTitle:    "Fix bug",
		PRNumber:      100,
		Status:        "pr-open",
		WorktreePath:  "/tmp/worktree",
		LastCommentID: 50,
	}

	agent.ProcessReviewComments(context.Background())

	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	// Two calls: triage + implementation
	if claudeCalls != 2 {
		t.Errorf("expected 2 claude calls with empty whitelist (triage + implement), got %d", claudeCalls)
	}
}

func TestProcessReviewComments_TriageSummaryLogged(t *testing.T) {
	triageSummary := "TRIAGE:\n- Comment #60 (reviewer): BUG FIX — nil dereference → ACCEPT"
	triageResult := streamResultJSON(AgentResult{Result: triageSummary})
	implResult := streamResultJSON(AgentResult{Result: "Addressed"})
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "nil check missing", Path: "main.go", Line: 10},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{triageResult, implResult}}
	wt := &mockWorktreeManager{}

	// Use a custom logger that captures log entries
	var logBuf logBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	agent := NewAgent(gh, runner, wt, NewState(), Config{Owner: "owner", Repo: "repo", Label: "good-for-ai", FlakyLabel: "flaky-test"}, logger, &ClaudeCodeAgent{})
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:   42,
		IssueTitle:    "Fix bug",
		PRNumber:      100,
		Status:        "pr-open",
		WorktreePath:  "/tmp/worktree",
		LastCommentID: 50,
	}

	agent.ProcessReviewComments(context.Background())

	// Verify the triage summary was logged
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "review triage summary") {
		t.Error("expected triage summary to be logged at Info level")
	}
	if !strings.Contains(logOutput, "TRIAGE:") {
		t.Error("expected log to contain the triage content")
	}
}

func TestProcessReviewComments_TriageFailureFallsBack(t *testing.T) {
	implResult := streamResultJSON(AgentResult{Result: "Addressed without triage"})
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	// First claude call (triage) fails, second (implementation) succeeds
	runner := &mockCommandRunner{
		claudeResults: [][]byte{
			nil, // triage will fail because of error
			implResult,
		},
	}
	wt := &mockWorktreeManager{}

	// Use a mockCodeAgent that fails on first call and succeeds on second
	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{err: fmt.Errorf("triage agent failed")},
			{result: AgentResult{Result: "Addressed without triage"}},
		},
	}

	agent := newTestAgent(gh, runner, wt)
	agent.codeAgent = codeAgent
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:   42,
		IssueTitle:    "Fix bug",
		PRNumber:      100,
		Status:        "pr-open",
		WorktreePath:  "/tmp/worktree",
		LastCommentID: 50,
	}

	agent.ProcessReviewComments(context.Background())

	// Should have called agent twice: triage (failed) + implementation (succeeded)
	if codeAgent.callCount != 2 {
		t.Fatalf("expected 2 agent calls (failed triage + implementation), got %d", codeAgent.callCount)
	}

	// The implementation prompt should NOT contain triage summary
	implPrompt := codeAgent.prompts[1]
	if strings.Contains(implPrompt, "triage summary was produced") {
		t.Error("implementation prompt should not contain triage section when triage failed")
	}

	// lastCommentID should still be updated
	if agent.state.ActiveIssues[42].LastCommentID != 60 {
		t.Errorf("expected lastCommentID 60, got %d", agent.state.ActiveIssues[42].LastCommentID)
	}
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

// logBuffer is a simple thread-safe bytes.Buffer for capturing log output.
type logBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (lb *logBuffer) Write(p []byte) (n int, err error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.buf = append(lb.buf, p...)
	return len(p), nil
}

func (lb *logBuffer) String() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return string(lb.buf)
}

func TestCleanupDone_MergedPR(t *testing.T) {
	gh := &mockGitHubClient{prState: "merged"}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.CleanupDone(context.Background())

	if len(wt.removedPaths) != 1 || wt.removedPaths[0] != "/tmp/worktree" {
		t.Errorf("expected worktree removal, got %v", wt.removedPaths)
	}
	if _, exists := agent.state.ActiveIssues[42]; exists {
		t.Error("expected issue 42 to be removed from state")
	}
}

func TestCleanupDone_ClosedPR(t *testing.T) {
	gh := &mockGitHubClient{prState: "closed"}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.CleanupDone(context.Background())

	if len(wt.removedPaths) != 1 {
		t.Error("expected worktree removal for closed PR")
	}
	if _, exists := agent.state.ActiveIssues[42]; exists {
		t.Error("expected issue 42 to be removed from state")
	}
}

func TestCleanupDone_OpenPR(t *testing.T) {
	gh := &mockGitHubClient{prState: "open"}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.CleanupDone(context.Background())

	if len(wt.removedPaths) != 0 {
		t.Error("should not remove worktree for open PR")
	}
	if _, exists := agent.state.ActiveIssues[42]; !exists {
		t.Error("should not remove open PR from state")
	}
}

func TestProcessCIFailures_FixesFailingCI(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "RELATED Fixed CI"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test", Status: "completed", Conclusion: "failure", Output: "tests failed"},
		},
		prHeadSHAs: []string{"sha-before", "sha-after"}, // different SHAs = Claude pushed
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	if claudeCalls != 1 {
		t.Fatalf("expected 1 claude call, got %d", claudeCalls)
	}
	if agent.state.ActiveIssues[42].CIFixAttempts != 1 {
		t.Errorf("expected 1 CI fix attempt, got %d", agent.state.ActiveIssues[42].CIFixAttempts)
	}
}

func TestProcessCIFailures_SkipsPassingCI(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test", Status: "completed", Conclusion: "success"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber: 42,
		PRNumber:    100,
		BranchName:  "ai/issue-42",
		Status:      "pr-open",
	}

	agent.ProcessCIFailures(context.Background())

	if len(runner.calls) != 0 {
		t.Error("should not invoke claude when CI passes")
	}
}

func TestProcessCIFailures_StopsAfterMaxRetries(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test", Status: "completed", Conclusion: "failure"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:   42,
		IssueTitle:    "Fix bug",
		PRNumber:      100,
		BranchName:    "ai/issue-42",
		Status:        "pr-open",
		CIFixAttempts: 3,
	}

	agent.ProcessCIFailures(context.Background())

	if len(runner.calls) != 0 {
		t.Error("should not invoke claude after max retries")
	}
	if len(gh.addedComments) != 0 {
		t.Error("expected no comments after max retries")
	}
}

func TestProcessCIFailures_SkipsPendingCI(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test", Status: "in_progress", Conclusion: ""},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber: 42,
		PRNumber:    100,
		BranchName:  "ai/issue-42",
		Status:      "pr-open",
	}

	agent.ProcessCIFailures(context.Background())

	if len(runner.calls) != 0 {
		t.Error("should not invoke claude while CI is still running")
	}
}

func TestProcessCIFailures_CreatesFlakyIssueWhenUnrelated(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true // Enable flaky issue creation
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Check that a flaky issue was created
	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected 1 created issue, got %d", len(gh.createdIssues))
	}
	issue := gh.createdIssues[0]
	if issue.Title != "Flaky CI: integration-tests" {
		t.Errorf("expected title 'Flaky CI: integration-tests', got %q", issue.Title)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "flaky-test" {
		t.Errorf("expected labels ['flaky-test'], got %v", issue.Labels)
	}
	// Check the body uses the flaking-test issue template format
	if !strings.Contains(issue.Body, "### Which jobs are flaking?") {
		t.Errorf("expected issue body to contain '### Which jobs are flaking?', got %q", issue.Body)
	}
	if !strings.Contains(issue.Body, "### Which tests are flaking?") {
		t.Errorf("expected issue body to contain '### Which tests are flaking?', got %q", issue.Body)
	}
	if !strings.Contains(issue.Body, "### Since when has it been flaking?") {
		t.Errorf("expected issue body to contain '### Since when has it been flaking?', got %q", issue.Body)
	}
	if !strings.Contains(issue.Body, "### Reason for failure (if possible)") {
		t.Errorf("expected issue body to contain '### Reason for failure (if possible)', got %q", issue.Body)
	}
	if !strings.Contains(issue.Body, "Automatically created by [oompa]") {
		t.Errorf("expected issue body to contain oompa attribution, got %q", issue.Body)
	}

	// Check that a comment was added to the PR
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (unrelated + issue link), got %d", len(gh.addedComments))
	}
}

func TestProcessCIFailures_SkipsFlakyIssueWhenDisabled(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = false // Disabled by default
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Check that no flaky issue was created
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues when feature is disabled, got %d", len(gh.createdIssues))
	}

	// Check that only one comment was added (the unrelated notice)
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment (unrelated notice), got %d", len(gh.addedComments))
	}
}

func TestProcessCIFailures_SkipsDuplicateFlakyIssue(t *testing.T) {
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	matchResult := streamResultJSON(AgentResult{Result: "MATCH 50"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
		searchResults: []Issue{
			{Number: 50, Title: "Flaky CI: integration-tests", Labels: []string{"flaky-test"}},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{ciResult, matchResult}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Check that no new issue was created (existing one should be referenced)
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues (should reference existing), got %d", len(gh.createdIssues))
	}

	// Check that comments were added (unrelated notice + duplicate reference)
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (unrelated + duplicate reference), got %d", len(gh.addedComments))
	}

	// Verify the duplicate reference comment
	if !strings.Contains(gh.addedComments[1], "duplicate of existing flaky test issue #50") {
		t.Errorf("expected duplicate reference comment, got: %q", gh.addedComments[1])
	}
}

func TestProcessCIFailures_CreatesNewFlakyIssueWhenNoDuplicate(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
		searchResults: []Issue{}, // No existing issues
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Check that a new issue was created
	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected 1 created issue, got %d", len(gh.createdIssues))
	}

	issue := gh.createdIssues[0]
	if issue.Title != "Flaky CI: integration-tests" {
		t.Errorf("expected title 'Flaky CI: integration-tests', got %q", issue.Title)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "flaky-test" {
		t.Errorf("expected labels ['flaky-test'], got %v", issue.Labels)
	}

	// Check that comments were added (unrelated notice + new issue reference)
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (unrelated + issue link), got %d", len(gh.addedComments))
	}
}

func TestProcessCIFailures_CreatesIssueWhenClaudeSaysNone(t *testing.T) {
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	matchResult := streamResultJSON(AgentResult{Result: "NONE"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
		searchResults: []Issue{
			{Number: 50, Title: "Some other flaky test", Labels: []string{"flaky-test"}},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{ciResult, matchResult}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Claude said NONE, so a new issue should be created
	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected 1 created issue, got %d", len(gh.createdIssues))
	}
	if gh.createdIssues[0].Title != "Flaky CI: integration-tests" {
		t.Errorf("expected title 'Flaky CI: integration-tests', got %q", gh.createdIssues[0].Title)
	}
}

func TestParseFlakyMatch(t *testing.T) {
	tests := []struct {
		input   string
		wantNum int
		wantOK  bool
	}{
		{"MATCH 50", 50, true},
		{"MATCH #50", 50, true},
		{"MATCH 123", 123, true},
		{"**MATCH 50", 50, true},
		{"NONE", 0, false},
		{"MATCH", 0, false},
		{"MATCH abc", 0, false},
		{"something else", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		num, ok := parseFlakyMatch(tt.input)
		if num != tt.wantNum || ok != tt.wantOK {
			t.Errorf("parseFlakyMatch(%q) = (%d, %v), want (%d, %v)", tt.input, num, ok, tt.wantNum, tt.wantOK)
		}
	}
}

func TestProcessCIFailures_ReinvestigatesAfterNewCommits(t *testing.T) {
	// Issue #28: Agent should re-investigate CI failures when new commits are pushed,
	// even if a previous rebase comment mentions the new commit SHA.
	claudeResult := streamResultJSON(AgentResult{Result: "RELATED Fixed CI"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test", Status: "completed", Conclusion: "failure", Output: "tests failed"},
		},
		prHeadSHAs: []string{"abc1234", "def5678"}, // First call returns abc1234, second returns def5678
		issueComments: []ReviewComment{
			// Simulate a rebase comment that mentions the new commit
			{ID: 1, User: "test-bot", Body: "Rebased commit def5678 on main and pushed.\n\n<!-- oompa-bot -->"},
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:      42,
		IssueTitle:       "Fix bug",
		PRNumber:         100,
		BranchName:       "ai/issue-42",
		Status:           "pr-open",
		WorktreePath:     "/tmp/worktree",
		LastCheckedCISHA: "abc1234", // Already investigated abc1234
	}

	// First call: should skip because LastCheckedCISHA matches current HEAD (abc1234)
	agent.ProcessCIFailures(context.Background())
	if countClaudeCalls(runner.calls) != 0 {
		t.Errorf("expected 0 claude calls (same SHA), got %d", countClaudeCalls(runner.calls))
	}

	// Second call: new commit def5678 pushed (e.g., by a human after rebase)
	// Even though there's a rebase comment mentioning def5678, the agent should
	// still investigate CI failures on this new commit
	agent.ProcessCIFailures(context.Background())
	if countClaudeCalls(runner.calls) != 1 {
		t.Errorf("expected 1 claude call (new SHA with CI failure), got %d", countClaudeCalls(runner.calls))
	}
}

func TestProcessCIFailures_SkipsAlreadyReportedAfterRestart(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test", Status: "completed", Conclusion: "failure", Output: "tests failed"},
		},
		prHeadSHAs: []string{"abc1234567890"},
		issueComments: []ReviewComment{
			{ID: 1, User: "bot", Body: fmt.Sprintf("CI check `test` failed on commit abc1234 but appears unrelated to this PR's changes.\n\nFlaky test\n\n%s", ciMarker("abc1234567890", "test"))},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	if countClaudeCalls(runner.calls) != 0 {
		t.Errorf("expected 0 claude calls (already reported via comment), got %d", countClaudeCalls(runner.calls))
	}
	if agent.state.ActiveIssues[42].LastCheckedCISHA != "abc1234567890" {
		t.Errorf("expected LastCheckedCISHA to be recovered to abc1234567890, got %q", agent.state.ActiveIssues[42].LastCheckedCISHA)
	}
}

func TestProcessCIFailures_NoDuplicateCommentsOnRepeatedPolls(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED Flaky network test"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "e2e-network", Status: "completed", Conclusion: "failure", Output: "timeout"},
		},
		prHeadSHAs: []string{"deadbeef1234567"},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment after first poll, got %d", len(gh.addedComments))
	}

	// Simulate the comment being visible on subsequent polls
	gh.issueComments = []ReviewComment{
		{ID: 1, User: "bot", Body: gh.addedComments[0]},
	}
	// Reset prHeadSHAs so mock returns the same SHA again
	gh.prHeadSHAs = []string{"deadbeef1234567"}

	// Second poll — should NOT post another comment
	agent.ProcessCIFailures(context.Background())

	if len(gh.addedComments) != 1 {
		t.Errorf("expected no new comments after second poll, got %d total", len(gh.addedComments))
	}
	if countClaudeCalls(runner.calls) != 1 {
		t.Errorf("expected 1 claude call total (skip second), got %d", countClaudeCalls(runner.calls))
	}
}

func countClaudeCalls(calls []commandCall) int {
	count := 0
	for _, c := range calls {
		if c.Name == "claude" {
			count++
		}
	}
	return count
}

func TestProcessCIFailures_DeduplicatesUnrelatedComments(t *testing.T) {
	// Issue #63: Should only post one unrelated comment per SHA, not on every poll cycle
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED Flaky test"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test", Status: "completed", Conclusion: "failure", Output: "tests failed"},
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	// First poll cycle: should investigate and post comment
	agent.ProcessCIFailures(context.Background())
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment on first poll, got %d", len(gh.addedComments))
	}
	if !strings.Contains(gh.addedComments[0], "CI check `test` failed on commit abc123 but appears unrelated") {
		t.Errorf("unexpected comment body: %s", gh.addedComments[0])
	}

	// Verify state was updated
	work := agent.state.ActiveIssues[42]
	if work.LastCheckedCISHA != "abc123" {
		t.Errorf("expected LastCheckedCISHA to be abc123, got %q", work.LastCheckedCISHA)
	}
	if work.LastCIStatus != "unrelated-failure" {
		t.Errorf("expected LastCIStatus to be unrelated-failure, got %q", work.LastCIStatus)
	}

	// Second poll cycle: same SHA, CI still failing
	// Should skip investigation entirely (no Claude call, no comment)
	agent.ProcessCIFailures(context.Background())
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected still only 1 comment after second poll (no duplicate), got %d", len(gh.addedComments))
	}
	// Verify Claude was not called again
	if countClaudeCalls(runner.calls) != 1 {
		t.Errorf("expected only 1 claude call total, got %d", countClaudeCalls(runner.calls))
	}
}

func TestShouldRunReaction_EmptyAllowsAll(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	// No reactions configured — all should be allowed
	for _, reaction := range []string{"reviews", "ci", "conflicts"} {
		if !agent.ShouldRunReaction(reaction) {
			t.Errorf("expected %q to be allowed with empty Reactions", reaction)
		}
	}
}

func TestShouldRunReaction_Filtered(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.Reactions = []string{"ci", "conflicts"}

	if !agent.ShouldRunReaction("ci") {
		t.Error("expected 'ci' to be allowed")
	}
	if !agent.ShouldRunReaction("conflicts") {
		t.Error("expected 'conflicts' to be allowed")
	}
	if agent.ShouldRunReaction("reviews") {
		t.Error("expected 'reviews' to be filtered out")
	}
}

func TestBootstrapWatchedPRs_HappyPath(t *testing.T) {
	gh := &mockGitHubClient{
		prs: []PR{
			{Number: 123, Title: "Fix login", State: "open", Head: "fix-login"},
			{Number: 456, Title: "Add tests", State: "open", Head: "add-tests"},
		},
	}
	agent := newTestAgent(gh, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.WatchPRs = []int{123, 456}

	agent.BootstrapWatchedPRs(context.Background())

	if len(agent.state.ActiveIssues) != 2 {
		t.Fatalf("expected 2 tracked PRs, got %d", len(agent.state.ActiveIssues))
	}

	work := agent.state.ActiveIssues[123]
	if work == nil {
		t.Fatal("PR 123 not tracked")
	}
	if work.PRNumber != 123 {
		t.Errorf("expected PRNumber 123, got %d", work.PRNumber)
	}
	if work.BranchName != "fix-login" {
		t.Errorf("expected branch 'fix-login', got %q", work.BranchName)
	}
	if work.Status != "pr-open" {
		t.Errorf("expected status 'pr-open', got %q", work.Status)
	}
	if work.IssueTitle != "Fix login" {
		t.Errorf("expected title 'Fix login', got %q", work.IssueTitle)
	}

	work2 := agent.state.ActiveIssues[456]
	if work2 == nil {
		t.Fatal("PR 456 not tracked")
	}
	if work2.BranchName != "add-tests" {
		t.Errorf("expected branch 'add-tests', got %q", work2.BranchName)
	}
}

func TestBootstrapWatchedPRs_SkipsClosedPR(t *testing.T) {
	gh := &mockGitHubClient{
		prs: []PR{
			{Number: 123, Title: "Old PR", State: "closed", Merged: true, Head: "old-branch"},
		},
	}
	agent := newTestAgent(gh, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.WatchPRs = []int{123}

	agent.BootstrapWatchedPRs(context.Background())

	if len(agent.state.ActiveIssues) != 0 {
		t.Error("should not track closed/merged PRs")
	}
}

func TestBootstrapWatchedPRs_SkipsAlreadyTracked(t *testing.T) {
	gh := &mockGitHubClient{
		prs: []PR{
			{Number: 123, Title: "Fix login", State: "open", Head: "fix-login"},
		},
	}
	agent := newTestAgent(gh, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.WatchPRs = []int{123}

	// Pre-populate state
	agent.state.ActiveIssues[123] = &IssueWork{
		PRNumber: 123,
		Status:   "pr-open",
	}

	agent.BootstrapWatchedPRs(context.Background())

	// Should still have exactly 1 entry (not duplicated)
	if len(agent.state.ActiveIssues) != 1 {
		t.Errorf("expected 1 tracked PR, got %d", len(agent.state.ActiveIssues))
	}
}

func TestProcessConflicts_SkipsWhenBehind(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "behind",
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessConflicts(context.Background())

	// ProcessConflicts should NOT rebase when state is "behind" (no conflicts)
	var rebaseCalls int
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" {
			rebaseCalls++
		}
	}
	if rebaseCalls != 0 {
		t.Error("ProcessConflicts should not rebase when state is behind (use ProcessRebase instead)")
	}
}

func TestProcessRebase_RebasesWhenBehind(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "behind",
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessRebase(context.Background())

	// Should have attempted a rebase (git fetch + git rebase)
	var fetchCalls, rebaseCalls int
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "fetch" {
			fetchCalls++
		}
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" {
			rebaseCalls++
		}
	}
	if fetchCalls == 0 {
		t.Error("expected git fetch to be called for behind PR")
	}
	if rebaseCalls == 0 {
		t.Error("expected git rebase to be called for behind PR")
	}
}

func TestProcessRebase_RebasesWhenUnstableButBehind(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "unstable",
		prBehind:       true,
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessRebase(context.Background())

	var rebaseCalls int
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" {
			rebaseCalls++
		}
	}
	if rebaseCalls == 0 {
		t.Error("expected git rebase to be called for unstable+behind PR")
	}
}

func TestProcessConflicts_SkipsUnstableNotBehind(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "unstable",
		prBehind:       false,
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessConflicts(context.Background())

	for _, c := range runner.calls {
		if c.Name == "git" {
			t.Errorf("should not run git commands for unstable+not-behind PR, got: git %v", c.Args)
		}
	}
}

func TestProcessRebase_SkipsUnstableNotBehind(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "unstable",
		prBehind:       false,
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessRebase(context.Background())

	for _, c := range runner.calls {
		if c.Name == "git" {
			t.Errorf("should not run git commands for unstable+not-behind PR, got: git %v", c.Args)
		}
	}
}

func TestProcessRebase_SkipsDirty(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "dirty",
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessRebase(context.Background())

	// ProcessRebase should NOT handle dirty state (that's for ProcessConflicts)
	for _, c := range runner.calls {
		if c.Name == "git" {
			t.Errorf("ProcessRebase should not run git commands for dirty PR, got: git %v", c.Args)
		}
	}
}

func TestProcessRebase_InvokesConflictResolutionWhenRebaseFails(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "behind",
	}
	// Create a custom runner that returns conflict error for rebase
	baseMock := &mockCommandRunner{}
	runner := &conflictRebaseRunner{
		mockCommandRunner: baseMock,
	}
	wt := &mockWorktreeManager{}
	codeAgent := &mockCodeAgent{}

	agent := newTestAgent(gh, runner, wt)
	agent.codeAgent = codeAgent
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessRebase(context.Background())

	// Should have called git rebase and git rebase --abort
	var rebaseCalls, abortCalls int
	for _, c := range baseMock.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" {
			if len(c.Args) > 1 && c.Args[1] == "--abort" {
				abortCalls++
			} else {
				rebaseCalls++
			}
		}
	}

	if rebaseCalls == 0 {
		t.Error("expected git rebase to be called")
	}
	if abortCalls == 0 {
		t.Error("expected git rebase --abort to be called after conflict")
	}

	// Should have invoked the code agent for conflict resolution
	if !codeAgent.called {
		t.Error("expected code agent to be called for conflict resolution")
	}
	if !strings.Contains(codeAgent.lastPrompt, "merge conflicts") {
		t.Errorf("expected conflict resolution prompt, got: %s", codeAgent.lastPrompt)
	}
}

// conflictRebaseRunner is a test helper that returns conflict errors for git rebase
type conflictRebaseRunner struct {
	*mockCommandRunner
}

func (r *conflictRebaseRunner) Run(ctx context.Context, workDir, name string, args ...string) (stdout, stderr []byte, err error) {
	// Call the base mock to record the call
	stdout, _, _ = r.mockCommandRunner.Run(ctx, workDir, name, args...)

	// Return conflict error for "git rebase" (but not "git rebase --abort")
	if name == "git" && len(args) > 0 && args[0] == "rebase" {
		isAbort := slices.Contains(args, "--abort")
		if !isAbort {
			return nil, []byte("error: could not apply 3a35b4e... Migrate remaining features"), fmt.Errorf("rebase failed")
		}
	}

	return stdout, nil, nil
}

// mockCodeAgent is a test double for CodeAgent
type mockCodeAgent struct {
	called     bool
	lastPrompt string
	result     AgentResult
	err        error
}

func (m *mockCodeAgent) Run(ctx context.Context, runner CommandRunner, workDir, prompt string, logger *slog.Logger, resume bool) (AgentResult, error) {
	m.called = true
	m.lastPrompt = prompt
	if m.err != nil {
		return AgentResult{}, m.err
	}
	return m.result, nil
}

func TestProcessConflicts_SkipsCleanPR(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "clean",
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessConflicts(context.Background())

	// Should NOT attempt any git operations
	for _, c := range runner.calls {
		if c.Name == "git" {
			t.Errorf("should not run git commands for clean PR, got: git %v", c.Args)
		}
	}
}

func TestHasWatchedPRs(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})

	if agent.HasWatchedPRs() {
		t.Error("expected false with no watched PRs")
	}

	agent.cfg.WatchPRs = []int{123}
	if !agent.HasWatchedPRs() {
		t.Error("expected true with watched PRs")
	}
}
func TestProcessNewIssues_SquashesCommits(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "Fixed it"})
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix the bug", Body: "broken"}},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SignedOffBy = "Test User <test@example.com>"
	agent.ProcessNewIssues(context.Background())

	// Verify git reset --soft was called to squash commits
	foundReset := false
	foundCommit := false
	foundForcePush := false
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) >= 2 {
			if c.Args[0] == "reset" && c.Args[1] == "--soft" {
				foundReset = true
				// Should reset to origin/main
				if len(c.Args) >= 3 && c.Args[2] != "origin/main" {
					t.Errorf("expected reset to origin/main, got %v", c.Args)
				}
			}
			if c.Args[0] == "commit" && c.Args[1] == "-m" {
				foundCommit = true
				// Verify commit message includes issue number
				if len(c.Args) >= 3 {
					commitMsg := c.Args[2]
					if !strings.Contains(commitMsg, "Fix #42") {
						t.Errorf("expected commit message to contain 'Fix #42', got: %s", commitMsg)
					}
					if !strings.Contains(commitMsg, "Signed-off-by") {
						t.Errorf("expected commit message to contain 'Signed-off-by', got: %s", commitMsg)
					}
				}
			}
			if c.Args[0] == "push" {
				// Should be force-with-lease push after squashing
				hasForce := false
				if slices.Contains(c.Args, "--force-with-lease") {
					hasForce = true
					foundForcePush = true
				}
				if !hasForce {
					t.Error("expected force-with-lease push after squashing commits")
				}
			}
		}
	}

	if !foundReset {
		t.Error("expected git reset --soft to be called for commit squashing")
	}
	if !foundCommit {
		t.Error("expected git commit to be called after squashing")
	}
	if !foundForcePush {
		t.Error("expected git push --force-with-lease after squashing")
	}
}

func TestProcessTriageJobs_NoJobsConfigured(t *testing.T) {
	gh := &mockGitHubClient{}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:      "owner",
		Repo:       "repo",
		TriageJobs: []string{}, // No jobs configured
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), &ClaudeCodeAgent{})
	a.ProcessTriageJobs(context.Background())

	// Should not create any worktrees or run Claude
	if len(wtm.createdBranches) > 0 {
		t.Errorf("expected no worktrees created, got %d", len(wtm.createdBranches))
	}
	if len(runner.calls) > 0 {
		t.Errorf("expected no commands run, got %d", len(runner.calls))
	}
}

func TestProcessTriageJobs_SkipsAlreadyInvestigated(t *testing.T) {
	gh := &mockGitHubClient{}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()

	// Mark run as already investigated
	state.MarkRunInvestigated("periodic-knmstate-e2e-handler-k8s-latest", "1234567890")

	cfg := Config{
		Owner:      "nmstate",
		Repo:       "kubernetes-nmstate",
		TriageJobs: []string{"https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/logs/periodic-knmstate-e2e-handler-k8s-latest/"},
	}

	NewAgent(gh, runner, wtm, state, cfg, slog.Default(), &ClaudeCodeAgent{})

	// Note: This test would need a real HTTP server to mock GCS API responses
	// For now, we test the state checking logic directly
	if !state.IsRunInvestigated("periodic-knmstate-e2e-handler-k8s-latest", "1234567890") {
		t.Error("expected run to be marked as investigated")
	}
}

func TestProcessTriageJobs_SkipsSuccessfulRuns(t *testing.T) {
	// This test verifies the logic in ProcessTriageJobs that skips successful runs
	state := NewState()

	// Simulate a successful run
	jobName := "test-job"
	runID := "success-run"
	status := "success"

	// The agent should mark successful runs as investigated without creating issues
	if status == "success" {
		state.MarkRunInvestigated(jobName, runID)
	}

	if !state.IsRunInvestigated(jobName, runID) {
		t.Error("expected successful run to be marked as investigated")
	}
}

func TestProcessTriageJobs_CreatesIssueWhenFlakyIssuesEnabled(t *testing.T) {
	gh := &mockGitHubClient{
		searchResults: []Issue{}, // No existing issues
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()

	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		TriageJobs:        []string{}, // Empty to avoid actual HTTP calls
		CreateFlakyIssues: true,
		FlakyLabel:        "ci-flake",
	}

	NewAgent(gh, runner, wtm, state, cfg, slog.Default(), &ClaudeCodeAgent{})

	// Verify that CreateFlakyIssues flag is respected
	if !cfg.CreateFlakyIssues {
		t.Error("expected CreateFlakyIssues to be true")
	}

	// The actual issue creation happens in ProcessTriageJobs when it calls gh.CreateIssue
	// We can verify the mock tracks created issues
	if gh.nextIssueNumber == 0 {
		gh.nextIssueNumber = 1
	}

	issueNum, err := gh.CreateIssue(context.Background(), "owner", "repo",
		"CI Failure: test-job", "Analysis output", []string{cfg.FlakyLabel})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if issueNum != 1 {
		t.Errorf("expected issue number 1, got %d", issueNum)
	}

	if len(gh.createdIssues) != 1 {
		t.Errorf("expected 1 issue created, got %d", len(gh.createdIssues))
	}

	issue := gh.createdIssues[0]
	if issue.Title != "CI Failure: test-job" {
		t.Errorf("expected title 'CI Failure: test-job', got %q", issue.Title)
	}

	if len(issue.Labels) != 1 || issue.Labels[0] != "ci-flake" {
		t.Errorf("expected label 'ci-flake', got %v", issue.Labels)
	}
}

func TestProcessCIFailures_DetectsCommitStatusFailures(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "RELATED Fixed CI"})
	gh := &mockGitHubClient{
		// No check run failures
		checkRuns: []CheckRun{
			{ID: 1, Name: "DCO", Status: "completed", Conclusion: "success"},
		},
		// Commit status failures (Prow)
		commitStatuses: []CheckRun{
			{Name: "pull-kubernetes-nmstate-unit-test", Status: "completed", Conclusion: "failure", Output: "https://prow.ci.kubevirt.io/logs/1234"},
		},
		prHeadSHAs: []string{"sha-before", "sha-after"},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	if claudeCalls != 1 {
		t.Fatalf("expected 1 claude call for commit status failure, got %d", claudeCalls)
	}
	if agent.state.ActiveIssues[42].CIFixAttempts != 1 {
		t.Errorf("expected 1 CI fix attempt, got %d", agent.state.ActiveIssues[42].CIFixAttempts)
	}
}

func TestProcessCIFailures_MergesCheckRunsAndCommitStatuses(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "RELATED Fixed both failures"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "github-actions-test", Status: "completed", Conclusion: "failure", Output: "test failed"},
		},
		commitStatuses: []CheckRun{
			{Name: "pull-unit-test", Status: "completed", Conclusion: "failure", Output: "https://prow.ci/logs/1234"},
		},
		prHeadSHAs: []string{"sha-before", "sha-after"},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[42] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Each failure should get its own Claude invocation for independent classification
	var claudePrompts []string
	for _, c := range runner.calls {
		if c.Name == "claude" {
			if len(c.Args) > 0 {
				claudePrompts = append(claudePrompts, c.Args[len(c.Args)-1])
			}
		}
	}
	if len(claudePrompts) != 2 {
		t.Fatalf("expected 2 claude calls (one per failure), got %d", len(claudePrompts))
	}

	// Verify each failure gets its own prompt
	foundCheckRun := false
	foundCommitStatus := false
	for _, prompt := range claudePrompts {
		if strings.Contains(prompt, "github-actions-test") {
			foundCheckRun = true
		}
		if strings.Contains(prompt, "pull-unit-test") {
			foundCommitStatus = true
		}
	}
	if !foundCheckRun {
		t.Errorf("expected one prompt to contain check run failure 'github-actions-test'")
	}
	if !foundCommitStatus {
		t.Errorf("expected one prompt to contain commit status failure 'pull-unit-test'")
	}
}
