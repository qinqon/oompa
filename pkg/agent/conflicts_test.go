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
	trackWork(agent)

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
	trackWork(agent)

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
	trackWork(agent)

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
	trackWork(agent)

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
	trackWork(agent)

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
	trackWork(agent)

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
	trackWork(agent)

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
	trackWork(agent)

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
	trackWork(agent)

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
	trackWork(agent)

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
	trackWork(agent)

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
	trackWork(agent)

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

// TestProcessRebase_Guards covers the guard matrix in shouldRebaseNow as
// exercised through ProcessRebase: main-branch activity, the minimum rebase
// interval, and fail-open behavior when the commit count is unavailable.
func TestProcessRebase_Guards(t *testing.T) {
	tests := []struct {
		name            string
		recentCommits   int
		countCommitsErr error
		sinceLastRebase time.Duration // zero = never rebased
		wantRebase      bool
	}{
		{name: "defers when main is active (>5 commits in window)", recentCommits: 10, wantRebase: false},
		{name: "proceeds when main is quiet", recentCommits: 2, wantRebase: true},
		{name: "defers when min interval not reached", sinceLastRebase: 1 * time.Hour, wantRebase: false},
		{name: "proceeds when min interval expired", recentCommits: 2, sinceLastRebase: 5 * time.Hour, wantRebase: true},
		{name: "fail-open when commit count unavailable", countCommitsErr: fmt.Errorf("API rate limit exceeded"), wantRebase: true},
		{name: "first rebase skips the interval guard", recentCommits: 3, wantRebase: true},
		{name: "exact activity threshold still rebases (only >threshold defers)", recentCommits: 5, wantRebase: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := &mockGitHubClient{
				mergeableState:  "behind",
				recentCommits:   tt.recentCommits,
				countCommitsErr: tt.countCommitsErr,
			}
			runner := &mockCommandRunner{}
			agent := newTestAgent(gh, runner, &mockWorktreeManager{})
			trackWork(agent, func(w *IssueWork) {
				if tt.sinceLastRebase != 0 {
					w.LastRebaseTime = time.Now().Add(-tt.sinceLastRebase)
				}
			})

			agent.ProcessRebase(context.Background())

			rebased := false
			for _, c := range runner.calls {
				if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rebase" {
					rebased = true
				}
			}
			if rebased != tt.wantRebase {
				t.Errorf("rebase attempted = %v, want %v", rebased, tt.wantRebase)
			}
			if !tt.wantRebase && countCalls(runner.calls, "git") != 0 {
				t.Errorf("deferred rebase should run no git commands, got %d", countCalls(runner.calls, "git"))
			}
		})
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
	trackWork(agent)

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
	trackWork(agent)

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

// TestShouldRebaseNow_Interval covers the configurable minimum-interval
// logic directly: a custom interval, its expiry, and the 4h default.
func TestShouldRebaseNow_Interval(t *testing.T) {
	tests := []struct {
		name       string
		interval   time.Duration // zero = 4h default
		sinceLast  time.Duration
		wantAllow  bool
		wantReason string
	}{
		{"24h interval not reached", 24 * time.Hour, 20 * time.Hour, false, "minimum interval not reached"},
		{"24h interval expired", 24 * time.Hour, 25 * time.Hour, true, ""},
		{"zero interval falls back to 4h default", 0, 3 * time.Hour, false, "minimum interval not reached"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := &mockGitHubClient{recentCommits: 0} // quiet main
			agent := newTestAgent(gh, &mockCommandRunner{}, &mockWorktreeManager{})
			agent.cfg.RebaseInterval = tt.interval

			work := &IssueWork{
				PRNumber:       100,
				LastRebaseTime: time.Now().Add(-tt.sinceLast),
			}

			allowed, reason := agent.shouldRebaseNow(context.Background(), work)
			if allowed != tt.wantAllow {
				t.Errorf("allowed = %v, want %v (reason %q)", allowed, tt.wantAllow, reason)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}
