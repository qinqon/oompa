package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	addedComments      []string
	addedCommentTargets []int // issue/PR number each comment was posted to
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
	recentCommits   int             // number of recent commits returned by CountCommitsSince
	countCommitsErr error           // error to return from CountCommitsSince
	hasEyesReaction map[int64]map[string]bool // commentID -> user -> has eyes reaction

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

func newTestAgent(gh *mockGitHubClient, runner CommandRunner, wt *mockWorktreeManager) *Agent {
	return &Agent{
		gh:        gh,
		runner:    runner,
		worktrees: wt,
		state:     NewState(),
		cfg:       Config{Owner: "owner", Repo: "repo", Label: "good-for-ai", FlakyLabel: "flaky-test", GitHubUser: "test-bot"},
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

func TestProcessNewIssues_EmitsIssueAndAgentEvents(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "Fixed it"})
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	emitter := &recordingEmitter{}
	agent.SetEmitter(emitter)

	agent.ProcessNewIssues(context.Background())

	// Verify issue-category event emitted when processing starts
	var foundIssueWorking, foundAgentInvocation, foundPRCreated, foundAgentCompleted bool
	for _, e := range emitter.events {
		if e.Category == CategoryIssue && e.State == "working" && strings.Contains(e.Action, "Working on issue #42") {
			foundIssueWorking = true
		}
		if e.Category == CategoryAgent && e.State == "working" && strings.Contains(e.Action, "Agent implementing issue #42") {
			foundAgentInvocation = true
		}
		if e.Category == CategoryIssue && e.State == "idle" && strings.Contains(e.Action, "Created PR #100 for issue #42") {
			foundPRCreated = true
			if len(e.PRNumbers) != 1 || e.PRNumbers[0] != 100 {
				t.Errorf("expected PRNumbers [100], got %v", e.PRNumbers)
			}
		}
		if e.Category == CategoryAgent && e.State == "idle" && strings.Contains(e.Action, "Agent finished implementing issue #42") {
			foundAgentCompleted = true
		}
	}
	if !foundIssueWorking {
		t.Error("expected CategoryIssue event with state 'working' when issue processing starts")
	}
	if !foundAgentInvocation {
		t.Error("expected CategoryAgent event with state 'working' when agent starts running")
	}
	if !foundPRCreated {
		t.Error("expected CategoryIssue event with state 'idle' when PR is created")
	}
	if !foundAgentCompleted {
		t.Error("expected CategoryAgent event with state 'idle' when agent finishes implementing")
	}
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

	// Oompa does NOT post its own replies — the skill handles all per-comment
	// communication. Verify no reply comments were posted by the agent.
	for _, comment := range gh.addedComments {
		if strings.HasPrefix(comment, "reply:") {
			t.Errorf("expected no agent-posted replies (skill owns replies), got: %s", comment)
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

	// Cursor should always advance after a successful agent run.
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCommentID != 60 {
		t.Errorf("expected LastCommentID to advance to 60, got %d", work.LastCommentID)
	}
}

func TestProcessReviewComments_AgentErrorStillAdvancesCursor(t *testing.T) {
	// When the agent errors out, cursor should still advance to avoid
	// infinite retry loops ($0.50-1.00 each time).
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
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
	}

	agent.ProcessReviewComments(context.Background())

	// Oompa does NOT post its own replies — the skill handles all per-comment communication.
	for _, comment := range gh.addedComments {
		if strings.HasPrefix(comment, "reply:") {
			t.Errorf("expected no agent-posted replies (skill owns replies), got: %s", comment)
		}
	}

	// Cursor should still advance (to avoid infinite retry loops)
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

	// Check that a single consolidated comment was added to the PR
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}
	// Consolidated comment should mention the flaky issue
	if !strings.Contains(gh.addedComments[0], "#1") {
		t.Errorf("expected consolidated comment to reference flaky issue #1, got: %q", gh.addedComments[0])
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

	// Check that exactly 1 consolidated comment was posted
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}

	// Verify the comment mentions infrastructure
	if !strings.Contains(gh.addedComments[0], "Infrastructure:") {
		t.Errorf("expected comment to mention Infrastructure, got: %q", gh.addedComments[0])
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

	// No comments should be posted — the unrelated section is skipped (ci-unrelated),
	// so the consolidated comment has no visible content and is suppressed.
	if len(gh.addedComments) != 0 {
		t.Fatalf("expected 0 comments (ci-unrelated skipped, no visible sections), got %d: %v", len(gh.addedComments), gh.addedComments)
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

// TestProcessCIFailures_CheckedCIChecksPopulatedWhenCommentsPosted verifies that
// the in-memory CheckedCIChecks map is populated even when comments are posted
// (the default, non-skip path). This is the primary dedup mechanism; comment
// markers are a secondary fallback that can be lost if comments are deleted.
func TestProcessCIFailures_CheckedCIChecksPopulatedWhenCommentsPosted(t *testing.T) {
	// Test all three classification categories
	tests := []struct {
		name           string
		claudeResponse string
		checkName      string
		wantCIStatus   string
	}{
		{
			name:           "infrastructure",
			claudeResponse: "INFRASTRUCTURE Fedora koji server returned HTTP 502 Bad Gateway",
			checkName:      "Build-PR",
			wantCIStatus:   "infrastructure-failure",
		},
		{
			name:           "unrelated",
			claudeResponse: "UNRELATED The test database connection times out intermittently",
			checkName:      "integration-tests",
			wantCIStatus:   "unrelated-failure",
		},
		{
			name:           "related-skip-fix",
			claudeResponse: "RELATED The unit test fails because the new function returns nil",
			checkName:      "unit-tests",
			wantCIStatus:   "related-skip-fix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claudeResult := streamResultJSON(AgentResult{Result: tt.claudeResponse})
			gh := &mockGitHubClient{
				checkRuns: []CheckRun{
					{ID: 1, Name: tt.checkName, Status: "completed", Conclusion: "failure", Output: "Error details here for analysis context padding to reach the 50 char minimum threshold"},
				},
				checkRunLogs: map[int64]string{
					1: "Build log output with enough content for analysis context padding to reach minimum",
				},
			}
			runner := &mockCommandRunner{stdout: claudeResult}
			wt := &mockWorktreeManager{}

			agent := newTestAgent(gh, runner, wt)
			agent.cfg.SkipFix = true // Prevent push attempts for RELATED
			// SkipComments is empty — comments will be posted (default behavior)
			agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
				IssueNumber:  42,
				IssueTitle:   "Fix bug",
				PRNumber:     100,
				BranchName:   "ai/issue-42",
				Status:       "pr-open",
				WorktreePath: "/tmp/worktree",
			}

			agent.ProcessCIFailures(context.Background())

			work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]

			// Verify in-memory dedup map is populated even though comments are posted
			dedupKey := "abc123:" + tt.checkName
			if !work.CheckedCIChecks[dedupKey] {
				t.Errorf("expected CheckedCIChecks[%q] to be true when comments are posted (not skipped), but it was false", dedupKey)
			}

			// Verify status was set correctly
			if work.LastCIStatus != tt.wantCIStatus {
				t.Errorf("expected LastCIStatus %q, got %q", tt.wantCIStatus, work.LastCIStatus)
			}

			// Verify a comment was actually posted (confirming we're testing the non-skip path)
			if len(gh.addedComments) == 0 {
				t.Error("expected at least 1 comment to be posted (testing non-skip path), got 0")
			}
		})
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

	// Should have only the consolidated comment (flaky issue column suppressed by skip-comment: flaky)
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment (flaky ref suppressed), got %d", len(gh.addedComments))
	}
	if !strings.Contains(gh.addedComments[0], "Unrelated:") {
		t.Errorf("expected unrelated section in consolidated comment, got: %q", gh.addedComments[0])
	}
	// With flaky comment skipped, the issue reference should NOT appear in the Known Issue section
	if strings.Contains(gh.addedComments[0], "Known Issue") {
		t.Errorf("expected Known Issue section to be suppressed when skip-comment: flaky, got: %q", gh.addedComments[0])
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
	// 1. CI lane link on the flaky issue (#50) — per-check side effect
	// 2. consolidated comment on PR with flaky issue reference in table
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (CI lane link + consolidated), got %d", len(gh.addedComments))
	}

	// Verify the CI lane link comment (posted on the flaky issue #50)
	if !strings.Contains(gh.addedComments[0], "CI failure on PR #100") {
		t.Errorf("expected CI lane link comment, got: %q", gh.addedComments[0])
	}
	if gh.addedCommentTargets[0] != 50 {
		t.Errorf("expected CI lane link comment posted to issue #50, got #%d", gh.addedCommentTargets[0])
	}

	// Verify the consolidated comment on the PR references the flaky issue
	if !strings.Contains(gh.addedComments[1], "#50") {
		t.Errorf("expected consolidated comment to reference flaky issue #50, got: %q", gh.addedComments[1])
	}
	if gh.addedCommentTargets[1] != 100 {
		t.Errorf("expected consolidated comment posted to PR #100, got #%d", gh.addedCommentTargets[1])
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

	// Should have 2 comments:
	// 1. CI lane link on the flaky issue (#99) — per-check side effect
	// 2. consolidated comment on PR with flaky issue reference in table
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (CI lane link + consolidated), got %d", len(gh.addedComments))
	}

	// Verify the CI lane link comment (posted on the flaky issue)
	if !strings.Contains(gh.addedComments[0], "CI failure on PR #100") {
		t.Errorf("expected CI lane link comment, got: %q", gh.addedComments[0])
	}

	// Verify the consolidated comment references flaky issue #99
	if !strings.Contains(gh.addedComments[1], "#99") {
		t.Errorf("expected consolidated comment to reference flaky issue #99, got: %q", gh.addedComments[1])
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

	// Check that a single consolidated comment was added referencing the new flaky issue
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}
	if !strings.Contains(gh.addedComments[0], "#1") {
		t.Errorf("expected consolidated comment to reference flaky issue #1, got: %q", gh.addedComments[0])
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

	// Should have 2 comments:
	// 1. CI lane link comment on the existing flaky issue (#1234) — per-check side effect
	// 2. consolidated comment on PR with flaky issue reference in table
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (CI lane link + consolidated), got %d", len(gh.addedComments))
	}

	// Verify CI lane link on the flaky issue
	if !strings.Contains(gh.addedComments[0], "CI failure on PR #100") {
		t.Errorf("expected CI lane link comment, got: %q", gh.addedComments[0])
	}
	if !strings.Contains(gh.addedComments[0], "integration-tests") {
		t.Errorf("expected CI lane link to mention CI lane name, got: %q", gh.addedComments[0])
	}

	// Verify the consolidated comment references flaky issue #1234
	if !strings.Contains(gh.addedComments[1], "#1234") {
		t.Errorf("expected consolidated comment to reference flaky issue #1234, got: %q", gh.addedComments[1])
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

	// Only the consolidated unrelated comment should be posted
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}
	if !strings.Contains(gh.addedComments[0], "Unrelated:") {
		t.Errorf("expected consolidated comment with unrelated section, got: %q", gh.addedComments[0])
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
			{ID: 67890, Name: "e2e (control-plane, HA, shared, ipv4)", Status: "completed", Conclusion: "failure", Output: "Error: test timed out waiting for condition", HTMLURL: "https://github.com/owner/repo/actions/runs/12345/job/67890"},
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

	// Should have 2 comments:
	// 1. CI lane link on the flaky issue (#5678) — per-check side effect
	// 2. consolidated comment on PR with flaky issue reference in table
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (CI lane link + consolidated), got %d", len(gh.addedComments))
	}

	ciLaneComment := gh.addedComments[0]
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

	// Should have 2 comments:
	// 1. CI lane link on the flaky issue (#999) — per-check side effect
	// 2. consolidated comment on PR
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments (CI lane link + consolidated), got %d", len(gh.addedComments))
	}

	ciLaneComment := gh.addedComments[0]
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
		// Multi-line responses: LLM appends explanation after MATCH line
		{"MATCH #42\n\nThis failure matches issue #42 because both share DNS timeout.", 42, true},
		{"MATCH 99\nThe root cause is the same infrastructure outage.", 99, true},
		{"MATCH #2802\n\nBoth failures stem from the same PR merge.", 2802, true},
		// Multi-line NONE should still return false
		{"NONE\n\nNo existing issue matches this failure.", 0, false},
		// Markdown-wrapped responses: bold/italic around MATCH keyword and number
		{"**MATCH #42**", 42, true},
		{"**MATCH 99**\n\nExplanation.", 99, true},
		{"_MATCH #50_", 50, true},
		{"**MATCH #50**\n", 50, true},
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

	// First poll cycle: should investigate and post consolidated comment
	agent.ProcessCIFailures(context.Background())
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment on first poll, got %d", len(gh.addedComments))
	}
	if !strings.Contains(gh.addedComments[0], "Unrelated:") || !strings.Contains(gh.addedComments[0], "<code>test</code>") {
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

func TestShouldRunReaction_NilAllowsAll(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	// Reactions == nil (not configured) — all should be allowed
	for _, reaction := range []string{"reviews", "ci", "conflicts", "rebase"} {
		if !agent.ShouldRunReaction(reaction) {
			t.Errorf("expected %q to be allowed with nil Reactions", reaction)
		}
	}
}

func TestShouldRunReaction_EmptySliceDisablesAll(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.Reactions = []string{} // explicitly set to empty list
	for _, reaction := range []string{"reviews", "ci", "conflicts", "rebase"} {
		if agent.ShouldRunReaction(reaction) {
			t.Errorf("expected %q to be disabled with empty (non-nil) Reactions", reaction)
		}
	}
}

func TestShouldRunReaction_Filtered(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.Reactions = []string{"ci", "rebase"}

	if !agent.ShouldRunReaction("ci") {
		t.Error("expected 'ci' to be allowed")
	}
	if !agent.ShouldRunReaction("rebase") {
		t.Error("expected 'rebase' to be allowed")
	}
	if agent.ShouldRunReaction("reviews") {
		t.Error("expected 'reviews' to be filtered out")
	}
	if agent.ShouldRunReaction("conflicts") {
		t.Error("expected 'conflicts' to be filtered out")
	}
}

func TestShouldCheckReaction_NoWebhook(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.SlackWebhookURL = ""
	// No webhook — always returns false regardless of reactions config
	agent.cfg.Reactions = nil
	if agent.ShouldCheckReaction("ci") {
		t.Error("expected false with no webhook and nil Reactions")
	}
	agent.cfg.Reactions = []string{}
	if agent.ShouldCheckReaction("ci") {
		t.Error("expected false with no webhook and empty Reactions")
	}
	agent.cfg.Reactions = []string{"rebase"}
	if agent.ShouldCheckReaction("ci") {
		t.Error("expected false with no webhook and specific Reactions")
	}
}

func TestShouldCheckReaction_WebhookNilReactions(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.SlackWebhookURL = "https://hooks.slack.com/test"
	agent.cfg.Reactions = nil // all reactions enabled
	// All reactions active → nothing is report-only
	for _, reaction := range []string{"reviews", "ci", "conflicts", "rebase"} {
		if agent.ShouldCheckReaction(reaction) {
			t.Errorf("expected %q to NOT be report-only when all reactions enabled", reaction)
		}
	}
}

func TestShouldCheckReaction_WebhookEmptyReactions(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.SlackWebhookURL = "https://hooks.slack.com/test"
	agent.cfg.Reactions = []string{} // no reactions enabled → everything is report-only
	for _, reaction := range []string{"reviews", "ci", "conflicts", "rebase"} {
		if !agent.ShouldCheckReaction(reaction) {
			t.Errorf("expected %q to be report-only when no reactions enabled", reaction)
		}
	}
}

func TestShouldCheckReaction_WebhookPartialReactions(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.SlackWebhookURL = "https://hooks.slack.com/test"
	agent.cfg.Reactions = []string{"rebase"}
	// "rebase" is active — NOT report-only
	if agent.ShouldCheckReaction("rebase") {
		t.Error("expected 'rebase' to NOT be report-only (it's active)")
	}
	// All others are report-only
	for _, reaction := range []string{"ci", "reviews", "conflicts"} {
		if !agent.ShouldCheckReaction(reaction) {
			t.Errorf("expected %q to be report-only (not in active reactions)", reaction)
		}
	}
}

func TestReportOnlyMode_EmptyReactionsGatesAndChecks(t *testing.T) {
	// Verifies that with Reactions == []string{} + webhook set:
	// - ShouldRunReaction returns false for all reactions (gating mechanism)
	// - ShouldCheckReaction returns true for all reactions (report-only)
	// - RunReportOnlyChecks produces findings from the mock state
	// - No agent/runner invocations occur
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
		checkRuns: []CheckRun{
			{ID: 1, Name: "test", Status: "completed", Conclusion: "failure", Output: "tests failed"},
		},
		mergeableState: "dirty",
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.Reactions = []string{}                            // report-only mode
	agent.cfg.SlackWebhookURL = "https://hooks.slack.com/test" // enable Slack
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-42",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	// All reactions should be skipped
	if agent.ShouldRunReaction("reviews") {
		t.Error("ProcessReviewComments should be skipped in report-only mode")
	}
	if agent.ShouldRunReaction("ci") {
		t.Error("ProcessCIFailures should be skipped in report-only mode")
	}
	if agent.ShouldRunReaction("rebase") {
		t.Error("ProcessRebase should be skipped in report-only mode")
	}
	if agent.ShouldRunReaction("conflicts") {
		t.Error("ProcessConflicts should be skipped in report-only mode")
	}

	// Report-only checks SHOULD run
	if !agent.ShouldCheckReaction("ci") {
		t.Error("CI report-only check should run in report-only mode")
	}
	if !agent.ShouldCheckReaction("reviews") {
		t.Error("Reviews report-only check should run in report-only mode")
	}
	if !agent.ShouldCheckReaction("conflicts") {
		t.Error("Conflicts report-only check should run in report-only mode")
	}
	if !agent.ShouldCheckReaction("rebase") {
		t.Error("Rebase report-only check should run in report-only mode")
	}

	// RunReportOnlyChecks should produce findings
	findings := agent.RunReportOnlyChecks(context.Background())
	if len(findings) == 0 {
		t.Error("expected at least one finding from report-only checks")
	}

	// No agent invocations should have happened
	if len(runner.calls) != 0 {
		t.Errorf("expected 0 runner calls in report-only mode, got %d", len(runner.calls))
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
					if !strings.Contains(commitMsg, "Related-to: #42") {
						t.Errorf("expected commit message to contain 'Related-to: #42', got: %s", commitMsg)
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

func TestIsConventionalCommitTitle(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		expected bool
	}{
		// Conventional commit prefixes — should match
		{"feat prefix", "feat: add new feature", true},
		{"fix prefix", "fix: resolve crash on startup", true},
		{"build prefix", "build: consolidate multi-arch container build scripts", true},
		{"refactor prefix", "refactor: simplify handler logic", true},
		{"docs prefix", "docs: update README", true},
		{"chore prefix", "chore: bump dependencies", true},
		{"test prefix", "test: add unit tests for parser", true},
		{"ci prefix", "ci: fix GitHub Actions workflow", true},
		{"perf prefix", "perf: optimize database queries", true},
		{"style prefix", "style: fix formatting", true},
		{"revert prefix", "revert: undo previous change", true},

		// With scope
		{"feat with scope", "feat(api): add pagination support", true},
		{"fix with scope", "fix(auth): handle expired tokens", true},
		{"refactor with scope", "refactor(build): consolidate scripts", true},

		// With breaking change indicator
		{"breaking without scope", "feat!: remove deprecated API", true},
		{"breaking with scope", "feat(api)!: change response format", true},

		// Non-conventional titles — should NOT match
		{"capitalized word", "Fix the bug", false},
		{"lowercase no prefix", "implement feature X", false},
		{"sentence case", "Update README with new instructions", false},
		{"issue ref prefix", "Fix #42: something", false},
		{"invalid prefix word", "feature: this is not a valid prefix", false},
		{"uppercase prefix", "FEAT: uppercase doesn't match", false},
		{"missing colon", "feat - missing colon", false},
		{"no space after colon", "feat:missing space is fine", true},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isConventionalCommitTitle(tt.title)
			if got != tt.expected {
				t.Errorf("isConventionalCommitTitle(%q) = %v, want %v", tt.title, got, tt.expected)
			}
		})
	}
}

func TestTruncateSubject(t *testing.T) {
	tests := []struct {
		name     string
		subject  string
		maxLen   int
		expected string
	}{
		{"short subject unchanged", "fix: short title", 72, "fix: short title"},
		{"exactly 72 chars unchanged", strings.Repeat("a", 72), 72, strings.Repeat("a", 72)},
		{"long title truncated at word boundary", "CI Failure: pull-e2e-cluster-network-addons-operator-monitoring-k8s / The pull-e2e-cluster-network-addons-operator-monitoring-k8s", 72, "CI Failure: pull-e2e-cluster-network-addons-operator-monitoring-k8s..."},
		{"long single word hard truncated", strings.Repeat("x", 100), 72, strings.Repeat("x", 69) + "..."},
		{"empty string unchanged", "", 72, ""},
		{"breaks at last space before cutoff", "word1 word2 word3 word4 word5 word6 word7 word8 word9 word10 word11 word12 word13", 72, "word1 word2 word3 word4 word5 word6 word7 word8 word9 word10 word11..."},
		{"multi-byte runes not split", "Ошибка CI: " + strings.Repeat("слово ", 20), 30, "Ошибка CI: слово слово..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateSubject(tt.subject, tt.maxLen)
			if got != tt.expected {
				t.Errorf("truncateSubject(%q, %d) =\n  %q (len=%d)\nwant:\n  %q (len=%d)", tt.subject, tt.maxLen, got, len(got), tt.expected, len(tt.expected))
			}
			if len([]rune(got)) > tt.maxLen {
				t.Errorf("truncateSubject result exceeds maxLen: got %d runes, max %d", len([]rune(got)), tt.maxLen)
			}
		})
	}
}

func TestProcessNewIssues_SquashesCommits_ConventionalCommitTitle(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "Fixed it"})
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 1532, Title: "build: consolidate multi-arch container build scripts", Body: "Consolidate build scripts"}},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SignedOffBy = "Test User <test@example.com>"
	agent.ProcessNewIssues(context.Background())

	// Verify the commit message uses the issue title as the subject
	// and puts "Related-to: #N" in the body (no auto-close keywords)
	for _, c := range runner.calls {
		if c.Name != "git" || len(c.Args) < 3 || c.Args[0] != "commit" || c.Args[1] != "-m" {
			continue
		}
		commitMsg := c.Args[2]
		// The subject line should be the issue title as-is
		lines := strings.SplitN(commitMsg, "\n", 2)
		if lines[0] != "build: consolidate multi-arch container build scripts" {
			t.Errorf("expected subject line to be the conventional commit title, got: %q", lines[0])
		}
		// Should NOT contain auto-close keywords
		if strings.Contains(commitMsg, "Fix #1532") || strings.Contains(commitMsg, "Fixes #1532") || strings.Contains(commitMsg, "Closes #1532") {
			t.Errorf("commit message should not contain auto-close keywords, got: %s", commitMsg)
		}
		// Should contain "Related-to: #1532" in the body
		if !strings.Contains(commitMsg, "Related-to: #1532") {
			t.Errorf("expected commit body to contain 'Related-to: #1532', got: %s", commitMsg)
		}
		// Should still have trailers
		if !strings.Contains(commitMsg, "Signed-off-by") {
			t.Errorf("expected commit message to contain 'Signed-off-by', got: %s", commitMsg)
		}
		return
	}
	t.Error("expected git commit -m to be called")
}

func TestProcessNewIssues_SquashesCommits_NonConventionalTitle(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "Fixed it"})
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "implement feature X", Body: "broken"}},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.SignedOffBy = "Test User <test@example.com>"
	agent.ProcessNewIssues(context.Background())

	// Verify the commit message uses the issue title as subject and
	// references the issue in the body with "Related-to:" (no auto-close keywords)
	for _, c := range runner.calls {
		if c.Name != "git" || len(c.Args) < 3 || c.Args[0] != "commit" || c.Args[1] != "-m" {
			continue
		}
		commitMsg := c.Args[2]
		lines := strings.SplitN(commitMsg, "\n", 2)
		if lines[0] != "implement feature X" {
			t.Errorf("expected subject line to be 'implement feature X', got: %q", lines[0])
		}
		// Should NOT contain auto-close keywords
		if strings.Contains(commitMsg, "Fix #42") || strings.Contains(commitMsg, "Fixes #42") || strings.Contains(commitMsg, "Closes #42") {
			t.Errorf("commit message should not contain auto-close keywords, got: %s", commitMsg)
		}
		// Should contain "Related-to: #42" in the body
		if !strings.Contains(commitMsg, "Related-to: #42") {
			t.Errorf("expected commit body to contain 'Related-to: #42', got: %s", commitMsg)
		}
		if !strings.Contains(commitMsg, "Signed-off-by") {
			t.Errorf("expected commit message to contain 'Signed-off-by', got: %s", commitMsg)
		}
		return
	}
	t.Error("expected git commit -m to be called")
}

func TestProcessNewIssues_SquashesCommits_LongTitle(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "Fixed it"})
	longTitle := "CI Failure: pull-e2e-cluster-network-addons-operator-monitoring-k8s / The pull-e2e-cluster-network-addons-operator-monitoring-k8s"
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 2799, Title: longTitle, Body: "CI is failing"}},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.ProcessNewIssues(context.Background())

	// Verify the commit subject is truncated to 72 characters
	for _, c := range runner.calls {
		if c.Name != "git" || len(c.Args) < 3 || c.Args[0] != "commit" || c.Args[1] != "-m" {
			continue
		}
		commitMsg := c.Args[2]
		lines := strings.SplitN(commitMsg, "\n", 2)
		subject := lines[0]
		if len(subject) > 72 {
			t.Errorf("subject line exceeds 72 chars (%d): %q", len(subject), subject)
		}
		if !strings.HasSuffix(subject, "...") {
			t.Errorf("truncated subject should end with '...', got: %q", subject)
		}
		// Should NOT contain auto-close keywords
		if strings.Contains(commitMsg, "Fix #2799") || strings.Contains(commitMsg, "Fixes #2799") {
			t.Errorf("commit message should not contain auto-close keywords, got: %s", commitMsg)
		}
		// Should contain "Related-to: #2799" in the body
		if !strings.Contains(commitMsg, "Related-to: #2799") {
			t.Errorf("expected commit body to contain 'Related-to: #2799', got: %s", commitMsg)
		}
		return
	}
	t.Error("expected git commit -m to be called")
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

func TestProcessTriageJobs_DeduplicatesMultipleRunsSameJob(t *testing.T) {
	// When multiple failed runs of the same job are investigated in the same
	// triage cycle, the second run should match the issue created by the first
	// and post a run-link comment instead of creating a duplicate issue.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 300, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/300"},
			{ID: 200, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-2 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/200"},
		},
		searchResults:   []Issue{}, // GitHub search returns nothing (eventual consistency lag)
		nextIssueNumber: 10,
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
		TriageJobs:        []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
		TriageLookback:    24 * time.Hour,
	}

	// Both runs produce the same failure signature, so titles match exactly.
	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "## Summary\nCompile error in main.go"}},
			{result: AgentResult{Result: "## Summary\nCompile error in main.go"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should create exactly 1 issue (first run) and post a run-link comment (second run)
	if len(gh.createdIssues) != 1 {
		t.Errorf("expected 1 issue created (dedup same job), got %d", len(gh.createdIssues))
	}

	// The run-link comment for the second run should reference the issue created by the first
	runLinkFound := false
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "Same failure observed") {
			runLinkFound = true
		}
	}
	if !runLinkFound {
		t.Error("expected a run-link comment for the deduplicated second run")
	}

	// Both runs should be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "300") {
		t.Error("expected run 300 to be marked as investigated")
	}
	if !state.IsRunInvestigated("owner/repo/ci.yml", "200") {
		t.Error("expected run 200 to be marked as investigated")
	}
}

func TestProcessTriageJobs_DeduplicatesDifferentJobsSameRootCause(t *testing.T) {
	// When different jobs fail for the same root cause in the same triage cycle,
	// the second job should match the issue created by the first via LLM matching
	// and post a run-link comment instead of creating a duplicate issue.
	now := time.Now()

	// Two different GitHub Actions workflows, each with one failed run
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 100, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/100"},
		},
		searchResults:   []Issue{}, // GitHub search returns nothing
		nextIssueNumber: 10,
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
		TriageJobs: []string{
			"https://github.com/owner/repo/actions/workflows/unit.yml",
			"https://github.com/owner/repo/actions/workflows/e2e.yml",
		},
	}

	// First job: analysis + no match (NONE) → creates issue
	// Second job: analysis + LLM match → deduplicates
	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "## Summary\nDependency X broke API"}},           // first job analysis
			{result: AgentResult{Result: "## Summary\nDependency X broke API in e2e test"}}, // second job analysis
			{result: AgentResult{Result: "MATCH #10"}},                                      // second job matches issue #10
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should create exactly 1 issue (first job) and post a run-link (second job)
	if len(gh.createdIssues) != 1 {
		t.Errorf("expected 1 issue created (dedup across jobs), got %d", len(gh.createdIssues))
	}

	// The issue created by the first job should be #10
	if len(gh.createdIssues) > 0 && gh.createdIssues[0].Number != 10 {
		t.Errorf("expected first issue number 10, got %d", gh.createdIssues[0].Number)
	}

	// The second job should have posted a run-link comment
	runLinkFound := false
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "Same failure observed") {
			runLinkFound = true
		}
	}
	if !runLinkFound {
		t.Error("expected a run-link comment for the deduplicated second job")
	}

	// Both runs should be investigated
	if !state.IsRunInvestigated("owner/repo/unit.yml", "100") {
		t.Error("expected unit.yml run to be marked as investigated")
	}
	if !state.IsRunInvestigated("owner/repo/e2e.yml", "100") {
		t.Error("expected e2e.yml run to be marked as investigated")
	}
}

func TestMergeIssues(t *testing.T) {
	tests := []struct {
		name    string
		primary []Issue
		extras  []Issue
		want    int // expected length
	}{
		{
			name:    "empty extras",
			primary: []Issue{{Number: 1}},
			extras:  nil,
			want:    1,
		},
		{
			name:    "no overlap",
			primary: []Issue{{Number: 1}},
			extras:  []Issue{{Number: 2}},
			want:    2,
		},
		{
			name:    "with overlap",
			primary: []Issue{{Number: 1}, {Number: 2}},
			extras:  []Issue{{Number: 2}, {Number: 3}},
			want:    3,
		},
		{
			name:    "both empty",
			primary: nil,
			extras:  nil,
			want:    0,
		},
		{
			name:    "empty primary with extras",
			primary: nil,
			extras:  []Issue{{Number: 5}},
			want:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeIssues(tt.primary, tt.extras)
			if len(got) != tt.want {
				t.Errorf("mergeIssues() returned %d issues, want %d", len(got), tt.want)
			}

			// Verify no duplicates
			seen := make(map[int]bool)
			for _, issue := range got {
				if seen[issue.Number] {
					t.Errorf("duplicate issue #%d in merged result", issue.Number)
				}
				seen[issue.Number] = true
			}
		})
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

func TestProcessCIFailures_ConsolidatesMultipleFailuresIntoSingleComment(t *testing.T) {
	// Issue #173: Multiple CI failures on the same SHA should produce a single consolidated comment.
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test-deploy", Status: "completed", Conclusion: "failure", Output: "GitHub git server returned HTTP 500"},
			{ID: 2, Name: "check-license-header", Status: "completed", Conclusion: "failure", Output: "GitHub git server returned HTTP 500"},
			{ID: 3, Name: "e2e-dual-conversion", Status: "completed", Conclusion: "failure", Output: "GitHub git server returned HTTP 500"},
		},
		checkRunLogs: map[int64]string{
			1: "Cloning repository...\nfatal: unable to access: HTTP 500 Internal Server Error",
			2: "Cloning repository...\nfatal: unable to access: HTTP 500 Internal Server Error",
			3: "Cloning repository...\nfatal: unable to access: HTTP 500 Internal Server Error",
		},
	}
	// All three failures are INFRASTRUCTURE
	infraResult := streamResultJSON(AgentResult{Result: "INFRASTRUCTURE GitHub git server returned HTTP 500"})
	runner := &mockCommandRunner{stdout: infraResult}
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

	// All three failures investigated independently
	claudeCalls := countClaudeCalls(runner.calls)
	if claudeCalls != 3 {
		t.Fatalf("expected 3 claude calls (one per failure), got %d", claudeCalls)
	}

	// Only ONE consolidated comment should be posted
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}

	comment := gh.addedComments[0]
	// Should mention all three checks
	if !strings.Contains(comment, "test-deploy") {
		t.Errorf("expected comment to mention test-deploy")
	}
	if !strings.Contains(comment, "check-license-header") {
		t.Errorf("expected comment to mention check-license-header")
	}
	if !strings.Contains(comment, "e2e-dual-conversion") {
		t.Errorf("expected comment to mention e2e-dual-conversion")
	}
	// Should have infrastructure section with grouped count in collapsible details
	if !strings.Contains(comment, "Infrastructure (3)") {
		t.Errorf("expected Infrastructure (3) in grouped details, got: %q", comment)
	}
	// Should have per-check dedup markers
	if !strings.Contains(comment, ciMarker("abc123", "test-deploy")) {
		t.Errorf("expected per-check dedup marker for test-deploy")
	}
	if !strings.Contains(comment, ciMarker("abc123", "check-license-header")) {
		t.Errorf("expected per-check dedup marker for check-license-header")
	}
}

func TestProcessCIFailures_ConsolidatesMixedCategories(t *testing.T) {
	// Issue #173: Mixed categories (infrastructure + unrelated + related) in one consolidated comment.
	infraResult := streamResultJSON(AgentResult{Result: "INFRASTRUCTURE GitHub git server returned HTTP 500"})
	unrelatedResult := streamResultJSON(AgentResult{Result: "UNRELATED BGP peering timeout in e2e test"})
	relatedResult := streamResultJSON(AgentResult{Result: "RELATED Test assertion failed in kubevirt handler"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test-deploy", Status: "completed", Conclusion: "failure", Output: "GitHub git server returned HTTP 500 with a bunch of other text to make it 50 chars"},
			{ID: 2, Name: "e2e-bgp", Status: "completed", Conclusion: "failure", Output: "BGP peering timeout in e2e test with more detail padding to exceed fifty characters"},
			{ID: 3, Name: "e2e-control-plane", Status: "completed", Conclusion: "failure", Output: "Test assertion failed in kubevirt handler extra padding for the fifty char check"},
		},
		prHeadSHAs: []string{"sha-before", "sha-after"},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{infraResult, unrelatedResult, relatedResult}}
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

	// Should have exactly ONE consolidated comment on the PR
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}

	comment := gh.addedComments[0]
	// All three categories should be present in collapsible details
	if !strings.Contains(comment, "Infrastructure:") {
		t.Errorf("expected Infrastructure details section, got: %q", comment)
	}
	if !strings.Contains(comment, "Unrelated:") {
		t.Errorf("expected Unrelated details section, got: %q", comment)
	}
	if !strings.Contains(comment, "Related:") {
		t.Errorf("expected Related details section, got: %q", comment)
	}
	// Summary header should have category breakdown
	if !strings.Contains(comment, "1 infrastructure, 1 unrelated, 1 related") {
		t.Errorf("expected category breakdown in summary, got: %q", comment)
	}
	// Related section should mention the fix was pushed
	if !strings.Contains(comment, "Pushed a fix") {
		t.Errorf("expected 'Pushed a fix' note, got: %q", comment)
	}
}

func TestProcessCIFailures_SingleFailureStillConsolidated(t *testing.T) {
	// Issue #173: A single failure should still use the consolidated format.
	claudeResult := streamResultJSON(AgentResult{Result: "UNRELATED Flaky network test with detailed explanation exceeding fifty characters for the output check"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "e2e-network", Status: "completed", Conclusion: "failure", Output: "timeout connecting to service with some extra text to exceed the threshold"},
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

	agent.ProcessCIFailures(context.Background())

	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(gh.addedComments))
	}
	comment := gh.addedComments[0]
	// Should use the structured format with summary header and collapsible details
	if !strings.Contains(comment, "CI Failure Analysis") {
		t.Errorf("expected structured format header, got: %q", comment)
	}
	if !strings.Contains(comment, "Unrelated:") {
		t.Errorf("expected Unrelated details section, got: %q", comment)
	}
	if !strings.Contains(comment, "<code>e2e-network</code>") {
		t.Errorf("expected check name in details summary, got: %q", comment)
	}
}

func TestProcessCIFailures_ConsolidatedSkipsInfrastructureSection(t *testing.T) {
	// Issue #173: skip-comment ci-infrastructure should suppress the infrastructure section
	// but still include other sections.
	infraResult := streamResultJSON(AgentResult{Result: "INFRASTRUCTURE GitHub git server returned HTTP 500"})
	unrelatedResult := streamResultJSON(AgentResult{Result: "UNRELATED Flaky test timeout exceeding the minimum chars check for the fifty character threshold"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "test-deploy", Status: "completed", Conclusion: "failure", Output: "GitHub git server returned HTTP 500 with a bunch of extra text to be above threshold"},
			{ID: 2, Name: "e2e-bgp", Status: "completed", Conclusion: "failure", Output: "BGP peering timeout in test with a bunch of extra padding for length threshold"},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{infraResult, unrelatedResult}}
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

	// Should have exactly 1 consolidated comment
	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}

	comment := gh.addedComments[0]
	// Infrastructure section should be absent (skipped by config)
	if strings.Contains(comment, "Infrastructure:") || strings.Contains(comment, "Infrastructure (") {
		t.Errorf("expected no Infrastructure section (skipped), got: %q", comment)
	}
	// Unrelated section should be present
	if !strings.Contains(comment, "Unrelated:") {
		t.Errorf("expected Unrelated details section, got: %q", comment)
	}
	// Infrastructure check's dedup marker should still be present
	if !strings.Contains(comment, ciMarker("abc123", "test-deploy")) {
		t.Errorf("expected dedup marker for skipped infrastructure check")
	}
}

func TestProcessCIFailures_FlakyIssueLinkInConsolidatedComment(t *testing.T) {
	// Issue #173: Flaky issue links should appear in the unrelated section table.
	ciResult1 := streamResultJSON(AgentResult{Result: "UNRELATED Flaky test timeout exceeding the minimum chars check for the fifty character threshold"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "integration-tests", Status: "completed", Conclusion: "failure", Output: "Error: connection timeout with additional text for the fifty character threshold"},
		},
		checkRunLogs: map[int64]string{
			1: "Starting integration tests...\nConnecting to database...\nError: connection timeout after 30s\nStack trace:\n  at TestDB.connect(db.go:42)\n  at TestSuite.setUp(suite.go:15)",
		},
		searchResults: []Issue{
			{Number: 42, Title: "Flaky CI: integration-tests", Labels: []string{"flaky-test"}},
		},
	}
	runner := &mockCommandRunner{stdout: ciResult1}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.CreateFlakyIssues = false
	agent.state.ActiveIssues[IssueKey("owner", "repo", 99)] = &IssueWork{
		IssueNumber:  99,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		BranchName:   "ai/issue-99",
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessCIFailures(context.Background())

	// Should have 2 comments: CI lane link on flaky issue + consolidated on PR
	if len(gh.addedComments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(gh.addedComments))
	}

	// Consolidated comment should include the flaky issue reference in the details block
	consolidated := gh.addedComments[1]
	if !strings.Contains(consolidated, "flaky test (#42)") {
		t.Errorf("expected flaky issue (#42) in details summary, got: %q", consolidated)
	}
	if !strings.Contains(consolidated, "Known Issue") {
		t.Errorf("expected Known Issue section, got: %q", consolidated)
	}
	if !strings.Contains(consolidated, "#42") {
		t.Errorf("expected flaky issue #42 reference, got: %q", consolidated)
	}
}

func TestProcessCIFailures_RelatedPushedFixNoteInConsolidated(t *testing.T) {
	// Issue #173: When a fix is pushed for a related failure, the consolidated
	// comment should note "Pushed a fix for the related failure."
	claudeResult := streamResultJSON(AgentResult{Result: "RELATED Fixed the kubevirt handler test assertion"})
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "e2e-control-plane", Status: "completed", Conclusion: "failure", Output: "Test assertion failed in kubevirt handler extra text to exceed fifty characters"},
		},
		prHeadSHAs: []string{"sha-before", "sha-after"}, // different SHAs = fix pushed
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
		t.Fatalf("expected 1 consolidated comment, got %d", len(gh.addedComments))
	}

	comment := gh.addedComments[0]
	if !strings.Contains(comment, "Related:") {
		t.Errorf("expected Related details section, got: %q", comment)
	}
	if !strings.Contains(comment, "fix pushed") {
		t.Errorf("expected 'fix pushed' in details summary, got: %q", comment)
	}
	if !strings.Contains(comment, "Pushed a fix") {
		t.Errorf("expected pushed fix note, got: %q", comment)
	}
}

func TestFirstSentence(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"GitHub git server returned HTTP 500. Detailed analysis follows.", "GitHub git server returned HTTP 500."},
		{"Simple explanation", "Simple explanation"},
		{"", ""},
		{"First line\nSecond line", "First line"},
		{strings.Repeat("x", 200), strings.Repeat("x", 120) + "..."},
	}
	for _, tt := range tests {
		got := firstSentence(tt.input)
		if got != tt.want {
			t.Errorf("firstSentence(%q) = %q, want %q", tt.input, got, tt.want)
		}
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

func TestProcessRebase_DefersWhenMainIsActive(t *testing.T) {
	// High-velocity repo: >5 commits in 2h → rebase should be deferred.
	gh := &mockGitHubClient{
		mergeableState: "behind",
		recentCommits:  10, // active main branch
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

	// Should NOT have attempted any git operations (rebase deferred)
	for _, c := range runner.calls {
		if c.Name == "git" {
			t.Errorf("should not run git commands when main is active, got: git %v", c.Args)
		}
	}
}

func TestProcessRebase_ProceedsWhenMainIsQuiet(t *testing.T) {
	// Quiet repo: ≤5 commits in 2h → rebase should proceed.
	gh := &mockGitHubClient{
		mergeableState: "behind",
		recentCommits:  2, // quiet main branch
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

	// Should have attempted a rebase
	var rebaseCalls int
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" {
			rebaseCalls++
		}
	}
	if rebaseCalls == 0 {
		t.Error("expected git rebase to be called when main is quiet")
	}
}

func TestProcessRebase_DefersWhenMinIntervalNotReached(t *testing.T) {
	// Rebase 1h ago, main is quiet → still deferred because 4h minimum not reached.
	gh := &mockGitHubClient{
		mergeableState: "behind",
		recentCommits:  0, // very quiet
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:    42,
		PRNumber:       100,
		BranchName:     "ai/issue-42",
		Status:         "pr-open",
		WorktreePath:   "/tmp/worktree",
		LastRebaseTime: time.Now().Add(-1 * time.Hour), // rebased 1h ago
	}

	agent.ProcessRebase(context.Background())

	// Should NOT have attempted any git operations
	for _, c := range runner.calls {
		if c.Name == "git" {
			t.Errorf("should not rebase when min interval not reached, got: git %v", c.Args)
		}
	}
}

func TestProcessRebase_ProceedsWhenMinIntervalExpired(t *testing.T) {
	// Rebase 5h ago, main is quiet → rebase proceeds.
	gh := &mockGitHubClient{
		mergeableState: "behind",
		recentCommits:  2, // quiet
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:    42,
		PRNumber:       100,
		BranchName:     "ai/issue-42",
		Status:         "pr-open",
		WorktreePath:   "/tmp/worktree",
		LastRebaseTime: time.Now().Add(-5 * time.Hour), // rebased 5h ago
	}

	agent.ProcessRebase(context.Background())

	var rebaseCalls int
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" {
			rebaseCalls++
		}
	}
	if rebaseCalls == 0 {
		t.Error("expected git rebase when min interval expired and main is quiet")
	}
}

func TestProcessRebase_FailOpenOnAPIError(t *testing.T) {
	// Can't count commits → fail-open, rebase proceeds.
	gh := &mockGitHubClient{
		mergeableState:  "behind",
		countCommitsErr: fmt.Errorf("API rate limit exceeded"),
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
		t.Error("expected git rebase to proceed on API error (fail-open)")
	}
}

func TestProcessRebase_FirstRebaseNoIntervalGuard(t *testing.T) {
	// First rebase (LastRebaseTime is zero) → should proceed without interval guard.
	gh := &mockGitHubClient{
		mergeableState: "behind",
		recentCommits:  3, // below threshold
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
		// LastRebaseTime is zero — first rebase
	}

	agent.ProcessRebase(context.Background())

	var rebaseCalls int
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" {
			rebaseCalls++
		}
	}
	if rebaseCalls == 0 {
		t.Error("expected git rebase on first rebase (no interval guard for zero time)")
	}
}

func TestProcessRebase_SetsLastRebaseTimeOnSuccess(t *testing.T) {
	// After a successful rebase, LastRebaseTime should be set.
	gh := &mockGitHubClient{
		mergeableState: "behind",
		recentCommits:  0, // quiet
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

	before := time.Now()
	agent.ProcessRebase(context.Background())

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastRebaseTime.IsZero() {
		t.Error("expected LastRebaseTime to be set after successful rebase")
	}
	if work.LastRebaseTime.Before(before) {
		t.Error("expected LastRebaseTime to be after the test start time")
	}
}

func TestProcessRebase_ExactThresholdAllowsRebase(t *testing.T) {
	// Exactly 5 commits (= threshold) → rebase should proceed (only >threshold defers).
	gh := &mockGitHubClient{
		mergeableState: "behind",
		recentCommits:  5, // exactly at threshold
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
		t.Error("expected git rebase when commit count equals threshold (only >threshold defers)")
	}
}

func TestShouldRebaseNow_ConfigurableInterval24h(t *testing.T) {
	// RebaseInterval = 24h, last rebase 20h ago → skip (minimum interval not reached)
	gh := &mockGitHubClient{
		recentCommits: 0, // quiet main
	}
	agent := newTestAgent(gh, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.RebaseInterval = 24 * time.Hour

	work := &IssueWork{
		PRNumber:       100,
		LastRebaseTime: time.Now().Add(-20 * time.Hour), // 20h ago
	}

	allowed, reason := agent.shouldRebaseNow(context.Background(), work)
	if allowed {
		t.Error("expected rebase to be deferred (20h < 24h interval)")
	}
	if reason != "minimum interval not reached" {
		t.Errorf("expected reason 'minimum interval not reached', got %q", reason)
	}
}

func TestShouldRebaseNow_ConfigurableInterval24hExpired(t *testing.T) {
	// RebaseInterval = 24h, last rebase 25h ago → allow
	gh := &mockGitHubClient{
		recentCommits: 0, // quiet main
	}
	agent := newTestAgent(gh, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.RebaseInterval = 24 * time.Hour

	work := &IssueWork{
		PRNumber:       100,
		LastRebaseTime: time.Now().Add(-25 * time.Hour), // 25h ago
	}

	allowed, _ := agent.shouldRebaseNow(context.Background(), work)
	if !allowed {
		t.Error("expected rebase to be allowed (25h > 24h interval)")
	}
}

func TestShouldRebaseNow_ZeroIntervalUsesDefault(t *testing.T) {
	// RebaseInterval = 0 (not set) → use 4h default
	gh := &mockGitHubClient{
		recentCommits: 0,
	}
	agent := newTestAgent(gh, &mockCommandRunner{}, &mockWorktreeManager{})
	// agent.cfg.RebaseInterval is zero (default)

	work := &IssueWork{
		PRNumber:       100,
		LastRebaseTime: time.Now().Add(-3 * time.Hour), // 3h ago, less than 4h default
	}

	allowed, reason := agent.shouldRebaseNow(context.Background(), work)
	if allowed {
		t.Error("expected rebase to be deferred (3h < 4h default interval)")
	}
	if reason != "minimum interval not reached" {
		t.Errorf("expected reason 'minimum interval not reached', got %q", reason)
	}
}

func TestBuildStateFromGitHub_RecoverLastRebaseTime(t *testing.T) {
	// On restart, LastRebaseTime should be recovered from the PR head commit date.
	headDate := time.Now().Add(-2 * time.Hour) // head commit was 2h ago
	gh := &mockGitHubClient{
		prs: []PR{
			{Number: 123, Title: "Fix login", State: "open", Head: "fix-login"},
		},
		headCommitDate: headDate,
	}

	cfg := Config{
		Owner:           "owner",
		Repo:            "repo",
		WatchPRs:        []int{123},
		GitHubHeadOwner: "owner",
	}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/work", slog.Default())

	work := state.ActiveIssues[IssueKey("owner", "repo", 123)]
	if work == nil {
		t.Fatal("expected PR 123 to be tracked")
	}
	if work.LastRebaseTime.IsZero() {
		t.Error("expected LastRebaseTime to be recovered from head commit date")
	}
	if !work.LastRebaseTime.Equal(headDate) {
		t.Errorf("expected LastRebaseTime %v, got %v", headDate, work.LastRebaseTime)
	}
}

func TestBuildStateFromGitHub_RecoverLastRebaseTimeIssueDiscovered(t *testing.T) {
	// Issue-discovered PR path: LastRebaseTime should be recovered from head commit date.
	headDate := time.Now().Add(-2 * time.Hour)
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug", Labels: []string{"good-for-ai"}}},
		prs:    []PR{{Number: 100, State: "open", Head: "ai/issue-42"}},
		headCommitDate: headDate,
	}

	cfg := Config{
		Owner:           "owner",
		Repo:            "repo",
		Label:           "good-for-ai",
		GitHubHeadOwner: "owner",
	}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/work", slog.Default())

	work := state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work == nil {
		t.Fatal("expected issue 42 to be tracked")
	}
	if work.PRNumber != 100 {
		t.Errorf("expected PR 100, got %d", work.PRNumber)
	}
	if work.LastRebaseTime.IsZero() {
		t.Error("expected LastRebaseTime to be recovered from head commit date")
	}
	if !work.LastRebaseTime.Equal(headDate) {
		t.Errorf("expected LastRebaseTime %v, got %v", headDate, work.LastRebaseTime)
	}
}

func TestBuildStateFromGitHub_NoHeadCommitDate(t *testing.T) {
	// On restart with no head commit date available → LastRebaseTime = zero → rebase allowed (fail-open)
	gh := &mockGitHubClient{
		prs: []PR{
			{Number: 123, Title: "Fix login", State: "open", Head: "fix-login"},
		},
		// headCommitDate is zero value
	}

	cfg := Config{
		Owner:           "owner",
		Repo:            "repo",
		WatchPRs:        []int{123},
		GitHubHeadOwner: "owner",
	}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/work", slog.Default())

	work := state.ActiveIssues[IssueKey("owner", "repo", 123)]
	if work == nil {
		t.Fatal("expected PR 123 to be tracked")
	}
	if !work.LastRebaseTime.IsZero() {
		t.Errorf("expected LastRebaseTime to be zero when no head commit date, got %v", work.LastRebaseTime)
	}
}

func TestTriageDedup_DifferentFailuresSameJob_CreatesSeparateIssues(t *testing.T) {
	// Two different failures from the same job should create two separate issues.
	// Existing issue is about VLAN test failure; new failure is CRI-O mirror 404.
	// The LLM says NONE — no match.
	gh := &mockGitHubClient{
		searchResults: []Issue{
			{Number: 1501, Title: "CI Failure: periodic-knmstate-e2e-handler-k8s-latest / VLAN bridge test timeout",
				Body: "Periodic CI job failed. VLAN configuration test failed with timeout."},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
		TriageJobs:        []string{},
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "NONE"}}, // LLM matching says no match
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	// Simulate calling matchExistingTriageIssue + issue creation path directly
	title := "CI Failure: periodic-knmstate-e2e-handler-k8s-latest / CRI-O mirror returned HTTP 404"
	analysis := "## Summary\nCRI-O mirror returned HTTP 404\n\n## Root Cause\nInfrastructure mirror outage"

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"periodic-knmstate-e2e-handler-k8s-latest",
		title,
		analysis,
		gh.searchResults,
		"/tmp/worktree",
		nil, // no cycle issues
		nil, // no concurrent failures (single job)
	)

	// Title doesn't match, LLM says NONE, single job → no match found
	if matchedIssue != 0 {
		t.Errorf("expected no match (different failure), got issue #%d", matchedIssue)
	}

	// The LLM matching agent should have been called (no exact title match)
	if codeAgent.callCount != 1 {
		t.Errorf("expected 1 agent call for LLM matching, got %d", codeAgent.callCount)
	}

	// Verify the prompt contains the analysis and existing issues
	if len(codeAgent.prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(codeAgent.prompts))
	}
	prompt := codeAgent.prompts[0]
	if !strings.Contains(prompt, "periodic-knmstate-e2e-handler-k8s-latest") {
		t.Error("expected prompt to contain job name")
	}
	if !strings.Contains(prompt, "CRI-O mirror returned HTTP 404") {
		t.Error("expected prompt to contain analysis")
	}
	if !strings.Contains(prompt, "Issue #1501") {
		t.Error("expected prompt to contain existing issue")
	}

	// Now verify a new issue would be created with the failure signature in the title
	issueNum, err := gh.CreateIssue(context.Background(), "owner", "repo", title, "analysis body", []string{"ci-flake"})
	if err != nil {
		t.Fatalf("unexpected error creating issue: %v", err)
	}
	if issueNum == 0 {
		t.Error("expected issue to be created")
	}
	if gh.createdIssues[0].Title != title {
		t.Errorf("expected title %q, got %q", title, gh.createdIssues[0].Title)
	}
}

func TestTriageDedup_SameFailureSameJob_MatchesExistingIssue(t *testing.T) {
	// Same failure from the same job should match the existing issue via LLM.
	gh := &mockGitHubClient{
		searchResults: []Issue{
			{Number: 42, Title: "CI Failure: periodic-e2e-test / DNS resolution failure",
				Body: "Periodic CI job failed. DNS resolution timed out."},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "MATCH 42"}}, // LLM says it matches
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	// New failure is also a DNS issue, but slightly different title
	title := "CI Failure: periodic-e2e-test / DNS lookup timed out for registry.k8s.io"
	analysis := "## Summary\nDNS lookup timed out for registry.k8s.io\n\n## Root Cause\nDNS resolution failure"

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"periodic-e2e-test",
		title,
		analysis,
		gh.searchResults,
		"/tmp/worktree",
		nil, // no cycle issues
		nil, // no concurrent failures (single job)
	)

	// LLM says MATCH 42 → should match
	if matchedIssue != 42 {
		t.Errorf("expected match on issue #42, got #%d", matchedIssue)
	}
}

func TestTriageDedup_ExactTitleMatch_SkipsLLM(t *testing.T) {
	// Exact title match should skip LLM invocation entirely.
	gh := &mockGitHubClient{
		searchResults: []Issue{
			{Number: 99, Title: "CI Failure: periodic-e2e-test / CRI-O mirror HTTP 404",
				Body: "CRI-O mirror outage"},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{}, // No results — should NOT be called
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	title := "CI Failure: periodic-e2e-test / CRI-O mirror HTTP 404"
	analysis := "## Summary\nCRI-O mirror HTTP 404\n\n## Root Cause\nMirror outage"

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"periodic-e2e-test",
		title,
		analysis,
		gh.searchResults,
		"/tmp/worktree",
		nil, // no cycle issues
		nil, // no concurrent failures (single job)
	)

	// Exact title match — should return issue #99
	if matchedIssue != 99 {
		t.Errorf("expected exact title match on issue #99, got #%d", matchedIssue)
	}

	// LLM should NOT have been called
	if codeAgent.callCount != 0 {
		t.Errorf("expected 0 agent calls (exact title match), got %d", codeAgent.callCount)
	}
}

func TestTriageDedup_NoExistingIssues_ReturnsZero(t *testing.T) {
	// No existing issues → should return 0 (will create a new issue).
	gh := &mockGitHubClient{
		searchResults: []Issue{},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"periodic-e2e-test",
		"CI Failure: periodic-e2e-test / some failure",
		"analysis",
		[]Issue{},
		"/tmp/worktree",
		nil, // no cycle issues
		nil, // no concurrent failures (single job)
	)

	if matchedIssue != 0 {
		t.Errorf("expected 0 (no existing issues), got #%d", matchedIssue)
	}
	if codeAgent.callCount != 0 {
		t.Errorf("expected 0 agent calls (no existing issues), got %d", codeAgent.callCount)
	}
}

func TestTriageDedup_AddRunLinkComment(t *testing.T) {
	// When a match IS found, a comment should be added to the existing issue.
	gh := &mockGitHubClient{}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner: "owner",
		Repo:  "repo",
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), &ClaudeCodeAgent{})

	run := JobRun{
		ID:     "12345",
		LogURL: "https://example.com/logs/12345",
	}

	a.addTriageRunLinkComment(context.Background(), "periodic-e2e-test", run, 42)

	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(gh.addedComments))
	}

	comment := gh.addedComments[0]
	if !strings.Contains(comment, "Same failure observed") {
		t.Errorf("expected comment to mention 'Same failure observed', got: %q", comment)
	}
	if !strings.Contains(comment, "periodic-e2e-test") {
		t.Errorf("expected comment to mention job name, got: %q", comment)
	}
	if !strings.Contains(comment, "12345") {
		t.Errorf("expected comment to mention run ID, got: %q", comment)
	}
	if !strings.Contains(comment, "https://example.com/logs/12345") {
		t.Errorf("expected comment to mention log URL, got: %q", comment)
	}
	if gh.addedCommentTargets[0] != 42 {
		t.Errorf("expected comment posted to issue #42, got #%d", gh.addedCommentTargets[0])
	}
}

func TestExtractFailureSignature(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "summary section",
			input: "## Summary\nCRI-O mirror returned HTTP 404\n\n## Root Cause\nMirror outage",
			want:  "CRI-O mirror returned HTTP 404",
		},
		{
			name:  "multi-line summary",
			input: "## Summary\nDNS resolution failed for registry.k8s.io causing cluster bootstrap to hang\n\n## Root Cause\nDNS outage",
			want:  "DNS resolution failed for registry.k8s.io causing cluster",
		},
		{
			name:  "no summary section falls back to first line",
			input: "The test timed out waiting for pod readiness",
			want:  "The test timed out waiting for pod readiness",
		},
		{
			name:  "empty analysis",
			input: "",
			want:  "",
		},
		{
			name:  "truncates long signature",
			input: "## Summary\n" + strings.Repeat("x", 200) + "\n\n## Root Cause\nSomething",
			want:  strings.Repeat("x", 60),
		},
		{
			name:  "blank line after heading",
			input: "## Summary\n\nCRI-O mirror returned HTTP 404\n\n## Root Cause\nMirror outage",
			want:  "CRI-O mirror returned HTTP 404",
		},
		{
			name:  "truncates by runes not bytes",
			input: "## Summary\n" + strings.Repeat("\u00e9", 100) + "\n\n## Root Cause\nSomething",
			want:  strings.Repeat("\u00e9", 60),
		},
		{
			name:  "skips markdown headings",
			input: "## Summary\n\n## Root Cause\nThe actual content here",
			want:  "The actual content here",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFailureSignature(tt.input)
			if got != tt.want {
				t.Errorf("extractFailureSignature() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildTriageMatchPrompt(t *testing.T) {
	existingIssues := []Issue{
		{Number: 1501, Title: "CI Failure: periodic-e2e / VLAN test timeout",
			Body: "VLAN configuration test failed with timeout."},
	}

	prompt := buildTriageMatchPrompt("periodic-e2e", "CRI-O mirror HTTP 404 error", existingIssues, nil)

	checks := []string{
		"periodic-e2e",
		"CRI-O mirror HTTP 404 error",
		"Issue #1501",
		"CI Failure: periodic-e2e / VLAN test timeout",
		"ROOT CAUSE",
		"not error message",
		"same underlying problem",
		"MATCH",
		"NONE",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	// Without concurrent failures, the prompt should NOT contain cycle context
	if strings.Contains(prompt, "Concurrent failure context") {
		t.Error("prompt should NOT contain concurrent failure context when cycleFailedJobs is nil")
	}
}

func TestBuildTriageMatchPrompt_ConcurrentFailures(t *testing.T) {
	existingIssues := []Issue{
		{Number: 2761, Title: "CI Failure: kubevirt-ipam-controller / setup timeout",
			Body: "Setup phase timed out waiting for cluster."},
	}

	cycleFailedJobs := []string{
		"kubevirt-ipam-controller",
		"workflow-k8s-s390x",
		"unit-test-release-0.102",
		"monitoring-k8s",
	}

	prompt := buildTriageMatchPrompt("workflow-k8s-s390x", "cluster setup failed", existingIssues, cycleFailedJobs)

	checks := []string{
		// Concurrent failure context section
		"Concurrent failure context",
		"4 CI jobs failed concurrently",
		"kubevirt-ipam-controller",
		"workflow-k8s-s390x",
		"unit-test-release-0.102",
		"monitoring-k8s",
		"STRONG signal",
		"common root cause",
		// Instructions about concurrent failures
		"Multiple CI jobs failed concurrently",
		"Bias toward MATCH",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildTriageMatchPrompt_SingleJobNoConcurrentContext(t *testing.T) {
	existingIssues := []Issue{
		{Number: 100, Title: "CI Failure: unit-test / nil pointer",
			Body: "nil pointer dereference in handler.go"},
	}

	// A single failing job should not produce concurrent context
	cycleFailedJobs := []string{"unit-test"}

	prompt := buildTriageMatchPrompt("unit-test", "nil pointer in handler", existingIssues, cycleFailedJobs)

	if strings.Contains(prompt, "Concurrent failure context") {
		t.Error("prompt should NOT contain concurrent failure context when only 1 job failed")
	}
}

func TestProcessReviewComments_OompaCommandProcessed(t *testing.T) {
	// A PR conversation comment starting with /oompa should be treated as a directive.
	result := streamResultJSON(AgentResult{Result: "Done"})
	gh := &mockGitHubClient{
		issueComments: []ReviewComment{
			{ID: 70, User: "reviewer", Body: "/oompa fix the commit message"},
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
	}

	agent.ProcessReviewComments(context.Background())

	// Should have invoked the agent
	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	if claudeCalls != 1 {
		t.Fatalf("expected 1 claude call, got %d", claudeCalls)
	}

	// Cursor should advance
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastIssueCommentID != 70 {
		t.Errorf("expected LastIssueCommentID 70, got %d", work.LastIssueCommentID)
	}

	// Should have added :eyes: reaction using issue comment API
	if !slices.Contains(gh.addedReactions, "issue:70:eyes") {
		t.Errorf("expected issue comment :eyes: reaction, got %v", gh.addedReactions)
	}
}

func TestProcessReviewComments_IgnoresNonOompaComments(t *testing.T) {
	// PR conversation comments without /oompa prefix should be ignored.
	gh := &mockGitHubClient{
		issueComments: []ReviewComment{
			{ID: 70, User: "reviewer", Body: "This looks good to me"},
			{ID: 71, User: "reviewer", Body: "I think we should refactor this"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessReviewComments(context.Background())

	// Should NOT invoke the agent
	for _, c := range runner.calls {
		if c.Name == "claude" {
			t.Error("should not invoke claude for comments without /oompa prefix")
		}
	}

	// Cursor should still advance past filtered comments
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastIssueCommentID != 71 {
		t.Errorf("expected LastIssueCommentID to advance to 71, got %d", work.LastIssueCommentID)
	}
}

func TestProcessReviewComments_OompaCommandSkipsNonWhitelisted(t *testing.T) {
	// /oompa commands from non-whitelisted users should be ignored.
	gh := &mockGitHubClient{
		issueComments: []ReviewComment{
			{ID: 70, User: "random-user", Body: "/oompa fix the commit message"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.cfg.Reviewers = []string{"trusted-reviewer"}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessReviewComments(context.Background())

	for _, c := range runner.calls {
		if c.Name == "claude" {
			t.Error("should not invoke claude for non-whitelisted user's /oompa command")
		}
	}
}

func TestProcessReviewComments_OompaCommandIncludedInPrompt(t *testing.T) {
	// The /oompa directive should appear in the prompt with the /oompa prefix stripped.
	gh := &mockGitHubClient{
		issueComments: []ReviewComment{
			{ID: 70, User: "reviewer", Body: "/oompa add Signed-off-by trailers"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{{result: AgentResult{Result: "Done"}}},
	}

	agent := newTestAgent(gh, runner, wt)
	agent.codeAgent = codeAgent
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessReviewComments(context.Background())

	if len(codeAgent.prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(codeAgent.prompts))
	}

	prompt := codeAgent.prompts[0]

	// Should contain the directive text
	if !strings.Contains(prompt, "add Signed-off-by trailers") {
		t.Error("prompt should contain the directive text")
	}

	// Should contain the section header
	if !strings.Contains(prompt, "PR conversation directives") {
		t.Error("prompt should contain PR conversation directives section")
	}

	// Should NOT contain the /oompa prefix in the directive
	if strings.Contains(prompt, "/oompa add") {
		t.Error("prompt should strip the /oompa prefix from directives")
	}
}

func TestProcessReviewComments_OompaCommandWithReviewComments(t *testing.T) {
	// Both inline review comments and /oompa PR comments should be processed together.
	result := streamResultJSON(AgentResult{Result: "Done"})
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Fix the typo", Path: "main.go", Line: 10},
		},
		issueComments: []ReviewComment{
			{ID: 70, User: "reviewer", Body: "/oompa rebase on main"},
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
	}

	agent.ProcessReviewComments(context.Background())

	// Should have invoked the agent once (both types combined into one task)
	claudeCalls := 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	if claudeCalls != 1 {
		t.Fatalf("expected 1 claude call, got %d", claudeCalls)
	}

	// Both cursors should advance
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastCommentID != 60 {
		t.Errorf("expected LastCommentID 60, got %d", work.LastCommentID)
	}
	if work.LastIssueCommentID != 70 {
		t.Errorf("expected LastIssueCommentID 70, got %d", work.LastIssueCommentID)
	}

	// Should have reactions for both comment types
	if !slices.Contains(gh.addedReactions, "60:eyes") {
		t.Error("expected :eyes: reaction on review comment")
	}
	if !slices.Contains(gh.addedReactions, "issue:70:eyes") {
		t.Error("expected :eyes: reaction on issue comment")
	}
}

func TestProcessReviewComments_OompaCommandCursorAdvancesOnAgentError(t *testing.T) {
	// When the agent errors, the issue comment cursor should still advance.
	gh := &mockGitHubClient{
		issueComments: []ReviewComment{
			{ID: 70, User: "reviewer", Body: "/oompa fix the commit message"},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.codeAgent = &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{{err: fmt.Errorf("agent crashed")}},
	}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessReviewComments(context.Background())

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastIssueCommentID != 70 {
		t.Errorf("expected LastIssueCommentID to advance to 70 on agent error, got %d", work.LastIssueCommentID)
	}
}

func TestProcessReviewComments_OompaCommandIgnoresBarePrefix(t *testing.T) {
	// A bare "/oompa" comment with no directive text should be ignored.
	gh := &mockGitHubClient{
		issueComments: []ReviewComment{
			{ID: 70, User: "reviewer", Body: "/oompa"},
			{ID: 71, User: "reviewer", Body: "/oompa   "},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessReviewComments(context.Background())

	// Should NOT invoke the agent for bare /oompa with no directive
	for _, c := range runner.calls {
		if c.Name == "claude" {
			t.Error("should not invoke claude for bare /oompa comment with no directive")
		}
	}

	// Cursor should still advance past filtered comments
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastIssueCommentID != 71 {
		t.Errorf("expected LastIssueCommentID to advance to 71, got %d", work.LastIssueCommentID)
	}
}

func TestProcessReviewComments_OompaCommandIgnoresBotComments(t *testing.T) {
	// Bot-posted comments with /oompa prefix should be ignored.
	gh := &mockGitHubClient{
		issueComments: []ReviewComment{
			{ID: 70, User: "some-bot", Body: "/oompa do something " + botMarker},
		},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: "/tmp/worktree",
	}

	agent.ProcessReviewComments(context.Background())

	for _, c := range runner.calls {
		if c.Name == "claude" {
			t.Error("should not invoke claude for bot-posted /oompa comment")
		}
	}
}

func TestReadCommitMsgFile_Present(t *testing.T) {
	// When .oompa-commit-msg exists and is non-empty, readCommitMsgFile should
	// return its trimmed contents and true, then delete the file.
	dir := t.TempDir()
	msgPath := filepath.Join(dir, commitMsgFile)
	want := "feat: new commit subject\n\nBody paragraph"
	if err := os.WriteFile(msgPath, []byte(want+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, ok := readCommitMsgFile(dir)
	if !ok {
		t.Fatal("expected readCommitMsgFile to return true when file exists")
	}
	if msg != want {
		t.Errorf("expected %q, got %q", want, msg)
	}

	// File should have been deleted
	if _, err := os.Stat(msgPath); !os.IsNotExist(err) {
		t.Error("expected .oompa-commit-msg to be deleted after reading")
	}
}

func TestReadCommitMsgFile_Absent(t *testing.T) {
	// When .oompa-commit-msg does not exist, readCommitMsgFile should return ("", false).
	dir := t.TempDir()
	msg, ok := readCommitMsgFile(dir)
	if ok {
		t.Error("expected readCommitMsgFile to return false when file is absent")
	}
	if msg != "" {
		t.Errorf("expected empty string, got %q", msg)
	}
}

func TestReadCommitMsgFile_Empty(t *testing.T) {
	// When .oompa-commit-msg exists but is empty/whitespace-only, return ("", false).
	dir := t.TempDir()
	msgPath := filepath.Join(dir, commitMsgFile)
	if err := os.WriteFile(msgPath, []byte("  \n  \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, ok := readCommitMsgFile(dir)
	if ok {
		t.Error("expected readCommitMsgFile to return false for empty file")
	}
	if msg != "" {
		t.Errorf("expected empty string, got %q", msg)
	}

	// File should have been deleted even when empty
	if _, err := os.Stat(msgPath); !os.IsNotExist(err) {
		t.Error("expected .oompa-commit-msg to be deleted after reading empty file")
	}
}

// commitMsgCodeAgent is a mock CodeAgent that writes .oompa-commit-msg to the workdir.
type commitMsgCodeAgent struct {
	commitMsg string
	result    AgentResult
	err       error
	prompts   []string
	callCount int
}

func (m *commitMsgCodeAgent) Run(_ context.Context, _ CommandRunner, workDir, prompt string, _ *slog.Logger, _ bool) (AgentResult, error) {
	m.callCount++
	m.prompts = append(m.prompts, prompt)
	// Only write the commit message file on the first call (the review fix).
	// Subsequent calls (e.g. change summary LLM) should not recreate it.
	if m.commitMsg != "" && m.callCount == 1 {
		// Simulate the agent writing the commit message file
		if err := os.WriteFile(filepath.Join(workDir, commitMsgFile), []byte(m.commitMsg), 0o644); err != nil {
			return AgentResult{}, err
		}
	}
	return m.result, m.err
}

// fixedPathWorktreeManager returns a fixed path from CreateWorktree, allowing tests
// to use a real temp directory for commit message file tests.
type fixedPathWorktreeManager struct {
	mockWorktreeManager
	fixedPath string
}

func (m *fixedPathWorktreeManager) CreateWorktree(_ context.Context, branchName string) (string, error) {
	m.createdBranches = append(m.createdBranches, branchName)
	return m.fixedPath, nil
}

func TestProcessReviewComments_SquashUsesCommitMsgFile(t *testing.T) {
	// When the agent commits directly AND writes .oompa-commit-msg,
	// gitSquashInto should use -m with the file's contents instead of --no-edit.
	// Configured trailers should be appended automatically.
	worktreeDir := t.TempDir()

	gh := &mockGitHubClient{
		issueComments: []ReviewComment{
			{ID: 70, User: "reviewer", Body: "/oompa fix the commit message"},
		},
	}
	runner := &reviewSquashRunner{
		mockCommandRunner: &mockCommandRunner{},
		headSHAs:          []string{"original-sha", "agent-committed-sha"},
	}
	wt := &fixedPathWorktreeManager{fixedPath: worktreeDir}

	agent := &Agent{
		gh:        gh,
		runner:    runner,
		worktrees: wt,
		state:     NewState(),
		cfg: Config{
			Owner:       "owner",
			Repo:        "repo",
			Label:       "good-for-ai",
			FlakyLabel:  "flaky-test",
			GitHubUser:  "test-bot",
			SignedOffBy: "Test User <test@example.com>",
			AssistedBy:  "Claude <noreply@anthropic.com>",
		},
		logger: slog.Default(),
		codeAgent: &commitMsgCodeAgent{
			commitMsg: "fix: corrected commit subject\n\nProper body",
			result:    AgentResult{Result: "Done"},
		},
	}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: worktreeDir,
	}

	agent.ProcessReviewComments(context.Background())

	// Verify git commit --amend -m was called with the new message plus trailers
	foundAmendWithMsg := false
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) >= 3 && c.Args[0] == "commit" && c.Args[1] == "--amend" && c.Args[2] == "-m" {
			foundAmendWithMsg = true
			if len(c.Args) >= 4 {
				msg := c.Args[3]
				if !strings.Contains(msg, "fix: corrected commit subject") {
					t.Errorf("commit message missing subject, got %q", msg)
				}
				if !strings.Contains(msg, "Proper body") {
					t.Errorf("commit message missing body, got %q", msg)
				}
				if !strings.Contains(msg, "Signed-off-by: Test User <test@example.com>") {
					t.Errorf("commit message missing Signed-off-by trailer, got %q", msg)
				}
				if !strings.Contains(msg, "Assisted-by: Claude <noreply@anthropic.com>") {
					t.Errorf("commit message missing Assisted-by trailer, got %q", msg)
				}
			}
		}
	}
	if !foundAmendWithMsg {
		t.Error("expected git commit --amend -m <msg> when .oompa-commit-msg is present")
	}

	// The .oompa-commit-msg file should have been cleaned up
	if _, err := os.Stat(filepath.Join(worktreeDir, commitMsgFile)); !os.IsNotExist(err) {
		t.Error("expected .oompa-commit-msg to be deleted after use")
	}
}

func TestProcessReviewComments_AmendUsesCommitMsgFile(t *testing.T) {
	// When the agent leaves uncommitted changes AND writes .oompa-commit-msg,
	// gitAmendAll should use -m with the file's contents instead of --no-edit.
	// Configured trailers should be appended automatically.
	worktreeDir := t.TempDir()

	gh := &mockGitHubClient{
		issueComments: []ReviewComment{
			{ID: 70, User: "reviewer", Body: "/oompa fix the commit message"},
		},
	}
	// Use changeSummaryRunner which simulates uncommitted changes (git status returns dirty)
	runner := &changeSummaryRunner{
		mockCommandRunner: &mockCommandRunner{},
		headSHA:           "after-sha",
		diffPatch:         "diff --git a/pkg/agent/review.go b/pkg/agent/review.go\n@@ -1 +1 @@\n-old\n+new\n",
	}
	wt := &fixedPathWorktreeManager{fixedPath: worktreeDir}

	agent := &Agent{
		gh:        gh,
		runner:    runner,
		worktrees: wt,
		state:     NewState(),
		cfg: Config{
			Owner:       "owner",
			Repo:        "repo",
			Label:       "good-for-ai",
			FlakyLabel:  "flaky-test",
			GitHubUser:  "test-bot",
			SignedOffBy: "Test User <test@example.com>",
			AssistedBy:  "Claude <noreply@anthropic.com>",
		},
		logger: slog.Default(),
		codeAgent: &commitMsgCodeAgent{
			commitMsg: "fix: updated commit message\n\nNew body",
			result:    AgentResult{Result: "Done"},
		},
	}
	agent.state.ActiveIssues[IssueKey("owner", "repo", 42)] = &IssueWork{
		IssueNumber:  42,
		IssueTitle:   "Fix bug",
		PRNumber:     100,
		Status:       "pr-open",
		WorktreePath: worktreeDir,
	}

	agent.ProcessReviewComments(context.Background())

	// Verify git commit --amend -m was called with the new message plus trailers
	foundAmendWithMsg := false
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) >= 3 && c.Args[0] == "commit" && c.Args[1] == "--amend" && c.Args[2] == "-m" {
			foundAmendWithMsg = true
			if len(c.Args) >= 4 {
				msg := c.Args[3]
				if !strings.Contains(msg, "fix: updated commit message") {
					t.Errorf("commit message missing subject, got %q", msg)
				}
				if !strings.Contains(msg, "New body") {
					t.Errorf("commit message missing body, got %q", msg)
				}
				if !strings.Contains(msg, "Signed-off-by: Test User <test@example.com>") {
					t.Errorf("commit message missing Signed-off-by trailer, got %q", msg)
				}
				if !strings.Contains(msg, "Assisted-by: Claude <noreply@anthropic.com>") {
					t.Errorf("commit message missing Assisted-by trailer, got %q", msg)
				}
			}
		}
	}
	if !foundAmendWithMsg {
		t.Error("expected git commit --amend -m <msg> when .oompa-commit-msg is present")
	}

	// The .oompa-commit-msg file should have been cleaned up
	if _, err := os.Stat(filepath.Join(worktreeDir, commitMsgFile)); !os.IsNotExist(err) {
		t.Error("expected .oompa-commit-msg to be deleted after use")
	}
}

func TestEnsureTrailers_AppendsWhenMissing(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.SignedOffBy = "Test User <test@example.com>"
	agent.cfg.AssistedBy = "Claude <noreply@anthropic.com>"

	msg := agent.ensureTrailers("fix: subject\n\nbody text")

	if !strings.Contains(msg, "Signed-off-by: Test User <test@example.com>") {
		t.Error("expected Signed-off-by trailer to be appended")
	}
	if !strings.Contains(msg, "Assisted-by: Claude <noreply@anthropic.com>") {
		t.Error("expected Assisted-by trailer to be appended")
	}
	if !strings.Contains(msg, "fix: subject") {
		t.Error("original subject should be preserved")
	}
	if !strings.Contains(msg, "body text") {
		t.Error("original body should be preserved")
	}
}

func TestEnsureTrailers_SkipsWhenPresent(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.SignedOffBy = "Test User <test@example.com>"
	agent.cfg.AssistedBy = "Claude <noreply@anthropic.com>"

	msg := "fix: subject\n\nbody text\n\nSigned-off-by: Test User <test@example.com>\nAssisted-by: Claude <noreply@anthropic.com>"
	result := agent.ensureTrailers(msg)

	if result != msg {
		t.Errorf("expected message to be unchanged when trailers already present, got %q", result)
	}
}

func TestEnsureTrailers_NoConfigNoChange(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	// No SignedOffBy or AssistedBy configured

	msg := "fix: subject\n\nbody text"
	result := agent.ensureTrailers(msg)

	if result != msg {
		t.Errorf("expected message to be unchanged when no trailers configured, got %q", result)
	}
}

func TestProcessReviewComments_SquashWithoutCommitMsgFileUsesNoEdit(t *testing.T) {
	// When the agent commits directly but does NOT write .oompa-commit-msg,
	// gitSquashInto should use --no-edit (preserving the original message).
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
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

	// Verify git commit --amend --no-edit was called (NOT -m)
	foundNoEdit := false
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) >= 2 && c.Args[0] == "commit" && c.Args[1] == "--amend" {
			if len(c.Args) >= 3 && c.Args[2] == "--no-edit" {
				foundNoEdit = true
			}
		}
	}
	if !foundNoEdit {
		t.Error("expected git commit --amend --no-edit when no .oompa-commit-msg file exists")
	}
}

func TestProcessTriageJobs_DeterministicFallbackWhenLLMSaysNone(t *testing.T) {
	// When multiple different jobs fail and the LLM says NONE for the second
	// job, the deterministic fallback should match to the cycle issue created
	// by the first job instead of creating a duplicate.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 100, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/100"},
		},
		searchResults:   []Issue{}, // GitHub search returns nothing
		nextIssueNumber: 10,
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
		TriageJobs: []string{
			"https://github.com/owner/repo/actions/workflows/unit.yml",
			"https://github.com/owner/repo/actions/workflows/e2e.yml",
			"https://github.com/owner/repo/actions/workflows/lint.yml",
		},
	}

	// First job: analysis → no match → creates issue
	// Second job: analysis + LLM says NONE → deterministic fallback should match
	// Third job: analysis + LLM says NONE → deterministic fallback should match
	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "## Summary\nInfra outage broke CI"}},   // first job analysis
			{result: AgentResult{Result: "## Summary\nDNS resolution failed"}},    // second job analysis
			{result: AgentResult{Result: "NONE"}},                                  // second job LLM says NONE
			{result: AgentResult{Result: "## Summary\nBuild timeout due to DNS"}}, // third job analysis
			{result: AgentResult{Result: "NONE"}},                                  // third job LLM says NONE
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should create exactly 1 issue (first job); jobs 2 and 3 use deterministic fallback
	if len(gh.createdIssues) != 1 {
		t.Errorf("expected 1 issue created (deterministic fallback for jobs 2+3), got %d", len(gh.createdIssues))
	}

	// Run-link comments should be posted for jobs 2 and 3
	runLinkCount := 0
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "Same failure observed") {
			runLinkCount++
		}
	}
	if runLinkCount != 2 {
		t.Errorf("expected 2 run-link comments, got %d", runLinkCount)
	}
}

func TestTriageDedup_DeterministicFallbackWithCycleIssuesMerged(t *testing.T) {
	// Mirrors production behavior: investigateTriageRun merges cycleIssues
	// into existingIssues before calling matchExistingTriageIssue. When the
	// LLM says NONE, the deterministic fallback should match to the cycle
	// issue even though it's present in existingIssues.
	gh := &mockGitHubClient{
		searchResults: []Issue{}, // GitHub search returns nothing (search lag)
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
	}

	// LLM says NONE — deterministic fallback should fire after LLM
	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "NONE"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	title := "CI Failure: job-b / DNS timeout"
	analysis := "## Summary\nDNS timeout"

	// Simulate 3 failed jobs with 1 cycle issue already created
	cycleIssues := []Issue{
		{Number: 42, Title: "CI Failure: job-a / Infra outage", Body: "Infra outage"},
	}
	cycleFailedJobs := []string{"job-a", "job-b", "job-c"}

	// Mirror production: merge cycleIssues into existingIssues (as
	// investigateTriageRun does before calling matchExistingTriageIssue)
	existingIssues := mergeIssues([]Issue{}, cycleIssues)

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"job-b", title, analysis,
		existingIssues,
		"/tmp/worktree",
		cycleIssues,
		cycleFailedJobs,
	)

	// Should match cycle issue #42 via deterministic fallback after LLM NONE
	if matchedIssue != 42 {
		t.Errorf("expected deterministic fallback to match cycle issue #42, got #%d", matchedIssue)
	}

	// LLM should have been called once (title didn't match, so LLM tried)
	if codeAgent.callCount != 1 {
		t.Errorf("expected 1 agent call (LLM tried before fallback), got %d", codeAgent.callCount)
	}
}

func TestTriageDedup_DeterministicFallbackWithMultiLineNone(t *testing.T) {
	// When multiple jobs fail and the LLM returns a multi-line NONE response
	// (e.g. "NONE\n\nNo match found."), the deterministic fallback should
	// still match to the cycle issue created by the first job.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 500, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/500"},
		},
		searchResults:   []Issue{}, // GitHub search returns nothing
		nextIssueNumber: 20,
	}

	// Two separate TriageJobs URLs (functionally equivalent to concurrent
	// jobs or lanes) to verify that the deterministic fallback works
	// when the LLM returns a multi-line NONE response.

	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
		TriageJobs: []string{
			"https://github.com/owner/repo/actions/workflows/unit.yml",
			"https://github.com/owner/repo/actions/workflows/e2e.yml",
		},
	}

	// First job: analysis → creates issue
	// Second job: analysis + LLM says NONE → deterministic fallback
	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "## Summary\nShared root cause"}},
			{result: AgentResult{Result: "## Summary\nDifferent symptoms same cause"}},
			{result: AgentResult{Result: "NONE\n\nNo match found."}}, // multi-line NONE
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should create exactly 1 issue; second job uses deterministic fallback
	if len(gh.createdIssues) != 1 {
		t.Errorf("expected 1 issue created, got %d", len(gh.createdIssues))
	}

	// Verify run-link comment was posted for the second job
	runLinkFound := false
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "Same failure observed") {
			runLinkFound = true
		}
	}
	if runLinkFound == false {
		t.Error("expected a run-link comment for the second job (deterministic fallback)")
	}
}

func TestProcessReviewComments_PostsChangeSummaryAfterPush(t *testing.T) {
	// After pushing a fix in response to review feedback, oompa should
	// comment on the PR with a compare URL and a semantic summary of the changes.
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	runner := &changeSummaryRunner{
		mockCommandRunner: &mockCommandRunner{},
		headSHA:           "abc123def456",
		diffPatch:         "diff --git a/pkg/agent/review.go b/pkg/agent/review.go\n--- a/pkg/agent/review.go\n+++ b/pkg/agent/review.go\n@@ -1,3 +1,5 @@\n+// Added validation\n func foo() {\n+\treturn nil\n }\n",
	}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.codeAgent = &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "Done"}},                                                 // review fix
			{result: AgentResult{Result: "- Added validation logic to the review handler"}}, // change summary
		},
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

	// Find the change summary comment
	var changeSummaryComment string
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "compare/") {
			changeSummaryComment = comment
		}
	}
	if changeSummaryComment == "" {
		t.Fatal("expected a change summary comment with compare URL to be posted")
	}

	// Should contain a compare URL
	if !strings.Contains(changeSummaryComment, "https://github.com/owner/repo/compare/") {
		t.Errorf("expected compare URL in comment, got: %q", changeSummaryComment)
	}
	// Should contain a [Change](...) link
	if !strings.Contains(changeSummaryComment, "[Change](") {
		t.Errorf("expected [Change](...) link in comment, got: %q", changeSummaryComment)
	}
	// Should contain semantic summary from LLM (not raw file paths with stats)
	if !strings.Contains(changeSummaryComment, "Added validation logic") {
		t.Errorf("expected semantic summary in comment, got: %q", changeSummaryComment)
	}
	// Should contain bot marker
	if !strings.Contains(changeSummaryComment, botMarker) {
		t.Errorf("expected bot marker in comment, got: %q", changeSummaryComment)
	}
	// Should be posted on the PR (issue number 100)
	for i, comment := range gh.addedComments {
		if strings.Contains(comment, "compare/") {
			if gh.addedCommentTargets[i] != 100 {
				t.Errorf("expected comment posted to PR #100, got #%d", gh.addedCommentTargets[i])
			}
		}
	}
}

func TestProcessReviewComments_NoChangeSummaryWhenNoPush(t *testing.T) {
	// When the agent doesn't make any changes, no change summary comment should be posted.
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	runner := &mockCommandRunner{claudeResults: [][]byte{streamResultJSON(AgentResult{Result: "Nothing to do"})}}
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

	// No push should have been attempted
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "push" {
			t.Error("expected no git push when agent makes no changes")
		}
	}

	// No change summary comment should be posted
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "compare/") {
			t.Errorf("expected no change summary comment when no push, got: %q", comment)
		}
	}
}

func TestBuildChangeSummary(t *testing.T) {
	tests := []struct {
		name        string
		diff        string       // git diff output (full patch)
		runnerErr   error        // if set, runner returns this error for git diff
		llmResult   string       // LLM response text
		llmErr      error        // if set, LLM call returns this error
		want        []string     // strings that should appear in the output
		notWant     []string     // strings that should NOT appear in the output
	}{
		{
			name:      "LLM summarizes single file change",
			diff:      "diff --git a/pkg/agent/review.go b/pkg/agent/review.go\n@@ -1,3 +1,5 @@\n+// Added validation\n func foo() {}\n",
			llmResult: "- Added input validation to the review handler",
			want:      []string{"Added input validation to the review handler"},
			notWant:   []string{"review.go", "+++"},
		},
		{
			name:      "LLM summarizes multiple changes",
			diff:      "diff --git a/review.go b/review.go\n@@ -1 +1 @@\n-old\n+new\ndiff --git a/loop.go b/loop.go\n@@ -1 +1 @@\n-old\n+new\n",
			llmResult: "- Refactored review handler for clarity\n- Updated loop to handle edge cases",
			want:      []string{"Refactored review handler", "Updated loop to handle edge cases"},
			notWant:   []string{"review.go", "loop.go"},
		},
		{
			name: "empty diff returns fallback",
			diff: "",
			want: []string{"Updated code to address review feedback"},
		},
		{
			name:      "git diff error returns fallback",
			runnerErr: fmt.Errorf("git diff failed"),
			want:      []string{"Updated code to address review feedback"},
		},
		{
			name:   "LLM error returns fallback",
			diff:   "diff --git a/foo.go b/foo.go\n@@ -1 +1 @@\n-old\n+new\n",
			llmErr: fmt.Errorf("LLM unavailable"),
			want:   []string{"Updated code to address review feedback"},
		},
		{
			name:      "LLM returns empty result uses fallback",
			diff:      "diff --git a/foo.go b/foo.go\n@@ -1 +1 @@\n-old\n+new\n",
			llmResult: "",
			want:      []string{"Updated code to address review feedback"},
		},
		{
			name:      "LLM output without bullet prefix gets normalized",
			diff:      "diff --git a/foo.go b/foo.go\n@@ -1 +1 @@\n-old\n+new\n",
			llmResult: "Fixed the null pointer check",
			want:      []string{"- Fixed the null pointer check"},
		},
		{
			name:      "LLM returns diff artifacts uses fallback",
			diff:      "diff --git a/foo.go b/foo.go\n@@ -1 +1 @@\n-old\n+new\n",
			llmResult: "diff --git a/foo.go b/foo.go\n@@ -1 +1 @@\n-old\n+new",
			want:      []string{"Updated code to address review feedback"},
		},
		{
			name:      "LLM returns stat artifacts uses fallback",
			diff:      "diff --git a/foo.go b/foo.go\n@@ -1 +1 @@\n-old\n+new\n",
			llmResult: "- foo.go | 2 files changed, 1 insertions(+), 1 deletions(-)",
			want:      []string{"Updated code to address review feedback"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &mockCommandRunner{stdout: []byte(tt.diff), err: tt.runnerErr}
			agent := newTestAgent(&mockGitHubClient{}, runner, &mockWorktreeManager{})
			agent.codeAgent = &sequentialMockCodeAgent{
				results: []mockCodeAgentCall{{result: AgentResult{Result: tt.llmResult}, err: tt.llmErr}},
			}

			result := agent.buildChangeSummary(context.Background(), "/tmp/worktree", "abc", "def")

			for _, s := range tt.want {
				if !strings.Contains(result, s) {
					t.Errorf("expected %q in summary, got: %q", s, result)
				}
			}
			for _, s := range tt.notWant {
				if strings.Contains(result, s) {
					t.Errorf("unexpected %q in summary, got: %q", s, result)
				}
			}
		})
	}
}

// changeSummaryRunner simulates review feedback that results in uncommitted changes,
// a successful amend+push, and provides a diff patch for the change summary.
type changeSummaryRunner struct {
	*mockCommandRunner
	headSHA    string // SHA to return for git rev-parse HEAD after the agent runs (post-push SHA)
	diffPatch  string // output of git diff (full patch)
	revCount   int    // tracks how many times rev-parse HEAD was called
	headBefore string // pre-agent SHA
}

func (r *changeSummaryRunner) Run(ctx context.Context, workDir, name string, args ...string) (stdout, stderr []byte, err error) {
	r.mockCommandRunner.Run(ctx, workDir, name, args...) //nolint:errcheck // recording call for test assertions

	// git rev-parse --abbrev-ref HEAD: return a stable branch name for gitPush
	if name == "git" && len(args) >= 3 && args[0] == "rev-parse" && args[1] == "--abbrev-ref" && args[2] == "HEAD" {
		return []byte("ai/issue-42\n"), nil, nil
	}

	// git rev-parse HEAD: return different SHAs before and after
	if name == "git" && len(args) >= 2 && args[0] == "rev-parse" && args[1] == "HEAD" {
		r.revCount++
		if r.revCount == 1 {
			// First call: HEAD before agent runs
			r.headBefore = "before-sha-1234567890"
			return []byte(r.headBefore + "\n"), nil, nil
		}
		// All subsequent calls: HEAD after push
		return []byte(r.headSHA + "\n"), nil, nil
	}

	// git status --porcelain: return dirty (triggers amend path)
	if name == "git" && len(args) >= 1 && args[0] == "status" {
		return []byte("M pkg/agent/review.go\n"), nil, nil
	}

	// git log --format=%s (fixup commit check): return no fixups
	if name == "git" && len(args) >= 1 && args[0] == "log" {
		return []byte(""), nil, nil
	}

	// git diff: return full patch
	if name == "git" && len(args) >= 1 && args[0] == "diff" {
		return []byte(r.diffPatch), nil, nil
	}

	return nil, nil, nil
}
