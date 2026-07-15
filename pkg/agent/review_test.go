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
)

func TestProcessReviewComments_NoNewComments(t *testing.T) {
	gh := &mockGitHubClient{}
	runner := &mockCommandRunner{}
	wt := &mockWorktreeManager{}

	agent := newTestAgent(gh, runner, wt)
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
	})

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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
	})

	agent.ProcessReviewComments(context.Background())

	claudeCalls := countCalls(runner.calls, "claude")
	if claudeCalls != 1 {
		t.Fatalf("expected 1 claude call, got %d", claudeCalls)
	}

	if agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].LastCommentID != 60 {
		t.Errorf("expected lastCommentID 60, got %d", agent.state.ActiveIssues[IssueKey("owner", "repo", 42)].LastCommentID)
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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
	})

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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
	})

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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
	})

	agent.ProcessReviewComments(context.Background())

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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
	})

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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
	})

	agent.ProcessReviewComments(context.Background())

	claudeCalls := countCalls(runner.calls, "claude")
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
	trackWork(agent, func(w *IssueWork) {
		w.LastReviewID = 100 // review 200 > 100, so it's new/unaddressed
	})

	agent.ProcessReviewComments(context.Background())

	claudeCalls := countCalls(runner.calls, "claude")
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
	trackWork(agent, func(w *IssueWork) {
		w.LastReviewID = 100 // review 50 <= 100, filtered by sinceID
	})

	agent.ProcessReviewComments(context.Background())

	claudeCalls := countCalls(runner.calls, "claude")
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
	trackWork(agent, func(w *IssueWork) {
		// LastReviewID 250: oompa already processed coderabbit (ID 200) and gemini (ID 250)
		// but copilot's review (ID 300) is still unaddressed
		w.LastReviewID = 250
	})

	agent.ProcessReviewComments(context.Background())

	claudeCalls := countCalls(runner.calls, "claude")
	// copilot's review should be processed — sinceID ensures it's not filtered out
	if claudeCalls != 1 {
		t.Errorf("expected 1 claude call for unaddressed copilot review, got %d", claudeCalls)
	}

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if work.LastReviewID != 300 {
		t.Errorf("expected LastReviewID to advance to 300, got %d", work.LastReviewID)
	}
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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
	})

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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
	})

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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
		w.LastReviewID = 100
	})

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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
		w.ReviewNoOpCount = 3 // already at limit
	})

	agent.ProcessReviewComments(context.Background())

	// Should NOT invoke the agent because no-op limit is reached.
	claudeCalls := countCalls(runner.calls, "claude")
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
	trackWork(agent, func(w *IssueWork) {
		w.LastReviewID = 250  // cursor at 250, review 300 is new
		w.ReviewNoOpCount = 3 // at limit
	})

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
	claudeCalls := countCalls(runner.calls, "claude")
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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
		w.ReviewNoOpCount = 2 // was approaching limit
	})

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
	trackWork(agent, func(w *IssueWork) {
		w.LastReviewID = 100
		w.ReviewNoOpCount = 1
	})

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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
		w.SessionCostUSD = 11.5 // exceeds $10 threshold
	})

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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
		w.SessionCostUSD = 2.0 // existing cost
	})

	agent.ProcessReviewComments(context.Background())

	work := agent.state.ActiveIssues[IssueKey("owner", "repo", 42)]
	expectedCost := 2.75 // 2.0 + 0.75
	if work.SessionCostUSD != expectedCost {
		t.Errorf("expected SessionCostUSD %.2f, got %.2f", expectedCost, work.SessionCostUSD)
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
	trackWork(agent)

	agent.ProcessReviewComments(context.Background())

	// Should have invoked the agent
	claudeCalls := countCalls(runner.calls, "claude")
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
	trackWork(agent)

	agent.ProcessReviewComments(context.Background())

	// Should NOT invoke the agent
	if countCalls(runner.calls, "claude") != 0 {
		t.Error("should not invoke claude for comments without /oompa prefix")
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
	trackWork(agent)

	agent.ProcessReviewComments(context.Background())

	if countCalls(runner.calls, "claude") != 0 {
		t.Error("should not invoke claude for non-whitelisted user's /oompa command")
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
	trackWork(agent)

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
	trackWork(agent)

	agent.ProcessReviewComments(context.Background())

	// Should have invoked the agent once (both types combined into one task)
	claudeCalls := countCalls(runner.calls, "claude")
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
	trackWork(agent)

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
	trackWork(agent)

	agent.ProcessReviewComments(context.Background())

	// Should NOT invoke the agent for bare /oompa with no directive
	if countCalls(runner.calls, "claude") != 0 {
		t.Error("should not invoke claude for bare /oompa comment with no directive")
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
	trackWork(agent)

	agent.ProcessReviewComments(context.Background())

	if countCalls(runner.calls, "claude") != 0 {
		t.Error("should not invoke claude for bot-posted /oompa comment")
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

	agent := newTestAgent(gh, runner, wt,
		withCfg(func(c *Config) {
			c.SignedOffBy = "Test User <test@example.com>"
			c.AssistedBy = "Claude <noreply@anthropic.com>"
		}),
		withCodeAgent(&commitMsgCodeAgent{
			commitMsg: "fix: corrected commit subject\n\nProper body",
			result:    AgentResult{Result: "Done"},
		}),
	)
	trackWork(agent, func(w *IssueWork) {
		w.WorktreePath = worktreeDir
	})

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

	agent := newTestAgent(gh, runner, wt,
		withCfg(func(c *Config) {
			c.SignedOffBy = "Test User <test@example.com>"
			c.AssistedBy = "Claude <noreply@anthropic.com>"
		}),
		withCodeAgent(&commitMsgCodeAgent{
			commitMsg: "fix: updated commit message\n\nNew body",
			result:    AgentResult{Result: "Done"},
		}),
	)
	trackWork(agent, func(w *IssueWork) {
		w.WorktreePath = worktreeDir
	})

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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
	})

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
			{result: AgentResult{Result: "Done"}},                                           // review fix
			{result: AgentResult{Result: "- Added validation logic to the review handler"}}, // change summary
		},
	}
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
	})

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
	trackWork(agent, func(w *IssueWork) {
		w.LastCommentID = 50
	})

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
		name      string
		diff      string   // git diff output (full patch)
		runnerErr error    // if set, runner returns this error for git diff
		llmResult string   // LLM response text
		llmErr    error    // if set, LLM call returns this error
		want      []string // strings that should appear in the output
		notWant   []string // strings that should NOT appear in the output
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

			result := agent.buildChangeSummary(context.Background(), &IssueWork{WorktreePath: "/tmp/worktree"}, "abc", "def")

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
