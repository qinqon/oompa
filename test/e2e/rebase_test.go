package e2e

import (
	"strings"
	"testing"
)

// TestE2E_RebaseWithDivergedMain verifies that oompa can rebase a PR branch
// when main has diverged (new commits pushed upstream).
// This scenario catches regression #144 (rebase with unstaged changes from
// upstream file deletions).
func TestE2E_RebaseWithDivergedMain(t *testing.T) {
	const (
		owner = "testowner"
		repo  = "testrepo"
		label = "good-for-ai"
	)

	fg := NewFakeGitHub(t, owner, repo)

	// Seed an issue with an existing PR.
	fg.SeedIssue(FakeIssue{
		Number: 60,
		Title:  "Rebase test",
		Body:   "Test rebase with diverged main.",
		Labels: []map[string]any{{"name": label}},
	})

	fg.SeedPR(FakePR{
		Number: 600,
		Title:  "Rebase test",
		Body:   "Fixes #60",
		State:  "open",
		Head:   "ai/issue-60",
		Base:   "main",
	})

	fg.SetPRHeadSHA(600, "rebase-sha-001")
	// Set PR as "behind" — triggers rebase processing.
	fg.SetPRMergeState(600, "behind")

	h := NewHarness(t, owner, repo, label)
	pushBranchToBare(t, h, "ai/issue-60")

	// Add a new commit to main AFTER the branch was created.
	// This diverges main from the branch, triggering a rebase.
	h.AddCommitToBareMain("new-file.txt", "upstream content\n", "upstream: add new file")

	stdout, stderr, err := h.RunOompa(fg.URL(), RunOompaOpts{
		WatchPRs:  []int{600},
		Reactions: []string{"rebase"},
	})
	if err != nil {
		t.Fatalf("oompa exited with error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// === Assertions ===

	// 1. Rebase was attempted — check debug logs.
	if !strings.Contains(stderr, "rebase") && !strings.Contains(stderr, "Rebase") {
		t.Error("expected rebase-related log messages in debug output")
	}

	// 2. Branch should still exist in bare repo (rebase doesn't delete it).
	if !h.BareRepoHasBranch("ai/issue-60") {
		t.Error("expected branch 'ai/issue-60' in bare repo after rebase")
	}

	// 3. Check for a rebase comment (if not skipped).
	fg.mu.Lock()
	commentCalls := make([]FakeComment, len(fg.CommentCalls))
	copy(commentCalls, fg.CommentCalls)
	fg.mu.Unlock()

	foundRebaseComment := false
	for _, c := range commentCalls {
		if strings.Contains(c.Body, "Rebased") || strings.Contains(c.Body, "rebase") {
			foundRebaseComment = true
			break
		}
	}
	if !foundRebaseComment {
		t.Error("expected a rebase comment to be posted on the PR")
	}
}

// TestE2E_RebaseConflict verifies that oompa handles rebase conflicts by
// invoking the agent for conflict resolution when automatic rebase fails.
func TestE2E_RebaseConflict(t *testing.T) {
	const (
		owner = "testowner"
		repo  = "testrepo"
		label = "good-for-ai"
	)

	fg := NewFakeGitHub(t, owner, repo)

	fg.SeedIssue(FakeIssue{
		Number: 61,
		Title:  "Rebase conflict test",
		Body:   "Test conflict resolution.",
		Labels: []map[string]any{{"name": label}},
	})

	fg.SeedPR(FakePR{
		Number: 601,
		Title:  "Rebase conflict test",
		Body:   "Fixes #61",
		State:  "open",
		Head:   "ai/issue-61",
		Base:   "main",
	})

	fg.SetPRHeadSHA(601, "conflict-sha-001")
	// Set PR as "dirty" — triggers conflict resolution processing.
	fg.SetPRMergeState(601, "dirty")

	h := NewHarness(t, owner, repo, label)
	h.InstallFakeClaudeScript("fake-claude-rebase.sh")
	pushBranchToBare(t, h, "ai/issue-61")

	// Add a conflicting commit to main — modifies the same file the branch has.
	h.AddCommitToBareMain("BRANCH.md", "conflicting content from main\n", "upstream: modify BRANCH.md")

	stdout, stderr, err := h.RunOompa(fg.URL(), RunOompaOpts{
		WatchPRs:  []int{601},
		Reactions: []string{"conflicts"},
	})
	if err != nil {
		t.Fatalf("oompa exited with error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// Verify rebase/conflict processing was attempted.
	if !strings.Contains(stderr, "conflict") && !strings.Contains(stderr, "rebase") {
		t.Error("expected conflict/rebase log messages in debug output")
	}

	// Branch should still exist regardless of outcome.
	if !h.BareRepoHasBranch("ai/issue-61") {
		t.Error("expected branch 'ai/issue-61' in bare repo after conflict resolution attempt")
	}

	// Verify the agent script actually executed git operations (not just silently failed).
	// The fake-claude-rebase.sh writes a marker file when rebase --continue or commit succeeds.
	rebaseMarker := findMarkerFile(t, h.CloneDir(), ".oompa-rebase-marker")
	if rebaseMarker == "" {
		t.Error("expected .oompa-rebase-marker — agent script did not execute git operations")
	}
}
