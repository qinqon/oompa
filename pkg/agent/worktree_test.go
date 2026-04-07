package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateWorktree(t *testing.T) {
	runner := &mockCommandRunner{}
	cloneDir := "/tmp/repo"
	mgr := NewGitWorktreeManager(runner, cloneDir, "https://github.com/owner/repo.git")

	path, err := mgr.CreateWorktree(context.Background(), "ai/issue-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPath := filepath.Join(cloneDir, "worktrees", "ai/issue-42")
	if path != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, path)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}

	call := runner.calls[0]
	expectedArgs := []string{"worktree", "add", "-b", "ai/issue-42", expectedPath, "origin/main"}
	if call.Name != "git" {
		t.Errorf("expected command 'git', got %q", call.Name)
	}
	for i, arg := range expectedArgs {
		if i >= len(call.Args) || call.Args[i] != arg {
			t.Errorf("arg[%d]: expected %q, got %q", i, arg, call.Args[i])
		}
	}
}

func TestRemoveWorktree(t *testing.T) {
	runner := &mockCommandRunner{}
	mgr := NewGitWorktreeManager(runner, "/tmp/repo", "https://github.com/owner/repo.git")

	err := mgr.RemoveWorktree(context.Background(), "/tmp/repo/worktrees/ai/issue-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}

	call := runner.calls[0]
	if call.Name != "git" {
		t.Errorf("expected command 'git', got %q", call.Name)
	}
	expectedArgs := []string{"worktree", "remove", "--force", "/tmp/repo/worktrees/ai/issue-42"}
	for i, arg := range expectedArgs {
		if i >= len(call.Args) || call.Args[i] != arg {
			t.Errorf("arg[%d]: expected %q, got %q", i, arg, call.Args[i])
		}
	}
}

func TestEnsureRepoCloned_AlreadyCloned(t *testing.T) {
	dir := t.TempDir()
	// Create a .git directory to simulate an existing clone
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	runner := &mockCommandRunner{}
	mgr := NewGitWorktreeManager(runner, dir, "https://github.com/owner/repo.git")

	err := mgr.EnsureRepoCloned(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}

	call := runner.calls[0]
	if call.Name != "git" || call.Args[0] != "fetch" {
		t.Errorf("expected 'git fetch', got %q %v", call.Name, call.Args)
	}
}

func TestEnsureRepoCloned_Fresh(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newrepo")

	runner := &mockCommandRunner{}
	mgr := NewGitWorktreeManager(runner, dir, "https://github.com/owner/repo.git")

	err := mgr.EnsureRepoCloned(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}

	call := runner.calls[0]
	if call.Name != "git" || call.Args[0] != "clone" {
		t.Errorf("expected 'git clone', got %q %v", call.Name, call.Args)
	}
}
