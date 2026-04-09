package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeGitHub is a stateful fake GitHub API server for integration tests.
type fakeGitHub struct {
	mu             sync.Mutex
	issues         []Issue
	prs            map[int]*PR           // prNumber -> PR
	prComments     map[int][]ReviewComment // prNumber -> comments
	postedComments []string               // issue comments posted
	addedLabels    []string
	removedLabels  []string
	reactions      []string
	checkRuns      []CheckRun
	nextPRNumber   int
	nextCommentID  int64
	shaCounter     int
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{
		prs:           make(map[int]*PR),
		prComments:    make(map[int][]ReviewComment),
		nextPRNumber:  100,
		nextCommentID: 1000,
	}
}

func (f *fakeGitHub) addIssue(issue Issue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issues = append(f.issues, issue)
}

func (f *fakeGitHub) addPR(branch string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	num := f.nextPRNumber
	f.nextPRNumber++
	f.prs[num] = &PR{Number: num, State: "open", Head: branch}
	return num
}

func (f *fakeGitHub) addReviewComment(prNumber int, user, body, path string, line int) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextCommentID
	f.nextCommentID++
	f.prComments[prNumber] = append(f.prComments[prNumber], ReviewComment{
		ID: id, User: user, Body: body, Path: path, Line: line,
	})
	return id
}

func (f *fakeGitHub) closePR(prNumber int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr, ok := f.prs[prNumber]; ok {
		pr.State = "closed"
	}
}

func (f *fakeGitHub) setCheckRuns(runs []CheckRun) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkRuns = runs
}

func (f *fakeGitHub) mergePR(prNumber int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr, ok := f.prs[prNumber]; ok {
		pr.State = "closed"
		pr.Merged = true
	}
}

// fakeGitHubClient implements GitHubClient backed by fakeGitHub state.
type fakeGitHubClient struct {
	state *fakeGitHub
}

func (f *fakeGitHubClient) ListLabeledIssues(_ context.Context, _, _, _ string) ([]Issue, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	return append([]Issue{}, f.state.issues...), nil
}

func (f *fakeGitHubClient) GetPRReviewComments(_ context.Context, _, _ string, prNumber int, sinceID int64) ([]ReviewComment, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	var result []ReviewComment
	for _, c := range f.state.prComments[prNumber] {
		if c.ID > sinceID {
			result = append(result, c)
		}
	}
	return result, nil
}

func (f *fakeGitHubClient) GetIssueComments(_ context.Context, _, _ string, _ int, _ int64) ([]ReviewComment, error) {
	return nil, nil
}

func (f *fakeGitHubClient) GetPRState(_ context.Context, _, _ string, prNumber int) (string, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	pr, ok := f.state.prs[prNumber]
	if !ok {
		return "", fmt.Errorf("PR %d not found", prNumber)
	}
	if pr.Merged {
		return "merged", nil
	}
	return pr.State, nil
}

func (f *fakeGitHubClient) AddIssueComment(_ context.Context, _, _ string, _ int, body string) error {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	f.state.postedComments = append(f.state.postedComments, body)
	return nil
}

func (f *fakeGitHubClient) AddLabel(_ context.Context, _, _ string, _ int, label string) error {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	f.state.addedLabels = append(f.state.addedLabels, label)
	return nil
}

func (f *fakeGitHubClient) RemoveLabel(_ context.Context, _, _ string, _ int, label string) error {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	f.state.removedLabels = append(f.state.removedLabels, label)
	return nil
}

func (f *fakeGitHubClient) ListPRsByHead(_ context.Context, _, _, _, branch string) ([]PR, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	var result []PR
	for _, pr := range f.state.prs {
		if pr.Head == branch && pr.State == "open" {
			result = append(result, *pr)
		}
	}
	return result, nil
}

func (f *fakeGitHubClient) AddPRCommentReaction(_ context.Context, _, _ string, commentID int64, reaction string) error {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	f.state.reactions = append(f.state.reactions, fmt.Sprintf("%d:%s", commentID, reaction))
	return nil
}

func (f *fakeGitHubClient) GetCheckRuns(_ context.Context, _, _, ref string) ([]CheckRun, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	return append([]CheckRun{}, f.state.checkRuns...), nil
}

func (f *fakeGitHubClient) GetCheckRunLog(_ context.Context, _, _ string, _ int64) (string, error) {
	return "", nil
}

func (f *fakeGitHubClient) GetPRHeadSHA(_ context.Context, _, _ string, _ int) (string, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	f.state.shaCounter++
	return fmt.Sprintf("fakesha%d", f.state.shaCounter), nil
}

func (f *fakeGitHubClient) HasPRCommentReaction(_ context.Context, _, _ string, _ int64, _, _ string) (bool, error) {
	return false, nil
}

func (f *fakeGitHubClient) ReplyToPRComment(_ context.Context, _, _ string, _ int, commentID int64, body string) error {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	f.state.postedComments = append(f.state.postedComments, fmt.Sprintf("reply:%d:%s", commentID, body))
	return nil
}

func (f *fakeGitHubClient) AssignIssue(_ context.Context, _, _ string, _ int, _ string) error {
	return nil
}

func (f *fakeGitHubClient) UnassignIssue(_ context.Context, _, _ string, _ int, _ string) error {
	return nil
}

// initBareRepo creates a bare repo and a working clone for the agent to use.
// Returns (cloneDir, cleanup).
func initBareRepo(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	bareDir := filepath.Join(base, "repo.git")
	cloneDir := filepath.Join(base, "clone")

	// Create bare repo with main as default branch
	run(t, "", "git", "init", "--bare", "--initial-branch=main", bareDir)

	// Clone it, add an initial commit so origin/main exists
	run(t, "", "git", "clone", bareDir, cloneDir)
	run(t, cloneDir, "git", "config", "user.email", "test@test.com")
	run(t, cloneDir, "git", "config", "user.name", "Test")
	run(t, cloneDir, "git", "checkout", "-b", "main")

	// Create initial commit
	readme := filepath.Join(cloneDir, "README.md")
	os.WriteFile(readme, []byte("# test repo\n"), 0o644)
	run(t, cloneDir, "git", "add", ".")
	run(t, cloneDir, "git", "commit", "-m", "initial commit")
	run(t, cloneDir, "git", "push", "origin", "main")

	return cloneDir
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

// fakeClaudeRunner records calls and returns canned results.
// It also creates a fake commit in the worktree to simulate Claude's work.
type fakeClaudeRunner struct {
	mu          sync.Mutex
	calls       []commandCall
	err         error
	onClaudeRun func() // called when claude is invoked, before returning
}

func (f *fakeClaudeRunner) Run(_ context.Context, workDir string, name string, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, commandCall{WorkDir: workDir, Name: name, Args: args})
	err := f.err
	f.mu.Unlock()

	// If this is a claude invocation, simulate work by creating a commit
	if name == "claude" {
		// Create a file and commit it
		filePath := filepath.Join(workDir, "fix.go")
		os.WriteFile(filePath, []byte("package main\n// fix\n"), 0o644)

		exec.Command("git", "-C", workDir, "add", ".").Run()
		cmd := exec.Command("git", "-C", workDir, "commit", "-m", "implement fix")
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Claude",
			"GIT_AUTHOR_EMAIL=claude@test.com",
			"GIT_COMMITTER_NAME=Claude",
			"GIT_COMMITTER_EMAIL=claude@test.com",
		)
		cmd.Run()
		exec.Command("git", "-C", workDir, "push", "origin", "HEAD").Run()

		if f.onClaudeRun != nil {
			f.onClaudeRun()
		}

		if err != nil {
			return nil, []byte("claude error"), err
		}

		result := streamResultJSON(ClaudeResult{Result: "RELATED Fixed it"})
		return result, nil, nil
	}

	// For non-claude commands (git), actually execute them
	cmd := exec.Command(name, args...)
	cmd.Dir = workDir
	stdout, runErr := cmd.Output()
	var stderr []byte
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		stderr = exitErr.Stderr
	}
	return stdout, stderr, runErr
}

func TestIntegration_FullIssueLifecycle(t *testing.T) {
	ctx := context.Background()
	cloneDir := initBareRepo(t)
	gh := newFakeGitHub()
	ghClient := &fakeGitHubClient{state: gh}
	runner := &fakeClaudeRunner{}

	// Use real git worktree manager with the real clone
	bareDir := filepath.Join(filepath.Dir(cloneDir), "repo.git")
	wtManager := NewGitWorktreeManager(&ExecRunner{}, cloneDir, bareDir)

	agent := NewAgent(
		ghClient,
		runner,
		wtManager,
		NewState(),
		Config{Owner: "owner", Repo: "repo", Label: "good-for-ai"},
		slog.Default(),
	)

	// === Phase 1: New issue appears, Claude implements it ===
	gh.addIssue(Issue{Number: 42, Title: "Fix the bug", Body: "It's broken"})

	// PR is created during Claude's run (simulating gh pr create)
	var prNum int
	runner.onClaudeRun = func() {
		prNum = gh.addPR("ai/issue-42")
	}

	agent.CleanupDone(ctx)
	agent.ProcessNewIssues(ctx)
	runner.onClaudeRun = nil // clear hook so subsequent Claude calls don't create more PRs

	// Verify state
	work, ok := agent.state.ActiveIssues[42]
	if !ok {
		t.Fatal("issue 42 should be in state after processing")
	}
	if work.PRNumber != prNum {
		t.Errorf("expected PR %d, got %d", prNum, work.PRNumber)
	}
	if work.Status != "pr-open" {
		t.Errorf("expected status 'pr-open', got %q", work.Status)
	}

	// Verify worktree was created (real git)
	if _, err := os.Stat(work.WorktreePath); os.IsNotExist(err) {
		t.Error("worktree directory should exist")
	}

	// Verify Claude was invoked
	var claudeCalls int
	runner.mu.Lock()
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	runner.mu.Unlock()
	if claudeCalls != 1 {
		t.Errorf("expected 1 claude call, got %d", claudeCalls)
	}

	// === Phase 2: Review comment arrives, Claude addresses it ===
	commentID := gh.addReviewComment(prNum, "reviewer", "Please add tests", "fix.go", 2)

	agent.ProcessReviewComments(ctx)

	// Verify eyes reaction was added
	gh.mu.Lock()
	hasReaction := false
	for _, r := range gh.reactions {
		if r == fmt.Sprintf("%d:eyes", commentID) {
			hasReaction = true
		}
	}
	gh.mu.Unlock()
	if !hasReaction {
		t.Error("expected eyes reaction on review comment")
	}

	// Verify lastCommentID was updated
	if agent.state.ActiveIssues[42].LastCommentID != commentID {
		t.Errorf("expected lastCommentID %d, got %d", commentID, agent.state.ActiveIssues[42].LastCommentID)
	}

	// Verify Claude was invoked again
	runner.mu.Lock()
	claudeCalls = 0
	for _, c := range runner.calls {
		if c.Name == "claude" {
			claudeCalls++
		}
	}
	runner.mu.Unlock()
	if claudeCalls != 2 {
		t.Errorf("expected 2 total claude calls, got %d", claudeCalls)
	}

	// === Phase 3: No new comments, nothing happens ===
	runner.mu.Lock()
	callsBefore := len(runner.calls)
	runner.mu.Unlock()

	agent.ProcessReviewComments(ctx)

	runner.mu.Lock()
	callsAfter := len(runner.calls)
	runner.mu.Unlock()
	if callsAfter != callsBefore {
		t.Error("should not invoke claude when no new comments")
	}

	// === Phase 4: PR merged, cleanup ===
	worktreePath := agent.state.ActiveIssues[42].WorktreePath
	gh.mergePR(prNum)

	agent.CleanupDone(ctx)

	if _, exists := agent.state.ActiveIssues[42]; exists {
		t.Error("issue 42 should be removed from state after merge")
	}

	// Verify worktree was removed (real git)
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Error("worktree directory should be removed after cleanup")
	}
}

func TestIntegration_ClaudeFailure(t *testing.T) {
	ctx := context.Background()
	cloneDir := initBareRepo(t)
	gh := newFakeGitHub()
	ghClient := &fakeGitHubClient{state: gh}
	runner := &fakeClaudeRunner{err: fmt.Errorf("claude crashed")}

	bareDir := filepath.Join(filepath.Dir(cloneDir), "repo.git")
	wtManager := NewGitWorktreeManager(&ExecRunner{}, cloneDir, bareDir)

	agent := NewAgent(
		ghClient,
		runner,
		wtManager,
		NewState(),
		Config{Owner: "owner", Repo: "repo", Label: "good-for-ai"},
		slog.Default(),
	)

	gh.addIssue(Issue{Number: 99, Title: "Hard bug", Body: "Very broken"})

	agent.ProcessNewIssues(ctx)

	// Verify failure state
	work := agent.state.ActiveIssues[99]
	if work == nil {
		t.Fatal("issue 99 should be in state")
	}
	if work.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", work.Status)
	}

	// Verify ai-failed label was added
	gh.mu.Lock()
	hasLabel := false
	for _, l := range gh.addedLabels {
		if l == "ai-failed" {
			hasLabel = true
		}
	}
	gh.mu.Unlock()
	if !hasLabel {
		t.Error("expected 'ai-failed' label on failure")
	}

	// Verify error comment was posted
	gh.mu.Lock()
	hasComment := len(gh.postedComments) > 0
	gh.mu.Unlock()
	if !hasComment {
		t.Error("expected error comment on issue")
	}
}

func TestIntegration_ClosedPRRetriggers(t *testing.T) {
	ctx := context.Background()
	cloneDir := initBareRepo(t)
	gh := newFakeGitHub()
	ghClient := &fakeGitHubClient{state: gh}
	runner := &fakeClaudeRunner{}

	bareDir := filepath.Join(filepath.Dir(cloneDir), "repo.git")
	wtManager := NewGitWorktreeManager(&ExecRunner{}, cloneDir, bareDir)

	agent := NewAgent(
		ghClient,
		runner,
		wtManager,
		NewState(),
		Config{Owner: "owner", Repo: "repo", Label: "good-for-ai"},
		slog.Default(),
	)

	// First run: issue processed, PR created during Claude run
	gh.addIssue(Issue{Number: 10, Title: "Feature", Body: "Add feature"})
	var prNum int
	runner.onClaudeRun = func() {
		prNum = gh.addPR("ai/issue-10")
	}

	agent.CleanupDone(ctx)
	agent.ProcessNewIssues(ctx)
	runner.onClaudeRun = nil

	if agent.state.ActiveIssues[10].PRNumber != prNum {
		t.Fatal("PR should be tracked")
	}

	// PR gets closed (rejected)
	gh.closePR(prNum)

	// Next cycle: cleanup removes it, then processNewIssues picks it up again
	var prNum2 int
	runner.onClaudeRun = func() {
		prNum2 = gh.addPR("ai/issue-10")
	}

	agent.CleanupDone(ctx)

	if _, exists := agent.state.ActiveIssues[10]; exists {
		t.Error("issue should be removed after PR closed")
	}

	agent.ProcessNewIssues(ctx)

	work := agent.state.ActiveIssues[10]
	if work == nil {
		t.Fatal("issue 10 should be re-processed after closed PR")
	}
	if work.PRNumber != prNum2 {
		t.Errorf("expected new PR %d, got %d", prNum2, work.PRNumber)
	}
}

func TestIntegration_ReviewerWhitelist(t *testing.T) {
	ctx := context.Background()
	cloneDir := initBareRepo(t)
	gh := newFakeGitHub()
	ghClient := &fakeGitHubClient{state: gh}
	runner := &fakeClaudeRunner{}

	bareDir := filepath.Join(filepath.Dir(cloneDir), "repo.git")
	wtManager := NewGitWorktreeManager(&ExecRunner{}, cloneDir, bareDir)

	agent := NewAgent(
		ghClient,
		runner,
		wtManager,
		NewState(),
		Config{Owner: "owner", Repo: "repo", Label: "good-for-ai", Reviewers: []string{"trusted-user"}},
		slog.Default(),
	)

	// Setup: issue with open PR (created during Claude run)
	gh.addIssue(Issue{Number: 50, Title: "Fix", Body: "broken"})
	var prNum int
	runner.onClaudeRun = func() {
		prNum = gh.addPR("ai/issue-50")
	}
	agent.ProcessNewIssues(ctx)
	runner.onClaudeRun = nil

	// Non-whitelisted user comments — should be ignored
	gh.addReviewComment(prNum, "random-bot", "do something", "fix.go", 1)

	runner.mu.Lock()
	callsBefore := len(runner.calls)
	runner.mu.Unlock()

	agent.ProcessReviewComments(ctx)

	runner.mu.Lock()
	claudeCallsAfter := 0
	for i := callsBefore; i < len(runner.calls); i++ {
		if runner.calls[i].Name == "claude" {
			claudeCallsAfter++
		}
	}
	runner.mu.Unlock()

	if claudeCallsAfter != 0 {
		t.Error("should not invoke claude for non-whitelisted reviewer")
	}

	// Whitelisted user comments — should be processed
	gh.addReviewComment(prNum, "trusted-user", "please add tests", "fix.go", 2)

	agent.ProcessReviewComments(ctx)

	runner.mu.Lock()
	claudeCallsAfter = 0
	for i := callsBefore; i < len(runner.calls); i++ {
		if runner.calls[i].Name == "claude" {
			claudeCallsAfter++
		}
	}
	runner.mu.Unlock()

	if claudeCallsAfter != 1 {
		t.Errorf("expected 1 claude call for whitelisted reviewer, got %d", claudeCallsAfter)
	}
}

func TestIntegration_CIFailureFixAndRetryLimit(t *testing.T) {
	ctx := context.Background()
	cloneDir := initBareRepo(t)
	gh := newFakeGitHub()
	ghClient := &fakeGitHubClient{state: gh}
	runner := &fakeClaudeRunner{}

	bareDir := filepath.Join(filepath.Dir(cloneDir), "repo.git")
	wtManager := NewGitWorktreeManager(&ExecRunner{}, cloneDir, bareDir)

	agent := NewAgent(
		ghClient,
		runner,
		wtManager,
		NewState(),
		Config{Owner: "owner", Repo: "repo", Label: "good-for-ai"},
		slog.Default(),
	)

	// Setup: issue with open PR (created during Claude run)
	gh.addIssue(Issue{Number: 77, Title: "Add feature", Body: "new feature"})
	runner.onClaudeRun = func() {
		gh.addPR("ai/issue-77")
	}

	agent.CleanupDone(ctx)
	agent.ProcessNewIssues(ctx)
	runner.onClaudeRun = nil

	work := agent.state.ActiveIssues[77]
	if work == nil {
		t.Fatal("issue 77 should be in state")
	}

	// CI fails
	gh.setCheckRuns( []CheckRun{
		{ID: 1, Name: "test", Status: "completed", Conclusion: "failure", Output: "test failed: expected 1 got 2"},
	})

	// First fix attempt
	agent.ProcessCIFailures(ctx)
	if agent.state.ActiveIssues[77].CIFixAttempts != 1 {
		t.Errorf("expected 1 CI fix attempt, got %d", agent.state.ActiveIssues[77].CIFixAttempts)
	}

	// CI still fails — second attempt
	agent.ProcessCIFailures(ctx)
	if agent.state.ActiveIssues[77].CIFixAttempts != 2 {
		t.Errorf("expected 2 CI fix attempts, got %d", agent.state.ActiveIssues[77].CIFixAttempts)
	}

	// CI still fails — third attempt
	agent.ProcessCIFailures(ctx)
	if agent.state.ActiveIssues[77].CIFixAttempts != 3 {
		t.Errorf("expected 3 CI fix attempts, got %d", agent.state.ActiveIssues[77].CIFixAttempts)
	}

	// Fourth attempt — should be blocked, comment posted
	runner.mu.Lock()
	callsBefore := len(runner.calls)
	runner.mu.Unlock()

	agent.ProcessCIFailures(ctx)

	runner.mu.Lock()
	newCalls := 0
	for i := callsBefore; i < len(runner.calls); i++ {
		if runner.calls[i].Name == "claude" {
			newCalls++
		}
	}
	runner.mu.Unlock()

	if newCalls != 0 {
		t.Error("should not invoke claude after max retries")
	}

	gh.mu.Lock()
	hasComment := false
	for _, c := range gh.postedComments {
		if strings.Contains(c, "3 fix attempts") {
			hasComment = true
		}
	}
	gh.mu.Unlock()

	if !hasComment {
		t.Error("expected comment about exhausted CI fix attempts")
	}

	// CI passes — verify no further action is taken
	gh.setCheckRuns( []CheckRun{
		{ID: 2, Name: "test", Status: "completed", Conclusion: "success"},
	})
	// Reset attempts to simulate a fresh state after human fix
	agent.state.ActiveIssues[77].CIFixAttempts = 0
	agent.state.ActiveIssues[77].LastCIStatus = ""

	runner.mu.Lock()
	callsBefore = len(runner.calls)
	runner.mu.Unlock()

	agent.ProcessCIFailures(ctx)

	runner.mu.Lock()
	newCalls = 0
	for i := callsBefore; i < len(runner.calls); i++ {
		if runner.calls[i].Name == "claude" {
			newCalls++
		}
	}
	runner.mu.Unlock()

	if newCalls != 0 {
		t.Error("should not invoke claude when CI passes")
	}
}

func TestIntegration_SyncWorktreePullsManualCommits(t *testing.T) {
	ctx := context.Background()
	cloneDir := initBareRepo(t)
	bareDir := filepath.Join(filepath.Dir(cloneDir), "repo.git")

	gh := newFakeGitHub()
	ghClient := &fakeGitHubClient{state: gh}
	runner := &fakeClaudeRunner{}

	wtManager := NewGitWorktreeManager(&ExecRunner{}, cloneDir, bareDir)

	agent := NewAgent(
		ghClient,
		runner,
		wtManager,
		NewState(),
		Config{Owner: "owner", Repo: "repo", Label: "good-for-ai"},
		slog.Default(),
	)

	// Create issue and PR (created during Claude run)
	gh.addIssue(Issue{Number: 88, Title: "Sync test", Body: "test sync"})
	runner.onClaudeRun = func() {
		gh.addPR("ai/issue-88")
	}

	agent.CleanupDone(ctx)
	agent.ProcessNewIssues(ctx)
	runner.onClaudeRun = nil

	work := agent.state.ActiveIssues[88]
	if work == nil {
		t.Fatal("issue 88 should be in state")
	}

	// Simulate a manual commit pushed to the branch by someone else
	// (push directly to the bare repo from a separate clone)
	manualClone := filepath.Join(t.TempDir(), "manual")
	run(t, "", "git", "clone", bareDir, manualClone)
	run(t, manualClone, "git", "config", "user.email", "human@test.com")
	run(t, manualClone, "git", "config", "user.name", "Human")
	run(t, manualClone, "git", "checkout", work.BranchName)
	manualFile := filepath.Join(manualClone, "manual.txt")
	os.WriteFile(manualFile, []byte("manual change\n"), 0o644)
	run(t, manualClone, "git", "add", ".")
	run(t, manualClone, "git", "commit", "-m", "manual fix by human")
	run(t, manualClone, "git", "push", "origin", work.BranchName)

	// Verify the file doesn't exist in the worktree yet
	if _, err := os.Stat(filepath.Join(work.WorktreePath, "manual.txt")); err == nil {
		t.Fatal("manual.txt should not exist in worktree before sync")
	}

	// Sync the worktree
	err := wtManager.SyncWorktree(ctx, work.WorktreePath)
	if err != nil {
		t.Fatalf("SyncWorktree failed: %v", err)
	}

	// Verify the manual commit is now in the worktree
	if _, err := os.Stat(filepath.Join(work.WorktreePath, "manual.txt")); os.IsNotExist(err) {
		t.Error("manual.txt should exist in worktree after sync")
	}
}

