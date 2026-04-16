package agent

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"
)

// mockGitHubClient implements GitHubClient for testing.
type mockGitHubClient struct {
	issues        []Issue
	prComments    []ReviewComment
	issueComments []ReviewComment
	prState       string
	prs           []PR
	addedComments  []string
	addedLabels    []string
	removedLabels  []string
	addedReactions []string
	checkRuns      []CheckRun
	prHeadSHAs     []string // returns these in sequence; if empty returns "abc123"
	prsAfterNCalls int      // only return PRs after this many ListPRsByHead calls
	prsCallCount   int
	mergeableState string   // mergeable state to return from GetPRMergeable (default: "clean")
	prBehind       bool     // whether IsPRBehind returns true

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

func (m *mockGitHubClient) GetCheckRunLog(_ context.Context, _, _ string, _ int64) (string, error) {
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

func newTestAgent(gh *mockGitHubClient, runner *mockCommandRunner, wt *mockWorktreeManager) *Agent {
	return &Agent{
		gh:        gh,
		runner:    runner,
		worktrees: wt,
		state:     NewState(),
		cfg:       Config{Owner: "owner", Repo: "repo", Label: "good-for-ai"},
		logger:    slog.Default(),
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
	claudeResult := streamResultJSON(ClaudeResult{Result: "Fixed it"})
	gh := &mockGitHubClient{
		issues:         []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
		prs:            []PR{{Number: 100, State: "open", Head: "ai/issue-42"}},
		prsAfterNCalls: 1, // skip first call (early check), return PR on second (after Claude)
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
	if len(gh.addedComments) != 2 {
		t.Errorf("expected 2 comments (in-progress + failure), got %d", len(gh.addedComments))
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
	claudeResult := streamResultJSON(ClaudeResult{Result: "Addressed"})
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "reviewer", Body: "Please fix this", Path: "main.go", Line: 10},
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
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
	if claudeCalls != 1 {
		t.Fatalf("expected 1 claude call, got %d", claudeCalls)
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
	claudeResult := streamResultJSON(ClaudeResult{Result: "Done"})
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 60, User: "anyone", Body: "fix this"},
		},
	}
	runner := &mockCommandRunner{stdout: claudeResult}
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
	if claudeCalls != 1 {
		t.Errorf("expected 1 claude call with empty whitelist, got %d", claudeCalls)
	}
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
	claudeResult := streamResultJSON(ClaudeResult{Result: "RELATED Fixed CI"})
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
	if len(gh.addedComments) != 1 {
		t.Error("expected comment about max retries")
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

func TestProcessConflicts_RebasesWhenBehind(t *testing.T) {
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

func TestProcessConflicts_RebasesWhenUnstableButBehind(t *testing.T) {
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

	agent.ProcessConflicts(context.Background())

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
