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
			{ID: 20, User: "bob", Body: "fix this"},
		},
		prReviews: []PRReview{
			{ID: 5, User: "carol", State: "CHANGES_REQUESTED", Body: "needs work"},
			{ID: 15, User: "dave", State: "APPROVED", Body: "lgtm"},
		},
		issueComments: []ReviewComment{
			{ID: 30, User: "eve", Body: "/oompa fix tests"},
		},
	}
	cfg := Config{Owner: "owner", Repo: "repo", Label: "good-for-ai"}

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
	// Comment cursors should be recovered to max existing IDs to prevent
	// re-processing old comments after a restart.
	if work.LastCommentID != 20 {
		t.Errorf("expected LastCommentID 20, got %d", work.LastCommentID)
	}
	if work.LastReviewID != 15 {
		t.Errorf("expected LastReviewID 15, got %d", work.LastReviewID)
	}
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
			{ID: 200, User: "bob", Body: "also this"},
		},
		prReviews: []PRReview{
			{ID: 50, User: "carol", State: "CHANGES_REQUESTED", Body: "needs work"},
		},
		issueComments: []ReviewComment{
			{ID: 300, User: "eve", Body: "/oompa fix tests"},
		},
	}
	cfg := Config{
		Owner:    "owner",
		Repo:     "repo",
		WatchPRs: []int{500},
	}

	state := BuildStateFromGitHub(context.Background(), gh, cfg, "/tmp/clone", slog.Default())

	work, ok := state.ActiveIssues[IssueKey("owner", "repo", 500)]
	if !ok {
		t.Fatal("expected watched PR 500 in state")
	}
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
