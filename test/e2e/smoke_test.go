package e2e

import (
	"os"
	"path/filepath"
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

	// 5. Prompt was delivered via stdin (catches #169)
	// The fake-claude.sh writes stdin content to .oompa-stdin-marker.
	// Find the worktree directory and check for the marker file.
	stdinMarker := findStdinMarker(t, h.CloneDir())
	if stdinMarker == "" {
		t.Error("expected .oompa-stdin-marker file to exist (stdin prompt delivery), but not found")
	} else {
		data, err := os.ReadFile(stdinMarker)
		if err != nil {
			t.Fatalf("reading stdin marker: %v", err)
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			t.Error("stdin marker file is empty — prompt was not delivered via stdin (regression #169)")
		}
		// Verify the prompt contains issue context
		if !strings.Contains(content, "Fix the widget") {
			t.Error("stdin prompt does not contain issue title 'Fix the widget'")
		}
	}
}

// TestE2ESmoke_CorruptWorktreeRecovery verifies that oompa recovers from a corrupt
// worktree and still produces a PR. This catches regression #190.
func TestE2ESmoke_CorruptWorktreeRecovery(t *testing.T) {
	const (
		owner = "testowner"
		repo  = "testrepo"
		label = "good-for-ai"
	)

	fg := NewFakeGitHub(t, owner, repo)
	fg.SeedIssue(FakeIssue{
		Number: 55,
		Title:  "Fix the corrupt widget",
		Body:   "Widget corrupted, needs fixing.",
		Labels: []map[string]any{{"name": label}},
	})

	h := NewHarness(t, owner, repo, label)

	// First, clone the base repo manually so EnsureRepoCloned finds it.
	// This simulates the state where oompa has previously cloned the repo.
	baseCloneDir := filepath.Join(h.CloneDir(), owner, repo)
	cloneAndSetup(t, h, baseCloneDir)

	// Pre-create a corrupt worktree directory.
	// The recovery path fires when:
	//   1. The worktree directory exists
	//   2. It has a .git file (not directory — worktrees use .git files pointing to main repo)
	//   3. But git status fails
	worktreeDir := filepath.Join(baseCloneDir, "worktrees", "ai", "issue-55")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	// Create a .git file that makes it look like a worktree but points nowhere valid
	gitFile := filepath.Join(worktreeDir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: /nonexistent/path\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}

	stdout, stderr, err := h.RunOompa(fg.URL())
	if err != nil {
		t.Fatalf("oompa exited with error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// Assert oompa recovered and still created a PR
	fg.mu.Lock()
	prCalls := fg.CreatePRCalls
	fg.mu.Unlock()

	if len(prCalls) != 1 {
		t.Fatalf("expected exactly 1 CreatePR call after worktree recovery, got %d", len(prCalls))
	}
	if prCalls[0].Head != "ai/issue-55" {
		t.Errorf("expected PR head 'ai/issue-55', got %q", prCalls[0].Head)
	}

	if !h.BareRepoHasBranch("ai/issue-55") {
		t.Error("expected branch 'ai/issue-55' in bare repo after recovery")
	}
}

// cloneAndSetup clones the bare repo into the given directory with proper git config.
func cloneAndSetup(t *testing.T, h *Harness, dir string) {
	t.Helper()
	run := func(d string, args ...string) {
		t.Helper()
		cmd := newGitCommand(args...)
		if d != "" {
			cmd.Dir = d
		}
		cmd.Env = gitEnv(h)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	run("", "clone", h.BareRepo(), dir)
	run(dir, "config", "user.name", "e2e")
	run(dir, "config", "user.email", "e2e@example.com")
}

// findStdinMarker searches for the .oompa-stdin-marker file in the clone directory tree.
func findStdinMarker(t *testing.T, cloneDir string) string {
	t.Helper()
	return findMarkerFile(t, cloneDir, ".oompa-stdin-marker")
}
