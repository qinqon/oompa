package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

// mockGitHubClient implements GitHubClient for testing.
type mockGitHubClient struct {
	issues        []Issue
	prComments    []ReviewComment
	issueComments []ReviewComment
	prState       string
	prs           []PR
	addedComments []string
	addedLabels   []string
	removedLabels []string

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

func (m *mockGitHubClient) ListPRsByHead(_ context.Context, _, _, _ string) ([]PR, error) {
	return m.prs, nil
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

func (m *mockWorktreeManager) RemoveWorktree(_ context.Context, worktreePath string) error {
	m.removedPaths = append(m.removedPaths, worktreePath)
	return nil
}

func newTestAgent(gh *mockGitHubClient, runner *mockCommandRunner, wt *mockWorktreeManager) *Agent {
	return &Agent{
		gh:        gh,
		runner:    runner,
		worktrees: wt,
		state: &State{
			ActiveIssues: make(map[int]*IssueWork),
			path:         "/dev/null",
		},
		cfg:    Config{Owner: "owner", Repo: "repo", Label: "good-for-ai"},
		logger: slog.Default(),
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
	claudeResult, _ := json.Marshal(ClaudeResult{Result: "Fixed it"})
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
		prs:    []PR{{Number: 100, State: "open", Head: "ai/issue-42"}},
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
		t.Error("expected error comment on issue")
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
	claudeResult, _ := json.Marshal(ClaudeResult{Result: "Addressed"})
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

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 claude call, got %d", len(runner.calls))
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
	claudeResult, _ := json.Marshal(ClaudeResult{Result: "Done"})
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

	if len(runner.calls) != 1 {
		t.Errorf("expected 1 claude call with empty whitelist, got %d", len(runner.calls))
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
