package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(BuildOompa(m))
}

// TestE2ESmoke_NewIssueToPR verifies the full happy path:
// a new labeled issue is discovered, assigned, commented on, implemented by
// the fake agent, and a PR is created — all via a black-box oompa subprocess.
func TestE2ESmoke_NewIssueToPR(t *testing.T) {
	const (
		owner = "testowner"
		repo  = "testrepo"
		label = "good-for-ai"
	)

	// Set up fake GitHub
	fg := NewFakeGitHub(t, owner, repo)
	fg.SeedIssue(FakeIssue{
		Number: 42,
		Title:  "Fix the widget",
		Body:   "The widget is broken, please fix it.",
		Labels: []map[string]any{{"name": label}},
	})

	// Set up harness (bare repo, fake claude, isolated gitconfig)
	h := NewHarness(t, owner, repo, label)

	// Run oompa
	stdout, stderr, err := h.RunOompa(fg.URL())
	if err != nil {
		t.Fatalf("oompa exited with error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// === Assertions ===

	// 1. Exactly one CreatePR call with correct head and base
	fg.mu.Lock()
	prCalls := fg.CreatePRCalls
	fg.mu.Unlock()

	if len(prCalls) != 1 {
		t.Fatalf("expected exactly 1 CreatePR call, got %d", len(prCalls))
	}
	if prCalls[0].Head != "ai/issue-42" {
		t.Errorf("expected PR head 'ai/issue-42', got %q", prCalls[0].Head)
	}
	if prCalls[0].Base != "main" {
		t.Errorf("expected PR base 'main', got %q", prCalls[0].Base)
	}

	// 2. Issue was assigned then unassigned (verify both calls and final state)
	fg.mu.Lock()
	assignCalls := fg.AssignCalls
	unassignCalls := fg.UnassignCalls
	finalAssignees := fg.issues[42].Assignees
	fg.mu.Unlock()

	if len(assignCalls) < 1 {
		t.Error("expected at least 1 AssignIssue call")
	}
	if len(unassignCalls) < 1 {
		t.Error("expected at least 1 UnassignIssue call")
	}
	if len(finalAssignees) != 0 {
		t.Errorf("expected issue to end with no assignees, got %v", finalAssignees)
	}

	// 3. At least one comment contains "working on this issue"
	fg.mu.Lock()
	commentCalls := fg.CommentCalls
	fg.mu.Unlock()

	foundWorkingComment := false
	for _, c := range commentCalls {
		if strings.Contains(c.Body, "working on this issue") {
			foundWorkingComment = true
			break
		}
	}
	if !foundWorkingComment {
		t.Error("expected a comment containing 'working on this issue'")
	}

	// 4. The bare repo has the pushed branch
	if !h.BareRepoHasBranch("ai/issue-42") {
		t.Error("expected branch 'ai/issue-42' in bare repo, but it was not found")
	}
}
