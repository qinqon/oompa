package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestE2E_ReviewFeedback verifies the PR review feedback loop:
// oompa discovers an existing PR with review comments, invokes the agent to
// address them, and pushes the changes.
// This scenario catches regressions in: #137, #151, #149, #162, #164, #181, #211.
func TestE2E_ReviewFeedback(t *testing.T) {
	const (
		owner = "testowner"
		repo  = "testrepo"
		label = "good-for-ai"
	)

	fg := NewFakeGitHub(t, owner, repo)

	// Seed an issue that already has an open PR — oompa must discover the
	// existing PR via ListPRsByHead and track it as StatusPROpen.
	fg.SeedIssue(FakeIssue{
		Number: 10,
		Title:  "Add logging middleware",
		Body:   "Add structured logging middleware for HTTP handlers.",
		Labels: []map[string]any{{"name": label}},
	})

	// Seed the existing PR for this issue.
	fg.SeedPR(FakePR{
		Number: 200,
		Title:  "Add logging middleware",
		Body:   "Fixes #10",
		State:  "open",
		Head:   "ai/issue-10",
		Base:   "main",
	})

	// Seed 35 review comments to exercise the pagination loop (catches #151).
	// The pre-#151 code used PerPage=30, which would drop comments after page 1.
	// Comments 1001-1002 are the primary review feedback; 1003-1035 are filler
	// to push total count past the old pagination boundary.
	fg.SeedReviewComment(200, FakeReviewComment{
		ID:   1001,
		Body: "Please add error handling for the nil case.",
		Path: "middleware.go",
		Line: 42,
		User: map[string]any{"login": "reviewer1"},
	})
	fg.SeedReviewComment(200, FakeReviewComment{
		ID:   1002,
		Body: "Consider using a context logger instead of the global one.",
		Path: "middleware.go",
		Line: 55,
		User: map[string]any{"login": "reviewer1"},
	})
	for i := int64(1003); i <= 1035; i++ {
		fg.SeedReviewComment(200, FakeReviewComment{
			ID:   i,
			Body: fmt.Sprintf("Review comment %d for pagination test.", i),
			Path: "middleware.go",
			Line: int(i - 1000),
			User: map[string]any{"login": "reviewer1"},
		})
	}

	// Set HEAD SHA so the PR appears valid.
	fg.SetPRHeadSHA(200, "abc123deadbeef")

	h := NewHarness(t, owner, repo, label)
	// Install the review-specific fake claude that creates fixup commits.
	h.InstallFakeClaudeScript("fake-claude-review.sh")

	// Push the initial branch to the bare repo so the worktree has something to work with.
	pushBranchToBare(t, h, "ai/issue-10")
	beforeSHA := bareBranchSHA(t, h, "ai/issue-10")

	// Run oompa with --watch-prs to directly watch the PR
	// and --reactions reviews to enable review processing only.
	stdout, stderr, err := h.RunOompa(fg.URL(), RunOompaOpts{
		WatchPRs:  []int{200},
		Reactions: []string{"reviews"},
	})
	if err != nil {
		t.Fatalf("oompa exited with error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// === Assertions ===

	// 1. Eyes reactions were added to signal processing of all 35 seeded comments.
	fg.mu.Lock()
	reactionCalls := make([]ReactionCall, len(fg.ReactionCalls))
	copy(reactionCalls, fg.ReactionCalls)
	fg.mu.Unlock()

	eyesByCommentID := map[int64]bool{}
	for _, rc := range reactionCalls {
		if rc.Reaction == "eyes" {
			eyesByCommentID[rc.CommentID] = true
		}
	}
	// Verify all 35 seeded comments (1001-1035) received eyes reactions.
	// This exercises the pagination loop: with PerPage=30 in the fake,
	// comments 31-35 are only visible on page 2.
	for id := int64(1001); id <= 1035; id++ {
		if !eyesByCommentID[id] {
			t.Errorf("expected 'eyes' reaction on review comment %d (pagination regression)", id)
		}
	}

	// 2. Branch was pushed and advanced (agent made changes).
	afterSHA := bareBranchSHA(t, h, "ai/issue-10")
	if afterSHA == beforeSHA {
		t.Error("expected branch 'ai/issue-10' to advance after review processing")
	}

	// 3. Verify prompt was delivered via stdin (catches #169 regression).
	stdinMarker := findStdinMarker(t, h.CloneDir())
	if stdinMarker == "" {
		t.Fatal("expected .oompa-stdin-marker file — prompt was not delivered via stdin")
	}
	data, err := readFileContent(t, stdinMarker)
	if err != nil {
		t.Fatalf("reading stdin marker: %v", err)
	}
	if !strings.Contains(data, "error handling") || !strings.Contains(data, "context logger") {
		t.Error("expected review prompt to contain review comment text")
	}
}

// TestE2E_ReviewFeedback_PRConversationComment verifies that /oompa prefixed
// comments on the PR conversation tab are processed as directives.
func TestE2E_ReviewFeedback_PRConversationComment(t *testing.T) {
	const (
		owner = "testowner"
		repo  = "testrepo"
		label = "good-for-ai"
	)

	fg := NewFakeGitHub(t, owner, repo)

	fg.SeedIssue(FakeIssue{
		Number: 11,
		Title:  "Update docs",
		Body:   "Update the README.",
		Labels: []map[string]any{{"name": label}},
	})

	fg.SeedPR(FakePR{
		Number: 201,
		Title:  "Update docs",
		Body:   "Fixes #11",
		State:  "open",
		Head:   "ai/issue-11",
		Base:   "main",
	})

	// Seed an issue comment (PR conversation tab) with /oompa prefix.
	fg.SeedIssueComment(201, FakeComment{
		ID:   2001,
		Body: "/oompa please also update the CHANGELOG",
		User: map[string]any{"login": "reviewer1"},
	})

	fg.SetPRHeadSHA(201, "def456deadbeef")

	h := NewHarness(t, owner, repo, label)
	h.InstallFakeClaudeScript("fake-claude-review.sh")
	pushBranchToBare(t, h, "ai/issue-11")
	beforeSHA := bareBranchSHA(t, h, "ai/issue-11")

	stdout, stderr, err := h.RunOompa(fg.URL(), RunOompaOpts{
		WatchPRs:  []int{201},
		Reactions: []string{"reviews"},
	})
	if err != nil {
		t.Fatalf("oompa exited with error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// Branch should be pushed and advanced from addressing the /oompa directive.
	afterSHA := bareBranchSHA(t, h, "ai/issue-11")
	if afterSHA == beforeSHA {
		t.Error("expected branch 'ai/issue-11' to advance after /oompa directive processing")
	}
}

// pushBranchToBare creates and pushes a branch to the bare repo so oompa
// can create a worktree from it.
func pushBranchToBare(t *testing.T, h *Harness, branchName string) {
	t.Helper()
	scratch := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := newGitCommand(args...)
		if dir != "" {
			cmd.Dir = dir
		}
		cmd.Env = gitEnv(h)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	run("", "clone", h.BareRepo(), scratch)
	run(scratch, "config", "user.name", "e2e")
	run(scratch, "config", "user.email", "e2e@example.com")
	run(scratch, "checkout", "-b", branchName)
	writeFile(t, scratch, "BRANCH.md", "# Branch "+branchName+"\n")
	run(scratch, "add", "BRANCH.md")
	run(scratch, "commit", "-m", "initial branch commit")
	run(scratch, "push", "origin", branchName)
}

// readFileContent reads a file and returns its trimmed content.
func readFileContent(t *testing.T, path string) (string, error) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
