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

	// No human-visible comment should be posted. The success comment always
	// carries the bot marker (which contains "<!--"), so filtering on the
	// marker would let a suppression regression slip through — assert that
	// nothing was posted at all, like the rebase skip-comment test above.
	if len(gh.addedComments) != 0 {
		t.Errorf("expected 0 comments (conflict comment suppressed), got %d: %v", len(gh.addedComments), gh.addedComments)
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
