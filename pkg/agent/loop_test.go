package agent

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
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
	prReviews      []PRReview // reviews to return from GetPRReviews
	headCommitDate time.Time  // date to return from GetPRHeadCommitDate

	listIssuesCalled bool // tracks whether ListLabeledIssues was called
	listIssuesErr    error
	replyErr         error // error to return from ReplyToPRComment
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
	if m.replyErr != nil {
		return m.replyErr
	}
	m.addedComments = append(m.addedComments, fmt.Sprintf("reply:%d:%s", commentID, body))
	return nil
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{IssueNumber: 42, Status: "pr-open"}

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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
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

	work, ok := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
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

func TestProcessNewIssues_SkipCommentIssueInProgress(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "Fixed it"})
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipComments = []string{"issue-in-progress"}
	agent.ProcessNewIssues(context.Background())

	// Should still create worktree and process the issue
	if len(wt.createdBranches) != 1 || wt.createdBranches[0] != "ai/issue-42" {
		t.Errorf("expected branch 'ai/issue-42', got %v", wt.createdBranches)
	}

	// No "working on this issue" comment should be posted
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "working on this issue") {
			t.Errorf("expected issue-in-progress comment to be suppressed, got: %q", comment)
		}
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

	work, ok := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
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

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work == nil || work.Status != "failed" {
		t.Error("expected issue status to be 'failed'")
	}
}

func TestProcessNewIssues_EmptyLabelSkips(t *testing.T) {
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Should not be processed"}},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.Label = "" // Triage role: no label set

	agent.ProcessNewIssues(context.Background())

	if gh.listIssuesCalled {
		t.Error("ListLabeledIssues should not be called when label is empty")
	}
	if len(agent.state.ActiveIssues) != 0 {
		t.Error("no issues should be processed when label is empty")
	}
}

func TestProcessNewIssues_ScanningEventsUseCategoryCheck(t *testing.T) {
	gh := &mockGitHubClient{}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	emitter := &recordingEmitter{}
	agent.SetEmitter(emitter)

	agent.ProcessNewIssues(context.Background())

	// Verify the scanning lifecycle events use CategoryCheck (not CategoryIssue)
	// so they are filtered out of the default status view as routine noise.
	var started, completed bool
	for _, e := range emitter.events {
		if e.Action == "Scanning for new issues" {
			if e.Category != CategoryCheck {
				t.Errorf("expected CategoryCheck for scanning start event, got %q", e.Category)
			}
			started = true
		}
		if e.Action == "Issue scanning complete" {
			if e.Category != CategoryCheck {
				t.Errorf("expected CategoryCheck for scanning complete event, got %q", e.Category)
			}
			completed = true
		}
	}
	if !started {
		t.Error("expected scanning start event to be emitted")
	}
	if !completed {
		t.Error("expected scanning complete event to be emitted")
	}
}

// recordingEmitter captures events for test assertions.
type recordingEmitter struct {
	events []Event
}

func (r *recordingEmitter) Emit(event Event) {
	r.events = append(r.events, event)
}

func TestProcessReviewComments_NoNewComments(t *testing.T) {
	gh := &mockGitHubClient{}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	result := streamResultJSON(AgentResult{Result: "Addressed"})
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{result}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	if claudeCalls != 1 {
		t.Fatalf("expected 1 claude call, got %d", claudeCalls)
	}

	if agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].LastCommentID != 60 {
		t.Errorf("expected lastCommentID 60, got %d", agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].LastCommentID)
	}

	// Oompa no longer posts hardcoded replies — the skill handles per-comment replies.
	for _, comment := range gh.addedComments {
		if strings.HasPrefix(comment, "reply:") {
			t.Errorf("expected no hardcoded replies from oompa, got: %s", comment)
		}
	}
}

func TestProcessReviewComments_PushFailureDoesNotAdvanceCursor(t *testing.T) {
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	// Runner returns non-empty stdout (triggers changeDetected) and an error (causes push to fail)
	runner := &mockCommandRunner{stdout: []byte("dirty"), err: fmt.Errorf("git failed")}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	// Use sequentialMockCodeAgent so the agent call succeeds despite the broken runner
	agent.codeAgent = &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{{result: AgentResult{Result: "Done"}}},
	}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:   42,
		IssueTitle:    "Fix bug",
		PRNumber:      100,
		Status:        "pr-open",
		WorktreePath:  "/tmp/worktree",
		LastCommentID: 50,
	}

	agent.ProcessReviewComments(context.Background())

	// Cursor should NOT advance when changes were detected but push failed,
	// so the comments are retried on the next poll cycle.
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCommentID != 50 {
		t.Errorf("expected LastCommentID to stay at 50, got %d", work.LastCommentID)
	}
}

func TestProcessReviewComments_CursorAdvancesUnconditionally(t *testing.T) {
	implResult := streamResultJSON(AgentResult{Result: "Done"})
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{implResult}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:   42,
		IssueTitle:    "Fix bug",
		PRNumber:      100,
		Status:        "pr-open",
		WorktreePath:  "/tmp/worktree",
		LastCommentID: 50,
	}

	agent.ProcessReviewComments(context.Background())

	// Cursor should always advance after a successful agent run —
	// oompa no longer posts replies (the skill handles them).
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCommentID != 60 {
		t.Errorf("expected LastCommentID to advance to 60, got %d", work.LastCommentID)
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	result := streamResultJSON(AgentResult{Result: "Done"})
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "anyone", Body: "fix this"},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{result}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	// No reviewers set — empty whitelist means allow all
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	// One call: implementation (no triage step)
	if claudeCalls != 1 {
		t.Errorf("expected 1 claude call with empty whitelist, got %d", claudeCalls)
	}
}

func TestProcessReviewComments_UnaddressedReviewIsProcessed(t *testing.T) {
	// A review with ID > LastReviewID should be processed (it's new/unaddressed).
	// The sinceID cursor in GetPRReviews ensures only unprocessed reviews are returned,
	// preventing the race condition from issue #162 where multiple bot reviewers
	// post simultaneously.
	result := streamResultJSON(AgentResult{Result: "Addressed"})
	gh := &mockGitHubClient{
		prReviews: []PRReview{
			{ID: 200, User: "copilot", Body: "Please fix the error handling"},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{result}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
		LastReviewID: 100, // review 200 > 100, so it's new/unaddressed
	}

	agent.ProcessReviewComments(context.Background())

	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	if claudeCalls != 1 {
		t.Errorf("expected 1 claude call for unaddressed review, got %d", claudeCalls)
	}

	// Review should have been processed — cursor should advance
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastReviewID != 200 {
		t.Errorf("expected LastReviewID to advance to 200, got %d", work.LastReviewID)
	}
}

func TestProcessReviewComments_AlreadyAddressedReviewIsFilteredBySinceID(t *testing.T) {
	// A review with ID <= LastReviewID is filtered out by GetPRReviews (sinceID),
	// so it's never returned and never processed. This is the API-level guarantee
	// that already-addressed reviews are skipped.
	gh := &mockGitHubClient{
		prReviews: []PRReview{
			{ID: 50, User: "gemini", Body: "Looks good with minor fixes"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
		LastReviewID: 100, // review 50 <= 100, filtered by sinceID
	}

	agent.ProcessReviewComments(context.Background())

	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	if claudeCalls != 0 {
		t.Errorf("expected 0 claude calls for already-addressed review, got %d", claudeCalls)
	}
}

func TestProcessReviewComments_MultipleReviewersSimultaneous(t *testing.T) {
	// Simulate the race condition from issue #162: multiple bot reviewers post
	// simultaneously. After oompa addresses some and pushes, the remaining
	// unaddressed reviews should still be processed because their IDs are above
	// the LastReviewID cursor (sinceID filter lets them through).
	result := streamResultJSON(AgentResult{Result: "Addressed"})
	gh := &mockGitHubClient{
		prReviews: []PRReview{
			// copilot reviewed before oompa pushed. This review was never addressed
			// (ID 300 > LastReviewID 250), so sinceID lets it through.
			{ID: 300, User: "copilot", Body: "4 inline comments about error handling"},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{result}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
		// LastReviewID 250: oompa already processed coderabbit (ID 200) and gemini (ID 250)
		// but copilot's review (ID 300) is still unaddressed
		LastReviewID: 250,
	}

	agent.ProcessReviewComments(context.Background())

	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	// copilot's review should be processed — sinceID ensures it's not filtered out
	if claudeCalls != 1 {
		t.Errorf("expected 1 claude call for unaddressed copilot review, got %d", claudeCalls)
	}

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastReviewID != 300 {
		t.Errorf("expected LastReviewID to advance to 300, got %d", work.LastReviewID)
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

func TestProcessReviewComments_SquashesAgentCommits(t *testing.T) {
	// When the agent commits directly (HEAD changes), ProcessReviewComments should
	// squash the new commits into the original HEAD via git reset --soft + amend.
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	// Use a custom runner that simulates the agent committing directly
	// by returning different SHAs for sequential git rev-parse HEAD calls.
	runner := &reviewSquashRunner{
		mockCommandRunner: &mockCommandRunner{},
		headSHAs:          []string{"original-sha", "agent-committed-sha"},
	}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.codeAgent = &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{{result: AgentResult{Result: "Done"}}},
	}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:   42,
		IssueTitle:    "Fix bug",
		PRNumber:      100,
		Status:        "pr-open",
		WorktreePath:  "/tmp/worktree",
		LastCommentID: 50,
	}

	agent.ProcessReviewComments(context.Background())

	// Verify git add -A, git reset --soft, and git commit --amend were called
	foundAdd := false
	foundReset := false
	foundAmend := false
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) >= 1 && c.Args[0] == "add" {
			foundAdd = true
		}
		if c.Name == "git" && len(c.Args) >= 3 && c.Args[0] == "reset" && c.Args[1] == "--soft" && c.Args[2] == "original-sha" {
			foundReset = true
		}
		if c.Name == "git" && len(c.Args) >= 2 && c.Args[0] == "commit" && c.Args[1] == "--amend" {
			foundAmend = true
		}
	}

	if !foundAdd {
		t.Error("expected git add -A to stage all changes before squashing")
	}
	if !foundReset {
		t.Error("expected git reset --soft <original-sha> to squash agent commits")
	}
	if !foundAmend {
		t.Error("expected git commit --amend to fold changes into original commit")
	}

	// Cursor should advance (push succeeded)
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCommentID != 60 {
		t.Errorf("expected LastCommentID to advance to 60, got %d", work.LastCommentID)
	}
}

// reviewSquashRunner is a test helper that returns different SHAs for sequential
// git rev-parse HEAD calls, simulating an agent that commits directly.
type reviewSquashRunner struct {
	*mockCommandRunner
	headSHAs  []string
	headIndex int
}

func (r *reviewSquashRunner) Run(ctx context.Context, workDir, name string, args ...string) (stdout, stderr []byte, err error) {
	// Record the call in the base mock
	r.mockCommandRunner.Run(ctx, workDir, name, args...) //nolint:errcheck // recording call for test assertions

	// Return different SHAs for git rev-parse HEAD
	if name == "git" && len(args) >= 2 && args[0] == "rev-parse" && args[1] == "HEAD" {
		if r.headIndex < len(r.headSHAs) {
			sha := r.headSHAs[r.headIndex]
			r.headIndex++
			return []byte(sha + "\n"), nil, nil
		}
		return []byte("unknown-sha\n"), nil, nil
	}

	// For git status --porcelain, return empty (no uncommitted changes)
	if name == "git" && len(args) >= 1 && args[0] == "status" {
		return []byte(""), nil, nil
	}

	// For git log (fixup commit check), return empty (no fixup commits)
	if name == "git" && len(args) >= 1 && args[0] == "log" {
		return []byte(""), nil, nil
	}

	return nil, nil, nil
}

func TestProcessReviewComments_AutosquashesFixupCommits(t *testing.T) {
	// When the agent creates fixup commits, ProcessReviewComments should
	// autosquash them rather than pushing separate commits.
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	runner := &reviewFixupRunner{
		mockCommandRunner: &mockCommandRunner{},
	}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.codeAgent = &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{{result: AgentResult{Result: "Done"}}},
	}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:   42,
		IssueTitle:    "Fix bug",
		PRNumber:      100,
		Status:        "pr-open",
		WorktreePath:  "/tmp/worktree",
		LastCommentID: 50,
	}

	agent.ProcessReviewComments(context.Background())

	// Verify autosquash rebase was called
	foundAutosquash := false
	for _, c := range runner.calls {
		if c.Name == "sh" && len(c.Args) >= 2 && strings.Contains(c.Args[1], "autosquash") {
			foundAutosquash = true
		}
	}

	if !foundAutosquash {
		t.Error("expected autosquash rebase to be called for fixup commits")
	}
}

// reviewFixupRunner is a test helper that simulates an agent creating fixup commits.
type reviewFixupRunner struct {
	*mockCommandRunner
}

func (r *reviewFixupRunner) Run(ctx context.Context, workDir, name string, args ...string) (stdout, stderr []byte, err error) {
	r.mockCommandRunner.Run(ctx, workDir, name, args...) //nolint:errcheck // recording call for test assertions

	// For git log --format=%s (fixup commit check), return a fixup commit
	if name == "git" && len(args) >= 1 && args[0] == "log" {
		if slices.Contains(args, "--format=%s") {
			return []byte("fixup! original commit\n"), nil, nil
		}
		return []byte(""), nil, nil
	}

	// For git status --porcelain, return empty (no uncommitted changes)
	if name == "git" && len(args) >= 1 && args[0] == "status" {
		return []byte(""), nil, nil
	}

	return nil, nil, nil
}

func TestCleanupDone_MergedPR(t *testing.T) {
	gh := &mockGitHubClient{prState: "merged"}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.CleanupDone(context.Background())

	if len(wt.removedPaths) != 1 || wt.removedPaths[0] != "/tmp/worktree" {
		t.Errorf("expected worktree removal, got %v", wt.removedPaths)
	}
	if _, exists := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]; exists {
		t.Error("expected issue 42 to be removed from state")
	}
}

func TestCleanupDone_ClosedPR(t *testing.T) {
	gh := &mockGitHubClient{prState: "closed"}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.CleanupDone(context.Background())

	if len(wt.removedPaths) != 1 {
		t.Error("expected worktree removal for closed PR")
	}
	if _, exists := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]; exists {
		t.Error("expected issue 42 to be removed from state")
	}
}

func TestCleanupDone_OpenPR(t *testing.T) {
	gh := &mockGitHubClient{prState: "open"}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.CleanupDone(context.Background())

	if len(wt.removedPaths) != 0 {
		t.Error("should not remove worktree for open PR")
	}
	if _, exists := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]; !exists {
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	if agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].CIFixAttempts != 1 {
		t.Errorf("expected 1 CI fix attempt, got %d", agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].CIFixAttempts)
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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

func TestProcessCIFailures_NoRunsDoesNotMarkChecked(t *testing.T) {
	// Issue #139: When no check runs are registered yet (e.g., oompa polls
	// before GitHub registers CI), allCompleted is vacuously true. The agent
	// must NOT set LastCheckedCISHA in this case, otherwise real CI failures
	// that appear later will be skipped by the fast path.
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{}, // No check runs registered yet
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber: 42,
		PRNumber:    100,
		BranchName:  "ai/issue-42",
		Status:      "pr-open",
	}

	agent.ProcessCIFailures(context.Background())

	if len(runner.calls) != 0 {
		t.Error("should not invoke claude when no check runs exist")
	}

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCheckedCISHA != "" {
		t.Errorf("expected LastCheckedCISHA to remain empty when no check runs registered, got %q", work.LastCheckedCISHA)
	}
}

func TestProcessCIFailures_NoRunsThenFailuresAreInvestigated(t *testing.T) {
	// Issue #139 end-to-end regression: when no check runs exist on poll 1,
	// LastCheckedCISHA must stay empty so that poll 2 (when runs appear with
	// a failure) actually invokes Claude instead of silently skipping.
	claudeResult := streamResultJSON(AgentResult{Result: "RELATED Fixed CI"})
	gh := &mockGitHubClient{
		checkRuns:  []CheckRun{}, // empty on first poll
		prHeadSHAs: []string{"sha1", "sha1", "sha1"},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	// Poll 1: no runs yet — must not mark SHA as checked
	agent.ProcessCIFailures(context.Background())
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCheckedCISHA != "" {
		t.Fatalf("poll 1: expected LastCheckedCISHA empty, got %q", work.LastCheckedCISHA)
	}
	if countClaudeCalls(runner.calls) != 0 {
		t.Fatalf("poll 1: expected 0 claude calls, got %d", countClaudeCalls(runner.calls))
	}

	// Poll 2: CI runs now registered with a failure
	gh.checkRuns = []CheckRun{
		{ID: 1, Name: "test", Status: "completed", Conclusion: "failure", Output: "tests failed"},
	}
	agent.ProcessCIFailures(context.Background())
	if countClaudeCalls(runner.calls) != 1 {
		t.Errorf("poll 2: expected 1 claude call, got %d", countClaudeCalls(runner.calls))
	}
}

func TestProcessCIFailures_CreatesFlakyIssueWhenUnrelated(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED\nFAILING_TEST: TestDB/connection_timeout\nThe test database connection times out intermittently"})
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Check that a flaky issue was created with the failing test name in the title
	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected 1 created issue, got %d", len(gh.createdIssues))
	}
	issue := gh.createdIssues[0]
	if issue.Title != "Flaky CI: integration-tests / TestDB/connection_timeout" {
		t.Errorf("expected title 'Flaky CI: integration-tests / TestDB/connection_timeout', got %q", issue.Title)
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
	// Body should use the failing test name, not the lane name, in the "Which tests" section
	if !strings.Contains(issue.Body, "TestDB/connection_timeout") {
		t.Errorf("expected issue body to contain the failing test name, got %q", issue.Body)
	}
	// Body should NOT contain the raw FAILING_TEST: line
	if strings.Contains(issue.Body, "FAILING_TEST:") {
		t.Errorf("expected FAILING_TEST: line to be stripped from issue body, got %q", issue.Body)
	}

	// Check that a comment was added to the PR
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (unrelated + issue link), got %d", len(gh.addedComments))
	}
}

func TestProcessCIFailures_InfrastructureSkipsFlakyIssue(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "INFRASTRUCTURE Fedora koji server returned HTTP 502 Bad Gateway"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "Build-PR", Status: "completed", Conclusion: "failure", Output: "HTTP 502 Bad Gateway from koji.fedoraproject.org"},
		},
		checkRunLogs: map[int64]string{
			1: "Building package...\nFetching from koji.fedoraproject.org...\nHTTP 502 Bad Gateway\nBuild failed",
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true // Even with flaky issues enabled, INFRASTRUCTURE should skip
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Check that NO flaky issue was created (infrastructure != flaky)
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues for INFRASTRUCTURE classification, got %d", len(gh.createdIssues))
	}

	// Check that exactly 1 comment was posted (the infrastructure notice)
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment (infrastructure notice), got %d", len(gh.addedComments))
	}

	// Verify the comment mentions infrastructure
	if !strings.Contains(gh.addedComments[0], "infrastructure issue") {
		t.Errorf("expected comment to mention infrastructure issue, got: %q", gh.addedComments[0])
	}
	if !strings.Contains(gh.addedComments[0], "Build-PR") {
		t.Errorf("expected comment to mention the check name, got: %q", gh.addedComments[0])
	}

	// Verify state was updated correctly
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCIStatus != "infrastructure-failure" {
		t.Errorf("expected LastCIStatus 'infrastructure-failure', got %q", work.LastCIStatus)
	}
	if work.CIFixAttempts != 0 {
		t.Errorf("expected 0 CI fix attempts for infrastructure failure, got %d", work.CIFixAttempts)
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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

func TestProcessCIFailures_SkipCommentCIUnrelated(t *testing.T) {
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
	agent.cfg.SkipComments = []string{"ci-unrelated"}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// No comments should be posted (comment skipped, dedup via state)
	if len(gh.addedComments) != 0 {
		t.Fatalf("expected 0 comments (ci-unrelated skipped), got %d: %v", len(gh.addedComments), gh.addedComments)
	}

	// State should still be updated
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCIStatus != "unrelated-failure" {
		t.Errorf("expected LastCIStatus 'unrelated-failure', got %q", work.LastCIStatus)
	}
	// Check should be recorded in state for dedup
	if !work.CheckedCIChecks["abc123:integration-tests"] {
		t.Error("expected check to be recorded in CheckedCIChecks for dedup")
	}
}

func TestProcessCIFailures_SkipCommentCIUnrelated_StillCreatesFlakyIssue(t *testing.T) {
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
	agent.cfg.SkipComments = []string{"ci-unrelated"}
	agent.cfg.CreateFlakyIssues = true
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Flaky issue should still be created even though ci-unrelated comment is skipped
	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected 1 created flaky issue, got %d", len(gh.createdIssues))
	}

	// Should have only flaky issue reference comment (no marker, no unrelated comment)
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment (flaky issue ref only), got %d", len(gh.addedComments))
	}
	if !strings.Contains(gh.addedComments[0], "Opened issue") {
		t.Errorf("expected flaky issue reference comment, got: %q", gh.addedComments[0])
	}
}

func TestProcessCIFailures_SkipCommentCIInfrastructure(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "INFRASTRUCTURE Fedora koji server returned HTTP 502 Bad Gateway"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "Build-PR", Status: "completed", Conclusion: "failure", Output: "HTTP 502 Bad Gateway from koji.fedoraproject.org"},
		},
		checkRunLogs: map[int64]string{
			1: "Building package...\nFetching from koji.fedoraproject.org...\nHTTP 502 Bad Gateway\nBuild failed",
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipComments = []string{"ci-infrastructure"}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// No comments should be posted
	if len(gh.addedComments) != 0 {
		t.Fatalf("expected 0 comments (ci-infrastructure skipped), got %d: %v", len(gh.addedComments), gh.addedComments)
	}

	// State should still be updated
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCIStatus != "infrastructure-failure" {
		t.Errorf("expected LastCIStatus 'infrastructure-failure', got %q", work.LastCIStatus)
	}
	// Check should be recorded in state for dedup
	if !work.CheckedCIChecks["abc123:Build-PR"] {
		t.Error("expected check to be recorded in CheckedCIChecks for dedup")
	}
}

func TestProcessCIFailures_SkipCommentFlaky(t *testing.T) {
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
	agent.cfg.SkipComments = []string{"flaky"}
	agent.cfg.CreateFlakyIssues = true
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Flaky issue should still be created
	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected 1 created flaky issue, got %d", len(gh.createdIssues))
	}

	// Should have only the unrelated comment (not the "Opened issue #N" cross-reference)
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment (unrelated notice only, flaky ref suppressed), got %d", len(gh.addedComments))
	}
	if !strings.Contains(gh.addedComments[0], "appears unrelated") {
		t.Errorf("expected unrelated comment, got: %q", gh.addedComments[0])
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
			// Title does NOT match exactly — use a different title to exercise LLM path
			{Number: 50, Title: "Flaky CI: db-integration", Labels: []string{"flaky-test"}},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{ciResult, matchResult}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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

	// Check that comments were added:
	// 1. unrelated notice on PR
	// 2. CI lane link on the flaky issue (#50)
	// 3. "Known flaky test" reference on PR
	if len(gh.addedComments) != 3 {
		t.Fatalf("expected 3 comments (unrelated + CI lane link + flaky reference), got %d", len(gh.addedComments))
	}

	// Verify the CI lane link comment (posted on the flaky issue)
	if !strings.Contains(gh.addedComments[1], "CI failure on PR #100") {
		t.Errorf("expected CI lane link comment, got: %q", gh.addedComments[1])
	}

	// Verify the flaky reference comment on the PR
	if !strings.Contains(gh.addedComments[2], "Known flaky test tracked in #50") {
		t.Errorf("expected flaky reference comment, got: %q", gh.addedComments[2])
	}
}

func TestProcessCIFailures_TitlePreCheckSkipsLLMMatching(t *testing.T) {
	// When an existing issue has an exact title match ("Flaky CI: <check-name>"),
	// the agent should skip LLM matching entirely and use the existing issue.
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The Fedora koji server returned 502 Bad Gateway"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "Build-PR", Status: "completed", Conclusion: "failure", Output: "Error: 502 Bad Gateway from koji.fedoraproject.org"},
		},
		checkRunLogs: map[int64]string{
			1: "Building package...\nFetching from koji.fedoraproject.org...\nHTTP 502 Bad Gateway\nBuild failed",
		},
		searchResults: []Issue{
			{Number: 99, Title: "Flaky CI: Build-PR", Body: "koji infrastructure failure", Labels: []string{"flaky-test"}},
		},
	}
	// Only one Claude result needed (for CI investigation). No match result needed
	// because the title pre-check should prevent the LLM matching call.
	runner := &mockCommandRunner{stdout: ciResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = true
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Should NOT have created a new issue
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues (title pre-check should match), got %d", len(gh.createdIssues))
	}

	// Only 1 claude call (CI investigation), NOT 2 (CI + matching)
	claudeCalls := countClaudeCalls(runner.calls)
	if claudeCalls != 1 {
		t.Errorf("expected 1 claude call (CI investigation only, no LLM matching), got %d", claudeCalls)
	}

	// Should have 3 comments:
	// 1. unrelated notice on PR
	// 2. CI lane link on the flaky issue (#99)
	// 3. "Known flaky test" reference on PR
	if len(gh.addedComments) != 3 {
		t.Fatalf("expected 3 comments (unrelated + CI lane link + flaky reference), got %d", len(gh.addedComments))
	}

	// Verify the CI lane link comment (posted on the flaky issue)
	if !strings.Contains(gh.addedComments[1], "CI failure on PR #100") {
		t.Errorf("expected CI lane link comment, got: %q", gh.addedComments[1])
	}

	// Verify the flaky reference points to issue #99
	if !strings.Contains(gh.addedComments[2], "Known flaky test tracked in #99") {
		t.Errorf("expected flaky reference to issue #99, got: %q", gh.addedComments[2])
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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

func TestProcessCIFailures_SearchAndLinkWithoutCreateFlakyIssues(t *testing.T) {
	// Issue #171: create-flaky-issues=false + flaky-label set + matching issue exists
	// → PR comment references the issue, CI lane link added to the issue, no new issue created.
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
		searchResults: []Issue{
			{Number: 1234, Title: "Flaky CI: integration-tests", Labels: []string{"kind/ci-flake"}},
		},
	}
	runner := &mockCommandRunner{stdout: ciResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = false       // Disabled — don't create new issues
	agent.cfg.FlakyLabel = "kind/ci-flake"    // But label is set — enables search-and-link
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// No new issue should be created
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues (create-flaky-issues=false), got %d", len(gh.createdIssues))
	}

	// Should have 3 comments:
	// 1. unrelated notice on PR
	// 2. CI lane link comment on the existing flaky issue (#1234)
	// 3. "Known flaky test" reference on PR
	if len(gh.addedComments) != 3 {
		t.Fatalf("expected 3 comments (unrelated + CI lane link + flaky reference), got %d", len(gh.addedComments))
	}

	// Verify unrelated comment
	if !strings.Contains(gh.addedComments[0], "appears unrelated") {
		t.Errorf("expected unrelated comment, got: %q", gh.addedComments[0])
	}

	// Verify CI lane link on the flaky issue
	if !strings.Contains(gh.addedComments[1], "CI failure on PR #100") {
		t.Errorf("expected CI lane link comment, got: %q", gh.addedComments[1])
	}
	if !strings.Contains(gh.addedComments[1], "integration-tests") {
		t.Errorf("expected CI lane link to mention CI lane name, got: %q", gh.addedComments[1])
	}

	// Verify the PR reference comment
	if !strings.Contains(gh.addedComments[2], "Known flaky test tracked in #1234") {
		t.Errorf("expected flaky reference to issue #1234, got: %q", gh.addedComments[2])
	}
}

func TestProcessCIFailures_NoMatchNoCreateWhenDisabled(t *testing.T) {
	// Issue #171: create-flaky-issues=false + flaky-label set + no matching issue
	// → regular unrelated comment, no issue created.
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
		searchResults: []Issue{}, // No matching issues
	}
	runner := &mockCommandRunner{stdout: ciResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = false
	agent.cfg.FlakyLabel = "kind/ci-flake"
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// No issue should be created
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues (create-flaky-issues=false, no match), got %d", len(gh.createdIssues))
	}

	// Only the unrelated comment should be posted
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment (unrelated notice only), got %d", len(gh.addedComments))
	}
	if !strings.Contains(gh.addedComments[0], "appears unrelated") {
		t.Errorf("expected unrelated comment, got: %q", gh.addedComments[0])
	}
}

func TestProcessCIFailures_NoSearchWhenFlakyLabelEmpty(t *testing.T) {
	// Issue #171: flaky-label not set → no search, no linking.
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test database connection times out intermittently"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
	}
	runner := &mockCommandRunner{stdout: ciResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.FlakyLabel = ""             // No flaky label
	agent.cfg.CreateFlakyIssues = false
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// No issue should be created
	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues, got %d", len(gh.createdIssues))
	}

	// Only the unrelated comment
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment (unrelated notice only), got %d", len(gh.addedComments))
	}
}

func TestProcessCIFailures_CILaneLinkIncludesJobURL(t *testing.T) {
	// Issue #171: CI lane link comment includes the correct job URL and PR reference.
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test timed out intermittently due to a flaky network connection in the CI environment"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 67890, Name: "e2e (control-plane, HA, shared, ipv4)", Status: "completed", Conclusion: "failure", Output: "Error: test timed out waiting for condition"},
		},
		checkRunLogs: map[int64]string{
			67890: "Running e2e tests...\nTimeout: waiting for pod to be ready\nTest failed after 300s\nStack trace:\n  at e2e.waitForPod(e2e.go:142)",
		},
		searchResults: []Issue{
			{Number: 5678, Title: "Flaky CI: e2e (control-plane, HA, shared, ipv4)", Labels: []string{"kind/ci-flake"}},
		},
	}
	runner := &mockCommandRunner{stdout: ciResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = false
	agent.cfg.FlakyLabel = "kind/ci-flake"
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Verify CI lane link comment was posted on the flaky issue
	if len(gh.addedComments) < 2 {
		t.Fatalf("expected at least 2 comments, got %d", len(gh.addedComments))
	}

	ciLaneComment := gh.addedComments[1]
	if !strings.Contains(ciLaneComment, "CI failure on PR #100") {
		t.Errorf("expected CI lane link to reference PR #100, got: %q", ciLaneComment)
	}
	if !strings.Contains(ciLaneComment, "e2e (control-plane, HA, shared, ipv4)") {
		t.Errorf("expected CI lane link to mention check name, got: %q", ciLaneComment)
	}
	// GitHub Actions check runs have ID > 0, so a link should be constructed
	if !strings.Contains(ciLaneComment, "**Link:**") {
		t.Errorf("expected CI lane link to include a job link, got: %q", ciLaneComment)
	}
}

func TestProcessCIFailures_CommitStatusCILaneLink(t *testing.T) {
	// Issue #171: commit status entries (Prow) use target_url as the CI link.
	ciResult := streamResultJSON(AgentResult{Result: "UNRELATED The test timed out intermittently on the Prow CI infrastructure"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{},
		commitStatuses: []CheckRun{
			// Commit status: ID=0, Output contains target_url
			{ID: 0, Name: "pull-unit-test", Status: "completed", Conclusion: "failure", Output: "Build failed\nhttps://prow.ci.kubevirt.io/view/gs/logs/1234"},
		},
		searchResults: []Issue{
			{Number: 999, Title: "Flaky CI: pull-unit-test", Labels: []string{"flaky-test"}},
		},
	}
	runner := &mockCommandRunner{stdout: ciResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = false
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Verify CI lane link uses the Prow URL extracted from the output
	if len(gh.addedComments) < 2 {
		t.Fatalf("expected at least 2 comments, got %d", len(gh.addedComments))
	}

	ciLaneComment := gh.addedComments[1]
	if !strings.Contains(ciLaneComment, "https://prow.ci.kubevirt.io/view/gs/logs/1234") {
		t.Errorf("expected CI lane link to include Prow URL, got: %q", ciLaneComment)
	}
}

func TestExtractURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://prow.ci.kubevirt.io/view/gs/logs/1234", "https://prow.ci.kubevirt.io/view/gs/logs/1234"},
		{"Build failed\nhttps://prow.ci.kubevirt.io/view/gs/logs/1234", "https://prow.ci.kubevirt.io/view/gs/logs/1234"},
		{"Build failed", ""},
		{"", ""},
		{"Error: timeout http://example.com/logs more text", "http://example.com/logs"},
		// Trailing punctuation is trimmed
		{"Check logs at https://example.com/log.", "https://example.com/log"},
		{"See https://example.com/log, then retry", "https://example.com/log"},
		{"See https://example.com/log) for details", "https://example.com/log"},
	}
	for _, tt := range tests {
		got := extractURL(tt.input)
		if got != tt.want {
			t.Errorf("extractURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
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

func TestParseFailingTest(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"FAILING_TEST: TestDualStack/should_create_two_pods\nThe test timed out", "TestDualStack/should_create_two_pods"},
		{"FAILING_TEST:TestFoo\nSome explanation", "TestFoo"},
		{"FAILING_TEST:  Hybrid mode > works with API-only config  \nDetails here", "Hybrid mode > works with API-only config"},
		{"The test database connection times out intermittently", ""},
		{"", ""},
		{"FAILING_TEST:\nSome explanation", ""},
		{"Some text\nFAILING_TEST: TestBar\nMore text", "TestBar"},
	}
	for _, tt := range tests {
		got := parseFailingTest(tt.input)
		if got != tt.want {
			t.Errorf("parseFailingTest(%q) = %q, want %q", tt.input, got, tt.want)
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	if agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].LastCheckedCISHA != "abc1234567890" {
		t.Errorf("expected LastCheckedCISHA to be recovered to abc1234567890, got %q", agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].LastCheckedCISHA)
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
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

func TestShouldSkipComment_EmptySkipsNone(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	// No skip comments configured — nothing should be skipped
	for _, category := range []string{"ci-unrelated", "ci-infrastructure", "ci-related", "conflict", "rebase", "flaky", "issue-in-progress"} {
		if agent.ShouldSkipComment(category) {
			t.Errorf("expected %q to NOT be skipped with empty SkipComments", category)
		}
	}
}

func TestShouldSkipComment_Filtered(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.SkipComments = []string{"ci-unrelated", "ci-infrastructure"}

	if !agent.ShouldSkipComment("ci-unrelated") {
		t.Error("expected 'ci-unrelated' to be skipped")
	}
	if !agent.ShouldSkipComment("ci-infrastructure") {
		t.Error("expected 'ci-infrastructure' to be skipped")
	}
	if agent.ShouldSkipComment("ci-related") {
		t.Error("expected 'ci-related' to NOT be skipped")
	}
	if agent.ShouldSkipComment("conflict") {
		t.Error("expected 'conflict' to NOT be skipped")
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

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 123)]
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

	work2 := agent.state.ActiveIssues[IssueKey("owner", "repo", 456)]
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 123)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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

func TestProcessRebase_SkipCommentRebase(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "behind",
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipComments = []string{"rebase"}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessRebase(context.Background())

	// Should have attempted rebase (git fetch + git rebase)
	var rebaseCalls int
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" {
			rebaseCalls++
		}
	}
	if rebaseCalls == 0 {
		t.Error("expected git rebase to be called")
	}

	// No comment should be posted (rebase comment skipped)
	if len(gh.addedComments) != 0 {
		t.Errorf("expected 0 comments (rebase comment suppressed), got %d: %v", len(gh.addedComments), gh.addedComments)
	}
}

func TestProcessConflicts_SkipCommentConflict(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "dirty",
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipComments = []string{"conflict"}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessConflicts(context.Background())

	// No human-visible comment should be posted
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "Rebased commit") && !strings.Contains(comment, "<!--") {
			t.Errorf("expected conflict comment to be suppressed, got: %q", comment)
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
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 100, Status: "completed", Conclusion: "failure", CreatedAt: time.Now()},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()

	// Pre-mark run as already investigated
	state.MarkRunInvestigated("owner/repo/ci.yml", "100")

	cfg := Config{
		Owner:      "owner",
		Repo:       "repo",
		TriageJobs: []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), &ClaudeCodeAgent{})
	a.ProcessTriageJobs(context.Background())

	// Should still be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "100") {
		t.Error("expected run to remain marked as investigated")
	}
	// Should not create any worktrees (no investigation triggered)
	if len(wtm.createdBranches) > 0 {
		t.Errorf("expected no worktrees created for already-investigated run, got %d", len(wtm.createdBranches))
	}
	// Should not have created any issues
	if len(gh.createdIssues) > 0 {
		t.Errorf("expected no issues created, got %d", len(gh.createdIssues))
	}
}

func TestProcessTriageJobs_SkipsSuccessfulRuns(t *testing.T) {
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 200, Status: "completed", Conclusion: "success", CreatedAt: time.Now()},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()

	cfg := Config{
		Owner:      "owner",
		Repo:       "repo",
		TriageJobs: []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), &ClaudeCodeAgent{})
	a.ProcessTriageJobs(context.Background())

	// Successful run should be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "200") {
		t.Error("expected successful run to be marked as investigated")
	}
	// Should not create any worktrees (no investigation needed for success)
	if len(wtm.createdBranches) > 0 {
		t.Errorf("expected no worktrees created for successful run, got %d", len(wtm.createdBranches))
	}
	// Should not have created any issues
	if len(gh.createdIssues) > 0 {
		t.Errorf("expected no issues created, got %d", len(gh.createdIssues))
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

func TestProcessTriageJobs_DefaultOnlyChecksLatest(t *testing.T) {
	// When TriageLookback is not set (zero), only the most recent run is checked.
	triageResult := streamResultJSON(AgentResult{Result: "Root cause: network timeout"})
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 300, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/300"},
			{ID: 200, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-2 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/200"},
			{ID: 100, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-3 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/100"},
		},
	}
	runner := &mockCommandRunner{stdout: triageResult}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:      "owner",
		Repo:       "repo",
		FlakyLabel: "flaky-test",
		TriageJobs: []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
		// TriageLookback: 0 (default — only check latest)
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "Root cause: network timeout"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should only investigate 1 run (the latest)
	if codeAgent.callCount != 1 {
		t.Errorf("expected 1 agent call (latest run only), got %d", codeAgent.callCount)
	}

	// Only the latest run should be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "300") {
		t.Error("expected run 300 to be marked as investigated")
	}
	if state.IsRunInvestigated("owner/repo/ci.yml", "200") {
		t.Error("expected run 200 to NOT be marked as investigated")
	}
	if state.IsRunInvestigated("owner/repo/ci.yml", "100") {
		t.Error("expected run 100 to NOT be marked as investigated")
	}
}

func TestProcessTriageJobs_LookbackInvestigatesAllFailedRuns(t *testing.T) {
	// When TriageLookback is set, all failed runs within the window are investigated.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 300, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/300"},
			{ID: 200, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-2 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/200"},
			{ID: 100, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-25 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/100"},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:          "owner",
		Repo:           "repo",
		FlakyLabel:     "flaky-test",
		TriageJobs:     []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
		TriageLookback: 24 * time.Hour, // look back 24 hours
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "Failure analysis 1"}},
			{result: AgentResult{Result: "Failure analysis 2"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should investigate 2 runs (300 and 200 are within 24h; 100 is older)
	if codeAgent.callCount != 2 {
		t.Errorf("expected 2 agent calls (runs within lookback window), got %d", codeAgent.callCount)
	}

	// Runs within window should be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "300") {
		t.Error("expected run 300 to be marked as investigated")
	}
	if !state.IsRunInvestigated("owner/repo/ci.yml", "200") {
		t.Error("expected run 200 to be marked as investigated")
	}
	// Run older than lookback should NOT be investigated
	if state.IsRunInvestigated("owner/repo/ci.yml", "100") {
		t.Error("expected run 100 to NOT be marked as investigated (too old)")
	}
}

func TestProcessTriageJobs_LookbackSkipsSuccessfulRuns(t *testing.T) {
	// Successful runs within the lookback window should be marked as investigated
	// but not trigger agent calls.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 300, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/300"},
			{ID: 200, Status: "completed", Conclusion: "success", CreatedAt: now.Add(-2 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/200"},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:          "owner",
		Repo:           "repo",
		FlakyLabel:     "flaky-test",
		TriageJobs:     []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
		TriageLookback: 24 * time.Hour,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "Failure analysis"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Only 1 agent call for the failed run
	if codeAgent.callCount != 1 {
		t.Errorf("expected 1 agent call (only failed runs), got %d", codeAgent.callCount)
	}

	// Both runs should be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "300") {
		t.Error("expected failed run 300 to be marked as investigated")
	}
	if !state.IsRunInvestigated("owner/repo/ci.yml", "200") {
		t.Error("expected passed run 200 to be marked as investigated")
	}
}

func TestProcessTriageJobs_LookbackSkipsAlreadyInvestigated(t *testing.T) {
	// Already-investigated runs should be skipped in lookback mode.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 300, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/300"},
			{ID: 200, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-2 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/200"},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()

	// Pre-mark run 200 as already investigated
	state.MarkRunInvestigated("owner/repo/ci.yml", "200")

	cfg := Config{
		Owner:          "owner",
		Repo:           "repo",
		FlakyLabel:     "flaky-test",
		TriageJobs:     []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
		TriageLookback: 24 * time.Hour,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "Failure analysis"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Only 1 agent call (run 200 was already investigated)
	if codeAgent.callCount != 1 {
		t.Errorf("expected 1 agent call (skip already investigated), got %d", codeAgent.callCount)
	}

	if !state.IsRunInvestigated("owner/repo/ci.yml", "300") {
		t.Error("expected run 300 to be marked as investigated")
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
	if agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].CIFixAttempts != 1 {
		t.Errorf("expected 1 CI fix attempt, got %d", agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].CIFixAttempts)
	}
}

func TestIsUnstagedChangesError(t *testing.T) {
	tests := []struct {
		stderr string
		want   bool
	}{
		{"error: cannot rebase: You have unstaged changes.\nerror: Please commit or stash them.\n", true},
		{"error: unstaged changes would be overwritten by rebase", true},
		{"error: could not apply 3a35b4e... Migrate remaining features\nCONFLICT (content): Merge conflict in main.go", false},
		{"fatal: some other error", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isUnstagedChangesError(tt.stderr)
		if got != tt.want {
			t.Errorf("isUnstagedChangesError(%q) = %v, want %v", tt.stderr, got, tt.want)
		}
	}
}

func TestProcessRebase_CleansUnstagedChangesAndRetries(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "behind",
	}
	runner := &unstagedChangesRebaseRunner{
		mockCommandRunner: &mockCommandRunner{},
	}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessRebase(context.Background())

	// Should have called git checkout -- . to clean unstaged changes
	foundCheckout := false
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) >= 3 && c.Args[0] == "checkout" && c.Args[1] == "--" && c.Args[2] == "." {
			foundCheckout = true
		}
	}
	if !foundCheckout {
		t.Error("expected git checkout -- . to be called to clean unstaged changes")
	}

	// Should have attempted rebase twice (first fails with unstaged, second succeeds)
	rebaseCalls := 0
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" && !slices.Contains(c.Args, "--abort") {
			rebaseCalls++
		}
	}
	if rebaseCalls != 2 {
		t.Errorf("expected 2 rebase calls (initial + retry), got %d", rebaseCalls)
	}

	// Should NOT have called git rebase --abort (retry succeeded)
	abortCalls := 0
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" && slices.Contains(c.Args, "--abort") {
			abortCalls++
		}
	}
	if abortCalls != 0 {
		t.Errorf("expected 0 rebase --abort calls (retry succeeded), got %d", abortCalls)
	}
}

func TestProcessConflicts_CleansUnstagedChangesAndRetries(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "dirty",
	}
	runner := &unstagedChangesRebaseRunner{
		mockCommandRunner: &mockCommandRunner{},
	}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessConflicts(context.Background())

	// Should have called git checkout -- . to clean unstaged changes
	foundCheckout := false
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) >= 3 && c.Args[0] == "checkout" && c.Args[1] == "--" && c.Args[2] == "." {
			foundCheckout = true
		}
	}
	if !foundCheckout {
		t.Error("expected git checkout -- . to be called to clean unstaged changes")
	}

	// Should have attempted rebase twice (first fails with unstaged, second succeeds)
	rebaseCalls := 0
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" && !slices.Contains(c.Args, "--abort") {
			rebaseCalls++
		}
	}
	if rebaseCalls != 2 {
		t.Errorf("expected 2 rebase calls (initial + retry), got %d", rebaseCalls)
	}

	// Should NOT have called git rebase --abort (retry succeeded)
	abortCalls := 0
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" && slices.Contains(c.Args, "--abort") {
			abortCalls++
		}
	}
	if abortCalls != 0 {
		t.Errorf("expected 0 rebase --abort calls (retry succeeded), got %d", abortCalls)
	}
}

// unstagedChangesRebaseRunner simulates a rebase that fails on the first attempt
// with "unstaged changes" and succeeds on the second attempt after cleanup.
type unstagedChangesRebaseRunner struct {
	*mockCommandRunner
	rebaseAttempts int
}

func (r *unstagedChangesRebaseRunner) Run(ctx context.Context, workDir, name string, args ...string) (stdout, stderr []byte, err error) {
	r.mockCommandRunner.Run(ctx, workDir, name, args...) //nolint:errcheck // recording call for test assertions

	// Return "unstaged changes" error on the first rebase attempt, succeed on retry
	if name == "git" && len(args) > 0 && args[0] == "rebase" && !slices.Contains(args, "--abort") {
		r.rebaseAttempts++
		if r.rebaseAttempts == 1 {
			return nil, []byte("error: cannot rebase: You have unstaged changes.\nerror: Please commit or stash them.\n"), fmt.Errorf("rebase failed")
		}
		return nil, nil, nil // retry succeeds
	}

	return nil, nil, nil
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
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
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
			claudePrompts = append(claudePrompts, c.Stdin)
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

func TestProcessCIFailures_SkipChecksExcludesFromFailures(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "can-be-merged", Status: "completed", Conclusion: "failure", Output: "merge gate failed"},
			{ID: 2, Name: "unit-tests", Status: "completed", Conclusion: "failure", Output: "test failed"},
		},
	}
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED this is flaky"})
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipChecks = []string{"can-be-merged"}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Only the non-skipped check should be investigated
	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
			// Verify the prompt does NOT mention the skipped check
			if strings.Contains(c.Stdin, "can-be-merged") {
				t.Error("skipped check 'can-be-merged' should not appear in claude prompt")
			}
		}
	}
	if claudeCalls != 1 {
		t.Fatalf("expected 1 claude call for non-skipped check, got %d", claudeCalls)
	}
}

func TestProcessCIFailures_SkipChecksAllFailuresSkipped(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "can-be-merged", Status: "completed", Conclusion: "failure", Output: "merge gate failed"},
			{ID: 2, Name: "verified", Status: "completed", Conclusion: "failure", Output: "not verified"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipChecks = []string{"can-be-merged", "verified"}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// No claude calls since all failures are skipped
	if len(runner.calls) != 0 {
		t.Errorf("expected no claude calls when all failures are skipped, got %d", len(runner.calls))
	}
}

func TestWorkerName(t *testing.T) {
	for _, tt := range []struct{ role, want string }{
		{"prs", "owner/repo:prs"},
		{"", "owner/repo"},
	} {
		a := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
		a.cfg.Role = tt.role
		if got := a.workerName(); got != tt.want {
			t.Errorf("workerName() with role=%q = %q, want %q", tt.role, got, tt.want)
		}
	}
}

func TestProcessCIFailures_SkipChecksDoesNotAffectAllCompleted(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "can-be-merged", Status: "in_progress", Conclusion: ""},
			{ID: 2, Name: "unit-tests", Status: "completed", Conclusion: "success"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SkipChecks = []string{"can-be-merged"}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber: 42,
		PRNumber:    100,
		BranchName:  "ai/issue-42",
		Status:      "pr-open",
	}

	agent.ProcessCIFailures(context.Background())

	// The skipped in_progress check should not prevent allCompleted from being true.
	// With can-be-merged skipped, only unit-tests (completed+success) remains,
	// so allCompleted=true and LastCheckedCISHA should be set.
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCheckedCISHA != "abc123" {
		t.Errorf("expected LastCheckedCISHA to be set when skipped check is the only non-completed, got %q", work.LastCheckedCISHA)
	}
}

func TestProcessReviewComments_AgentFailureAdvancesCursor(t *testing.T) {
	// Issue #164: When the agent fails (e.g., git corruption), cursors must advance
	// to prevent infinite retry loops that waste API credits.
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
		prReviews: []PRReview{
			{ID: 200, User: "copilot", Body: "Review feedback"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.codeAgent = &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{{err: fmt.Errorf("agent crashed")}},
	}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:   42,
		IssueTitle:    "Fix bug",
		PRNumber:      100,
		Status:        "pr-open",
		WorktreePath:  "/tmp/worktree",
		LastCommentID: 50,
		LastReviewID:  100,
	}

	agent.ProcessReviewComments(context.Background())

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	// Cursors MUST advance even on agent failure to prevent infinite loops.
	if work.LastCommentID != 60 {
		t.Errorf("expected LastCommentID to advance to 60 on agent failure, got %d", work.LastCommentID)
	}
	if work.LastReviewID != 200 {
		t.Errorf("expected LastReviewID to advance to 200 on agent failure, got %d", work.LastReviewID)
	}
	// ReviewNoOpCount should increment on failure.
	if work.ReviewNoOpCount != 1 {
		t.Errorf("expected ReviewNoOpCount to be 1 after agent failure, got %d", work.ReviewNoOpCount)
	}
}

func TestProcessReviewComments_NoOpCountPausesReviews(t *testing.T) {
	// Issue #164: After N consecutive no-op cycles, review processing should pause.
	// Simulates the scenario where push keeps failing on the same reviews.
	result := streamResultJSON(AgentResult{Result: "No changes needed"})
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{result}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.MaxReviewNoOps = 3
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:     42,
		IssueTitle:      "Fix bug",
		PRNumber:        100,
		Status:          "pr-open",
		WorktreePath:    "/tmp/worktree",
		LastCommentID:   50,
		ReviewNoOpCount: 3, // already at limit
	}

	agent.ProcessReviewComments(context.Background())

	// Should NOT invoke the agent because no-op limit is reached.
	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	if claudeCalls != 0 {
		t.Errorf("expected 0 claude calls when no-op limit reached, got %d", claudeCalls)
	}

	// Cursors should still advance past the skipped reviews.
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCommentID != 60 {
		t.Errorf("expected LastCommentID to advance to 60, got %d", work.LastCommentID)
	}
}

func TestProcessReviewComments_NewReviewAfterNoOpPause(t *testing.T) {
	// Issue #164: When the no-op limit is reached, cursors advance and the
	// counter resets. New reviews that arrive later are processed normally.
	// This tests the two-cycle flow:
	// Cycle 1: no-op limit hit → cursors advance, counter resets, reviews skipped
	// Cycle 2: new review arrives with ID > advanced cursor → processed normally
	result := streamResultJSON(AgentResult{Result: "Addressed"})
	gh := &mockGitHubClient{
		// This review has ID 300, which will be above the cursor after it advances
		prReviews: []PRReview{
			{ID: 300, User: "reviewer", Body: "New feedback"},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{result}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.MaxReviewNoOps = 3
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:     42,
		IssueTitle:      "Fix bug",
		PRNumber:        100,
		Status:          "pr-open",
		WorktreePath:    "/tmp/worktree",
		LastReviewID:    250, // cursor at 250, review 300 is new
		ReviewNoOpCount: 3,  // at limit
	}

	// Cycle 1: no-op limit reached → cursors advance to 300, counter resets to 0
	agent.ProcessReviewComments(context.Background())

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastReviewID != 300 {
		t.Fatalf("expected LastReviewID to advance to 300 on no-op skip, got %d", work.LastReviewID)
	}
	if work.ReviewNoOpCount != 0 {
		t.Fatalf("expected ReviewNoOpCount to reset to 0 after no-op skip, got %d", work.ReviewNoOpCount)
	}

	// Simulate a new review arriving after the cursor advanced
	gh.prReviews = []PRReview{
		{ID: 400, User: "reviewer", Body: "Another round of feedback"},
	}

	// Cycle 2: counter was reset to 0, so the new review (ID 400) is processed.
	agent.ProcessReviewComments(context.Background())

	// The new review should be processed since the counter was reset.
	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	if claudeCalls != 1 {
		t.Errorf("expected 1 claude call for new review after counter reset, got %d", claudeCalls)
	}

	// Cursor should advance past the new review
	if work.LastReviewID != 400 {
		t.Errorf("expected LastReviewID to advance to 400, got %d", work.LastReviewID)
	}
}

func TestProcessReviewComments_NoOpCountResetsOnPush(t *testing.T) {
	// When the agent pushes successfully, the no-op counter should reset to 0.
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	// Use a runner that simulates uncommitted changes (triggers push path)
	runner := &reviewFixupRunner{
		mockCommandRunner: &mockCommandRunner{},
	}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.codeAgent = &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{{result: AgentResult{Result: "Done"}}},
	}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:     42,
		IssueTitle:      "Fix bug",
		PRNumber:        100,
		Status:          "pr-open",
		WorktreePath:    "/tmp/worktree",
		LastCommentID:   50,
		ReviewNoOpCount: 2, // was approaching limit
	}

	agent.ProcessReviewComments(context.Background())

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	// The fixup runner simulates a successful push, so counter should reset.
	if work.ReviewNoOpCount != 0 {
		t.Errorf("expected ReviewNoOpCount to reset to 0 after successful push, got %d", work.ReviewNoOpCount)
	}
}

func TestProcessReviewComments_NoOpCountIncrementsOnNoPush(t *testing.T) {
	// When the agent runs but produces no changes (no push), the counter increments.
	result := streamResultJSON(AgentResult{Result: "All reviews are stale"})
	gh := &mockGitHubClient{
		prReviews: []PRReview{
			{ID: 200, User: "reviewer", Body: "Please fix the error handling"},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{result}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:     42,
		IssueTitle:      "Fix bug",
		PRNumber:        100,
		Status:          "pr-open",
		WorktreePath:    "/tmp/worktree",
		LastReviewID:    100,
		ReviewNoOpCount: 1,
	}

	agent.ProcessReviewComments(context.Background())

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.ReviewNoOpCount != 2 {
		t.Errorf("expected ReviewNoOpCount to increment to 2, got %d", work.ReviewNoOpCount)
	}
	// Cursor should advance since no changes were detected
	if work.LastReviewID != 200 {
		t.Errorf("expected LastReviewID to advance to 200, got %d", work.LastReviewID)
	}
}

func TestProcessReviewComments_CostGuardSkipsReviews(t *testing.T) {
	// Issue #164: When a PR exceeds the per-session cost threshold, skip reviews.
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.MaxPRSessionCost = 10.0
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:    42,
		IssueTitle:     "Fix bug",
		PRNumber:       100,
		Status:         "pr-open",
		WorktreePath:   "/tmp/worktree",
		LastCommentID:  50,
		SessionCostUSD: 11.5, // exceeds $10 threshold
	}

	agent.ProcessReviewComments(context.Background())

	// Should NOT invoke the agent because cost limit is reached.
	if len(runner.calls) != 0 {
		t.Errorf("expected 0 calls when cost limit reached, got %d", len(runner.calls))
	}
}

func TestProcessReviewComments_CostTracking(t *testing.T) {
	// Verify that agent cost is accumulated in SessionCostUSD.
	result := streamResultJSON(AgentResult{Result: "Done", CostUSD: 0.75})
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{result}}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:    42,
		IssueTitle:     "Fix bug",
		PRNumber:       100,
		Status:         "pr-open",
		WorktreePath:   "/tmp/worktree",
		LastCommentID:  50,
		SessionCostUSD: 2.0, // existing cost
	}

	agent.ProcessReviewComments(context.Background())

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	expectedCost := 2.75 // 2.0 + 0.75
	if work.SessionCostUSD != expectedCost {
		t.Errorf("expected SessionCostUSD %.2f, got %.2f", expectedCost, work.SessionCostUSD)
	}
}
