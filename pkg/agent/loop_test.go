package agent

import (
	"context"
	"testing"
)

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
	agent.cfg.Reactions = []string{}                           // report-only mode
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
