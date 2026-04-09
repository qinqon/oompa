package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateWorktree_New(t *testing.T) {
	runner := &mockCommandRunner{}
	cloneDir := "/tmp/repo"
	mgr := NewGitWorktreeManager(runner, cloneDir, "https://github.com/owner/repo.git", "")

	path, err := mgr.CreateWorktree(context.Background(), "ai/issue-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPath := filepath.Join(cloneDir, "worktrees", "ai/issue-42")
	if path != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, path)
	}

	if len(runner.calls) != 4 {
		t.Fatalf("expected 4 calls (worktree remove, prune, branch delete, worktree add), got %d", len(runner.calls))
	}

	if runner.calls[0].Args[0] != "worktree" || runner.calls[0].Args[1] != "remove" {
		t.Errorf("expected first call to be 'git worktree remove', got %v", runner.calls[0].Args)
	}
	if runner.calls[1].Args[0] != "worktree" || runner.calls[1].Args[1] != "prune" {
		t.Errorf("expected second call to be 'git worktree prune', got %v", runner.calls[1].Args)
	}
	if runner.calls[2].Args[0] != "branch" {
		t.Errorf("expected third call to be 'git branch', got %v", runner.calls[2].Args)
	}

	call := runner.calls[3]
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

func TestCreateWorktree_ReusesExisting(t *testing.T) {
	cloneDir := t.TempDir()
	worktreePath := filepath.Join(cloneDir, "worktrees", "ai/issue-42")

	// Simulate an existing worktree with a .git marker
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, ".git"), []byte("gitdir: ..."), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &mockCommandRunner{}
	mgr := NewGitWorktreeManager(runner, cloneDir, "https://github.com/owner/repo.git", "")

	path, err := mgr.CreateWorktree(context.Background(), "ai/issue-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if path != worktreePath {
		t.Errorf("expected path %q, got %q", worktreePath, path)
	}

	if len(runner.calls) != 0 {
		t.Errorf("expected no git calls when reusing worktree, got %d", len(runner.calls))
	}
}

func TestRemoveWorktree(t *testing.T) {
	runner := &mockCommandRunner{}
	mgr := NewGitWorktreeManager(runner, "/tmp/repo", "https://github.com/owner/repo.git", "")

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

func TestSyncWorktree(t *testing.T) {
	runner := &mockCommandRunner{stdout: []byte("ai/issue-42\n")}
	mgr := NewGitWorktreeManager(runner, "/tmp/repo", "https://github.com/owner/repo.git", "")

	err := mgr.SyncWorktree(context.Background(), "/tmp/repo/worktrees/ai/issue-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 calls (fetch, rev-parse, reset), got %d", len(runner.calls))
	}

	// First call: fetch
	if runner.calls[0].Args[0] != "fetch" {
		t.Errorf("expected first call 'git fetch', got %v", runner.calls[0].Args)
	}
	if runner.calls[0].WorkDir != "/tmp/repo/worktrees/ai/issue-42" {
		t.Errorf("expected workdir for fetch, got %q", runner.calls[0].WorkDir)
	}

	// Second call: rev-parse
	if runner.calls[1].Args[0] != "rev-parse" {
		t.Errorf("expected second call 'git rev-parse', got %v", runner.calls[1].Args)
	}

	// Third call: reset --hard
	if runner.calls[2].Args[0] != "reset" || runner.calls[2].Args[1] != "--hard" || runner.calls[2].Args[2] != "origin/ai/issue-42" {
		t.Errorf("expected 'git reset --hard origin/ai/issue-42', got %v", runner.calls[2].Args)
	}
}

func TestEnsureRepoCloned_AlreadyCloned(t *testing.T) {
	dir := t.TempDir()
	// Create a .git directory to simulate an existing clone
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	runner := &mockCommandRunner{}
	mgr := NewGitWorktreeManager(runner, dir, "https://github.com/owner/repo.git", "")

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
	mgr := NewGitWorktreeManager(runner, dir, "https://github.com/owner/repo.git", "")

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
