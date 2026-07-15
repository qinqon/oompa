package agent

import (
	"context"
	"slices"
	"strings"
	"testing"
)

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

func TestProcessNewIssues_SkipsLinkedPR(t *testing.T) {
	gh := &mockGitHubClient{
		issues:   []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
		linkedPR: true, // an open PR (e.g. human-created) already references the issue
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.ProcessNewIssues(context.Background())

	if len(runner.calls) != 0 {
		t.Error("should not invoke claude when a linked PR exists")
	}
	if len(wt.createdBranches) != 0 {
		t.Error("should not create worktree when a linked PR exists")
	}
	if len(gh.addedComments) != 0 {
		t.Errorf("should not comment on issue with linked PR, got %v", gh.addedComments)
	}
	if _, ok := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]; ok {
		t.Error("issue with linked PR should not be tracked")
	}
}

func TestProcessNewIssues_DefersOnListPRsError(t *testing.T) {
	gh := &mockGitHubClient{
		issues:     []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
		listPRsErr: &mockError{msg: "boom"},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.ProcessNewIssues(context.Background())

	if len(runner.calls) != 0 {
		t.Error("should not invoke claude when PR listing fails")
	}
	if len(wt.createdBranches) != 0 {
		t.Error("should not create worktree when PR listing fails")
	}
	if _, ok := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]; ok {
		t.Error("deferred issue should not be tracked so the next poll retries it")
	}
}

func TestProcessNewIssues_DefersOnLinkedPRCheckError(t *testing.T) {
	gh := &mockGitHubClient{
		issues:      []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
		linkedPRErr: &mockError{msg: "boom"},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.ProcessNewIssues(context.Background())

	if len(runner.calls) != 0 {
		t.Error("should not invoke claude when linked PR check fails")
	}
	if len(wt.createdBranches) != 0 {
		t.Error("should not create worktree when linked PR check fails")
	}
	if _, ok := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]; ok {
		t.Error("deferred issue should not be tracked so the next poll retries it")
	}
}

func TestProcessNewIssues_SkipsMergedPR(t *testing.T) {
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
		prs:    []PR{{Number: 100, State: "closed", Merged: true, Head: "ai/issue-42"}},
	}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.ProcessNewIssues(context.Background())

	if len(runner.calls) != 0 {
		t.Error("should not invoke claude when the fix PR was merged")
	}
	if len(wt.createdBranches) != 0 {
		t.Error("should not create worktree when the fix PR was merged")
	}
	if _, ok := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]; ok {
		t.Error("issue with merged PR should not be tracked")
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

// commentOnlyDiffRunner simulates Claude producing commits whose diff contains
// only comment additions. diffW is returned for `git diff -w` (whitespace-ignoring).
type commentOnlyDiffRunner struct {
	*mockCommandRunner
	diff  string
	diffW string
}

func (r *commentOnlyDiffRunner) Run(ctx context.Context, workDir, name string, args ...string) (stdout, stderr []byte, err error) {
	if name == "git" && len(args) >= 1 {
		switch args[0] {
		case "log":
			r.mockCommandRunner.Run(ctx, workDir, name, args...) //nolint:errcheck // recording call for test assertions
			return []byte("abc123 add comments\n"), nil, nil
		case "diff":
			r.mockCommandRunner.Run(ctx, workDir, name, args...) //nolint:errcheck // recording call for test assertions
			if len(args) >= 2 && args[1] == "-w" {
				return []byte(r.diffW), nil, nil
			}
			return []byte(r.diff), nil, nil
		}
	}
	return r.mockCommandRunner.Run(ctx, workDir, name, args...)
}

func TestProcessNewIssues_CommentOnlyDiffMarksFailed(t *testing.T) {
	claudeResult := streamResultJSON(AgentResult{Result: "Added explanatory comments"})
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
	}
	runner := &commentOnlyDiffRunner{
		mockCommandRunner: &mockCommandRunner{stdout: claudeResult},
		diff: `diff --git a/hack/bump.sh b/hack/bump.sh
index 1234567..89abcde 100644
--- a/hack/bump.sh
+++ b/hack/bump.sh
@@ -1,3 +1,6 @@
 set -e
+# NOTE: When using sed '/pattern/a' to append multiple blocks,
+# the LAST sed command's output appears FIRST.
+
 sed -i 's/foo/bar/' file
`,
	}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.ProcessNewIssues(context.Background())

	if len(gh.addedLabels) != 1 || gh.addedLabels[0] != "ai-failed" {
		t.Errorf("expected 'ai-failed' label, got %v", gh.addedLabels)
	}
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work == nil || work.Status != "failed" {
		t.Error("expected issue status to be 'failed'")
	}
	if work != nil && work.PRNumber != 0 {
		t.Errorf("expected no PR, got PR %d", work.PRNumber)
	}
	for _, call := range runner.calls {
		if call.Name == "git" && len(call.Args) > 0 && call.Args[0] == "push" {
			t.Error("expected no git push for comment-only diff")
		}
	}
}

func TestProcessNewIssues_WhitespaceOnlyDiffMarksFailed(t *testing.T) {
	// A diff that only reindents code is non-empty, but empty under
	// `git diff -w`, so it must be treated as a failed fix.
	claudeResult := streamResultJSON(AgentResult{Result: "Reformatted"})
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
	}
	runner := &commentOnlyDiffRunner{
		mockCommandRunner: &mockCommandRunner{stdout: claudeResult},
		diff:              "+++ b/f.go\n-\tx := 1\n+    x := 1\n",
		diffW:             "",
	}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	agent.ProcessNewIssues(context.Background())

	if len(gh.addedLabels) != 1 || gh.addedLabels[0] != "ai-failed" {
		t.Errorf("expected 'ai-failed' label, got %v", gh.addedLabels)
	}
	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work == nil || work.Status != "failed" {
		t.Error("expected issue status to be 'failed'")
	}
}
