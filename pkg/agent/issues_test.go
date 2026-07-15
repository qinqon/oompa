package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestProcessNewIssues_SkipsAndDefers covers every reason a labeled issue is
// not picked up: it is already tracked, a PR for its branch or a linked PR
// already exists (open or merged), or a pre-flight GitHub call fails and the
// issue is deferred to the next poll.
func TestProcessNewIssues_SkipsAndDefers(t *testing.T) {
	tests := []struct {
		name     string
		gh       *mockGitHubClient
		preTrack bool // issue 42 already in state before the poll
		// wantStatus is the expected tracked status afterwards; empty means
		// the issue must not be tracked (so the next poll retries it).
		wantStatus string
		wantPR     int
	}{
		{
			name:       "skips issue already tracked",
			gh:         &mockGitHubClient{issues: []Issue{{Number: 42, Title: "Fix bug"}}},
			preTrack:   true,
			wantStatus: StatusPROpen,
			wantPR:     100,
		},
		{
			name: "adopts existing open PR for the branch",
			gh: &mockGitHubClient{
				issues: []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
				prs:    []PR{{Number: 100, State: "open", Head: "ai/issue-42"}},
			},
			wantStatus: StatusPROpen,
			wantPR:     100,
		},
		{
			name: "skips issue with linked PR",
			gh: &mockGitHubClient{
				issues:   []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
				linkedPR: true, // an open PR (e.g. human-created) already references the issue
			},
		},
		{
			name: "defers when PR listing fails",
			gh: &mockGitHubClient{
				issues:     []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
				listPRsErr: &mockError{msg: "boom"},
			},
		},
		{
			name: "defers when linked PR check fails",
			gh: &mockGitHubClient{
				issues:      []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
				linkedPRErr: &mockError{msg: "boom"},
			},
		},
		{
			name: "skips issue whose fix PR was merged",
			gh: &mockGitHubClient{
				issues: []Issue{{Number: 42, Title: "Fix bug", Body: "broken"}},
				prs:    []PR{{Number: 100, State: "closed", Merged: true, Head: "ai/issue-42"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &mockCommandRunner{}
			wt := &mockWorktreeManager{}
			agent := newTestAgent(tt.gh, runner, wt)
			if tt.preTrack {
				trackWork(agent)
			}

			agent.ProcessNewIssues(context.Background())

			if len(runner.calls) != 0 {
				t.Error("should not invoke the code agent")
			}
			if len(wt.createdBranches) != 0 {
				t.Error("should not create a worktree")
			}
			if len(tt.gh.addedComments) != 0 {
				t.Errorf("should not comment on the issue, got %v", tt.gh.addedComments)
			}

			work, tracked := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
			if tt.wantStatus == "" {
				if tracked {
					t.Error("issue should not be tracked so the next poll retries it")
				}
				return
			}
			if !tracked {
				t.Fatal("issue 42 should be tracked")
			}
			if work.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", work.Status, tt.wantStatus)
			}
			if work.PRNumber != tt.wantPR {
				t.Errorf("PRNumber = %d, want %d", work.PRNumber, tt.wantPR)
			}
		})
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
	trackWork(agent, func(w *IssueWork) {
		w.Status = "implementing"
		w.PRNumber = 0
	})

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

// TestProcessNewIssues_SquashCommitMessage covers how the squash commit
// message is built from the issue title: the title becomes the subject
// (truncated at 72 chars), the body references the issue with Related-to
// instead of auto-close keywords, and configured trailers are appended.
func TestProcessNewIssues_SquashCommitMessage(t *testing.T) {
	longTitle := "CI Failure: pull-e2e-cluster-network-addons-operator-monitoring-k8s / The pull-e2e-cluster-network-addons-operator-monitoring-k8s"
	tests := []struct {
		name        string
		issue       Issue
		wantSubject string // empty: checked by the long-title rules instead
		longTitle   bool
	}{
		{
			name:        "conventional commit title used as-is",
			issue:       Issue{Number: 1532, Title: "build: consolidate multi-arch container build scripts", Body: "Consolidate build scripts"},
			wantSubject: "build: consolidate multi-arch container build scripts",
		},
		{
			name:        "non-conventional title used as-is",
			issue:       Issue{Number: 42, Title: "implement feature X", Body: "broken"},
			wantSubject: "implement feature X",
		},
		{
			name:      "long title truncated to 72 chars with ellipsis",
			issue:     Issue{Number: 2799, Title: longTitle, Body: "CI is failing"},
			longTitle: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := &mockGitHubClient{issues: []Issue{tt.issue}}
			runner := &mockCommandRunner{stdout: streamResultJSON(AgentResult{Result: "Fixed it"})}
			agent := newTestAgent(gh, runner, &mockWorktreeManager{})
			agent.cfg.SignedOffBy = "Test User <test@example.com>"

			agent.ProcessNewIssues(context.Background())

			var commitMsg string
			for _, c := range runner.calls {
				if c.Name == "git" && len(c.Args) >= 3 && c.Args[0] == "commit" && c.Args[1] == "-m" {
					commitMsg = c.Args[2]
					break
				}
			}
			if commitMsg == "" {
				t.Fatal("expected git commit -m to be called")
			}

			subject := strings.SplitN(commitMsg, "\n", 2)[0]
			if tt.longTitle {
				// truncateSubject breaks at the last word boundary before
				// 72 runes and appends an ellipsis.
				want := "CI Failure: pull-e2e-cluster-network-addons-operator-monitoring-k8s..."
				if subject != want {
					t.Errorf("subject = %q, want %q", subject, want)
				}
			} else if subject != tt.wantSubject {
				t.Errorf("subject = %q, want %q", subject, tt.wantSubject)
			}

			for _, keyword := range []string{"Fix #", "Fixes #", "Closes #"} {
				if strings.Contains(commitMsg, fmt.Sprintf("%s%d", keyword, tt.issue.Number)) {
					t.Errorf("commit message should not contain auto-close keyword %q: %s", keyword, commitMsg)
				}
			}
			if want := fmt.Sprintf("Related-to: #%d", tt.issue.Number); !strings.Contains(commitMsg, want) {
				t.Errorf("expected commit body to contain %q, got: %s", want, commitMsg)
			}
			if !strings.Contains(commitMsg, "Signed-off-by: Test User <test@example.com>") {
				t.Errorf("expected commit message to contain the configured sign-off, got: %s", commitMsg)
			}
		})
	}
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
