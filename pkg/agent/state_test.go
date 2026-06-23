package agent

import (
	"context"
	"log/slog"
	"testing"
)

func TestNewState(t *testing.T) {
	s := NewState()
	if s == nil {
		t.Fatal("expected non-nil state")
	}
	if len(s.ActiveIssues) != 0 {
		t.Errorf("expected empty ActiveIssues, got %d", len(s.ActiveIssues))
	}
}

func TestBuildStateFromGitHub_NoIssues(t *testing.T) {
	gh := &mockGitHubClient{}
	cfg := Config{Owner: "owner", Repo: "repo", Label: "good-for-ai"}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/clone", slog.Default())

	if len(state.ActiveIssues) != 0 {
		t.Errorf("expected empty state, got %d issues", len(state.ActiveIssues))
	}
}

func TestBuildStateFromGitHub_RecoversPRState(t *testing.T) {
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 42, Title: "Fix bug", Labels: []string{"good-for-ai"}}},
		prs:    []PR{{Number: 100, State: "open", Head: "ai/issue-42"}},
		prComments: []ReviewComment{
			{ID: 10, User: "alice", Body: "looks good"},
			// Bot reply to alice's comment — proves alice's comment was processed
			{ID: 15, User: "oompa-bot", Body: "fixed", InReplyToID: 10},
			{ID: 20, User: "bob", Body: "fix this"},
			// Bot reply to bob's comment
			{ID: 25, User: "oompa-bot", Body: "done", InReplyToID: 20},
		},
		prReviews: []PRReview{
			{ID: 5, User: "carol", State: "CHANGES_REQUESTED", Body: "needs work"},
			{ID: 15, User: "oompa-bot", State: "COMMENTED", Body: "addressed <!-- oompa-bot -->"},
		},
		issueComments: []ReviewComment{
			{ID: 30, User: "oompa-bot", Body: "Working on it <!-- oompa-bot -->"},
		},
	}
	cfg := Config{Owner: "owner", Repo: "repo", Label: "good-for-ai", GitHubUser: "oompa-bot"}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/clone", slog.Default())

	work, ok := state.ActiveIssues[IssueKey("owner", "repo", 42)]
	if !ok {
		t.Fatal("expected issue 42 in state")
	}
	if work.PRNumber != 100 {
		t.Errorf("expected PR 100, got %d", work.PRNumber)
	}
	if work.Status != "pr-open" {
		t.Errorf("expected status 'pr-open', got %q", work.Status)
	}
	// Comment cursors should recover to max ID among processed comments only.
	// Bot replies (ID 25) are the highest processed review comment.
	if work.LastCommentID != 25 {
		t.Errorf("expected LastCommentID 25, got %d", work.LastCommentID)
	}
	// Bot review (ID 15) is the highest processed review.
	if work.LastReviewID != 15 {
		t.Errorf("expected LastReviewID 15, got %d", work.LastReviewID)
	}
	// Bot issue comment (ID 30) is the highest processed issue comment.
	if work.LastIssueCommentID != 30 {
		t.Errorf("expected LastIssueCommentID 30, got %d", work.LastIssueCommentID)
	}
}

func TestBuildStateFromGitHub_RecoversFailedState(t *testing.T) {
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 99, Title: "Hard bug", Labels: []string{"good-for-ai", "ai-failed"}}},
	}
	cfg := Config{Owner: "owner", Repo: "repo", Label: "good-for-ai"}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/clone", slog.Default())

	work, ok := state.ActiveIssues[IssueKey("owner", "repo", 99)]
	if !ok {
		t.Fatal("expected issue 99 in state")
	}
	if work.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", work.Status)
	}
}

func TestBuildStateFromGitHub_SkipsNewIssues(t *testing.T) {
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 1, Title: "New issue", Labels: []string{"good-for-ai"}}},
	}
	cfg := Config{Owner: "owner", Repo: "repo", Label: "good-for-ai"}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/clone", slog.Default())

	if len(state.ActiveIssues) != 0 {
		t.Error("new issues without PRs should not be in recovered state")
	}
}

func TestBuildStateFromGitHub_WatchPRsSkipsLabeledIssues(t *testing.T) {
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 123, Title: "Labeled issue", Labels: []string{"good-for-ai"}}},
		prs: []PR{
			{Number: 299, State: "open", Head: "ai/issue-123"},
			{Number: 313, State: "open", Head: "kube-linter", Title: "Watched PR"},
		},
	}
	cfg := Config{
		Owner:    "owner",
		Repo:     "repo",
		Label:    "good-for-ai",
		WatchPRs: []int{313}, // Only watch PR 313
	}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/clone", slog.Default())

	// Should only contain the watched PR, not the labeled issue's PR
	if len(state.ActiveIssues) != 1 {
		t.Errorf("expected 1 issue in state, got %d", len(state.ActiveIssues))
	}

	work, ok := state.ActiveIssues[IssueKey("owner", "repo", 313)]
	if !ok {
		t.Fatal("expected watched PR 313 in state")
	}
	if work.PRNumber != 313 {
		t.Errorf("expected PR 313, got %d", work.PRNumber)
	}

	// PR 299 from the labeled issue should NOT be in state
	if _, exists := state.ActiveIssues[IssueKey("owner", "repo", 123)]; exists {
		t.Error("labeled issue 123 should not be in state when watch-prs is configured")
	}
	for _, w := range state.ActiveIssues {
		if w.PRNumber == 299 {
			t.Error("PR 299 from labeled issue should not be recovered when watch-prs is configured")
		}
	}
}

func TestBuildStateFromGitHub_EmptyLabelSkipsScan(t *testing.T) {
	gh := &mockGitHubClient{
		issues: []Issue{
			{Number: 42, Title: "Unrelated issue", Labels: []string{"ai-failed"}},
			{Number: 99, Title: "Another issue", Labels: []string{"ai-failed"}},
		},
	}
	// Triage role: no label set, no watch PRs
	cfg := Config{Owner: "owner", Repo: "repo", Label: ""}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/clone", slog.Default())

	if len(state.ActiveIssues) != 0 {
		t.Errorf("expected empty state when label is empty, got %d issues", len(state.ActiveIssues))
	}
}

func TestState_MarkRunInvestigated(t *testing.T) {
	s := NewState()

	// Mark a run as investigated
	s.MarkRunInvestigated("test-job", "run-123")

	// Verify it was marked
	if !s.IsRunInvestigated("test-job", "run-123") {
		t.Error("expected run to be marked as investigated")
	}
}

func TestState_IsRunInvestigated(t *testing.T) {
	s := NewState()

	// Initially not investigated
	if s.IsRunInvestigated("test-job", "run-123") {
		t.Error("expected run to not be investigated initially")
	}

	// Mark as investigated
	s.MarkRunInvestigated("test-job", "run-123")

	// Now it should be investigated
	if !s.IsRunInvestigated("test-job", "run-123") {
		t.Error("expected run to be investigated after marking")
	}

	// Different job should not be investigated
	if s.IsRunInvestigated("other-job", "run-123") {
		t.Error("expected different job to not be investigated")
	}

	// Same job, different run should not be investigated
	if s.IsRunInvestigated("test-job", "run-456") {
		t.Error("expected different run to not be investigated")
	}
}

func TestNewState_InvestigatedRuns(t *testing.T) {
	s := NewState()
	if s.InvestigatedRuns == nil {
		t.Error("expected InvestigatedRuns to be initialized")
	}
	if len(s.InvestigatedRuns) != 0 {
		t.Errorf("expected empty InvestigatedRuns, got %d", len(s.InvestigatedRuns))
	}
}

func TestBuildStateFromGitHub_WatchPRsRecoversCursors(t *testing.T) {
	gh := &mockGitHubClient{
		prs: []PR{
			{Number: 500, State: "open", Head: "feature-branch", Title: "My Feature"},
		},
		prComments: []ReviewComment{
			{ID: 100, User: "alice", Body: "fix this"},
			{ID: 150, User: "oompa-bot", Body: "fixed", InReplyToID: 100},
			{ID: 200, User: "oompa-bot", Body: "done", InReplyToID: 0},
		},
		prReviews: []PRReview{
			{ID: 50, User: "oompa-bot", State: "COMMENTED", Body: "addressed <!-- oompa-bot -->"},
		},
		issueComments: []ReviewComment{
			{ID: 300, User: "oompa-bot", Body: "Working on it <!-- oompa-bot -->"},
		},
	}
	cfg := Config{
		Owner:      "owner",
		Repo:       "repo",
		WatchPRs:   []int{500},
		GitHubUser: "oompa-bot",
	}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/clone", slog.Default())

	work, ok := state.ActiveIssues[IssueKey("owner", "repo", 500)]
	if !ok {
		t.Fatal("expected watched PR 500 in state")
	}
	// Cursors recover to max ID among bot-posted/replied comments only.
	if work.LastCommentID != 200 {
		t.Errorf("expected LastCommentID 200, got %d", work.LastCommentID)
	}
	if work.LastReviewID != 50 {
		t.Errorf("expected LastReviewID 50, got %d", work.LastReviewID)
	}
	if work.LastIssueCommentID != 300 {
		t.Errorf("expected LastIssueCommentID 300, got %d", work.LastIssueCommentID)
	}
}

func TestBuildStateFromGitHub_RecoversCursorsNoComments(t *testing.T) {
	// When a PR has no comments/reviews, cursors should stay at 0.
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 7, Title: "Clean PR", Labels: []string{"good-for-ai"}}},
		prs:    []PR{{Number: 77, State: "open", Head: "ai/issue-7"}},
	}
	cfg := Config{Owner: "owner", Repo: "repo", Label: "good-for-ai"}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/clone", slog.Default())

	work, ok := state.ActiveIssues[IssueKey("owner", "repo", 7)]
	if !ok {
		t.Fatal("expected issue 7 in state")
	}
	if work.LastCommentID != 0 {
		t.Errorf("expected LastCommentID 0, got %d", work.LastCommentID)
	}
	if work.LastReviewID != 0 {
		t.Errorf("expected LastReviewID 0, got %d", work.LastReviewID)
	}
	if work.LastIssueCommentID != 0 {
		t.Errorf("expected LastIssueCommentID 0, got %d", work.LastIssueCommentID)
	}
}

func TestBuildStateFromGitHub_CursorRecoverySkipsUnprocessedComments(t *testing.T) {
	// Regression test for #251: cursor recovery must NOT advance past unprocessed
	// review comments. When a second reviewer posts comments after the bot's last
	// processing cycle, those comments should remain above the cursor.
	//
	// Scenario: Bot processed alice's comment (ID 10, replied with ID 15),
	// then copilot posted two new comments (ID 20, 30) that were never processed.
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 50, Title: "Feature", Labels: []string{"good-for-ai"}}},
		prs:    []PR{{Number: 250, State: "open", Head: "ai/issue-50"}},
		prComments: []ReviewComment{
			{ID: 10, User: "alice", Body: "fix the type"},
			{ID: 15, User: "oompa-bot", Body: "fixed", InReplyToID: 10},
			// Copilot's comments arrived after bot's last cycle — never processed
			{ID: 20, User: "copilot[bot]", Body: "consider using a constant here"},
			{ID: 30, User: "copilot[bot]", Body: "this could be simplified"},
		},
		prReviews: []PRReview{
			{ID: 5, User: "alice", State: "CHANGES_REQUESTED", Body: "needs work"},
			// Copilot review arrived after bot's last cycle — never processed
			{ID: 25, User: "copilot[bot]", State: "COMMENTED", Body: "minor suggestions"},
		},
		issueComments: []ReviewComment{
			{ID: 40, User: "oompa-bot", Body: "Working on it <!-- oompa-bot -->"},
			// External comment arrived after bot's last cycle
			{ID: 50, User: "dave", Body: "/oompa please also fix the tests"},
		},
	}
	cfg := Config{Owner: "owner", Repo: "repo", Label: "good-for-ai", GitHubUser: "oompa-bot"}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/clone", slog.Default())

	work, ok := state.ActiveIssues[IssueKey("owner", "repo", 50)]
	if !ok {
		t.Fatal("expected issue 50 in state")
	}

	// LastCommentID should be 15 (bot's reply), NOT 30 (copilot's unprocessed comment).
	// This ensures copilot's comments (ID 20, 30) will be fetched on the next poll
	// cycle since they have ID > 15.
	if work.LastCommentID != 15 {
		t.Errorf("expected LastCommentID 15 (bot reply), got %d — unprocessed comments were incorrectly skipped", work.LastCommentID)
	}

	// LastReviewID should be 0 — alice's review (ID 5) was not posted by the bot
	// and copilot's review (ID 25) was never processed. No bot-posted reviews exist.
	if work.LastReviewID != 0 {
		t.Errorf("expected LastReviewID 0, got %d — unprocessed reviews were incorrectly skipped", work.LastReviewID)
	}

	// LastIssueCommentID should be 40 (bot's comment), NOT 50 (dave's unprocessed comment).
	if work.LastIssueCommentID != 40 {
		t.Errorf("expected LastIssueCommentID 40 (bot comment), got %d — unprocessed issue comments were incorrectly skipped", work.LastIssueCommentID)
	}
}

func TestBuildStateFromGitHub_CursorRecoveryWithEyesReaction(t *testing.T) {
	// When a review comment has an eyes reaction from the bot (but no bot reply),
	// it should still be considered processed. This handles the case where the agent
	// added eyes, ran, and pushed changes without posting an individual reply.
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 60, Title: "Bug fix", Labels: []string{"good-for-ai"}}},
		prs:    []PR{{Number: 300, State: "open", Head: "ai/issue-60"}},
		prComments: []ReviewComment{
			// Comment that was processed (eyes reaction) but got no bot reply
			{ID: 10, User: "alice", Body: "fix the type"},
			// Comment that was NOT processed (no eyes, no reply)
			{ID: 20, User: "bob", Body: "also fix this"},
		},
		hasEyesReaction: map[int64]map[string]bool{10: {"oompa-bot": true}},
	}
	cfg := Config{Owner: "owner", Repo: "repo", Label: "good-for-ai", GitHubUser: "oompa-bot"}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/clone", slog.Default())

	work, ok := state.ActiveIssues[IssueKey("owner", "repo", 60)]
	if !ok {
		t.Fatal("expected issue 60 in state")
	}

	// LastCommentID should be 10 (alice's comment with eyes reaction), NOT 20 (unprocessed).
	if work.LastCommentID != 10 {
		t.Errorf("expected LastCommentID 10 (comment with eyes reaction), got %d", work.LastCommentID)
	}
}

func TestBuildStateFromGitHub_CursorRecoveryAllUnprocessed(t *testing.T) {
	// When no comments have been processed (fresh PR, all from external reviewers),
	// cursors should stay at 0 so all comments get processed.
	gh := &mockGitHubClient{
		issues: []Issue{{Number: 70, Title: "New PR", Labels: []string{"good-for-ai"}}},
		prs:    []PR{{Number: 400, State: "open", Head: "ai/issue-70"}},
		prComments: []ReviewComment{
			{ID: 10, User: "alice", Body: "looks wrong"},
			{ID: 20, User: "bob", Body: "fix this"},
		},
		prReviews: []PRReview{
			{ID: 5, User: "carol", State: "CHANGES_REQUESTED", Body: "needs work"},
		},
		issueComments: []ReviewComment{
			{ID: 30, User: "eve", Body: "/oompa fix tests"},
		},
	}
	cfg := Config{Owner: "owner", Repo: "repo", Label: "good-for-ai", GitHubUser: "oompa-bot"}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/clone", slog.Default())

	work, ok := state.ActiveIssues[IssueKey("owner", "repo", 70)]
	if !ok {
		t.Fatal("expected issue 70 in state")
	}

	// All comments are from external users with no processing evidence.
	// Cursors should be 0 so everything gets processed.
	if work.LastCommentID != 0 {
		t.Errorf("expected LastCommentID 0 (no processed comments), got %d", work.LastCommentID)
	}
	if work.LastReviewID != 0 {
		t.Errorf("expected LastReviewID 0 (no processed reviews), got %d", work.LastReviewID)
	}
	if work.LastIssueCommentID != 0 {
		t.Errorf("expected LastIssueCommentID 0 (no processed issue comments), got %d", work.LastIssueCommentID)
	}
}
