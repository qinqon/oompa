package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	expectedPath := filepath.Join(cloneDir, "worktrees", "ai", "issue-42")
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
	worktreePath := filepath.Join(cloneDir, "worktrees", "ai", "issue-42")

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

	// Only git status health check should run when reusing a healthy worktree
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call (git status health check), got %d", len(runner.calls))
	}
	if runner.calls[0].Args[0] != "status" {
		t.Errorf("expected 'git status', got %v", runner.calls[0].Args)
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

	// Third call: pull --rebase
	if runner.calls[2].Args[0] != "pull" || runner.calls[2].Args[1] != "--rebase" || runner.calls[2].Args[2] != "origin" || runner.calls[2].Args[3] != "ai/issue-42" {
		t.Errorf("expected 'git pull --rebase origin ai/issue-42', got %v", runner.calls[2].Args)
	}
}

func TestEnsureRepoCloned_AlreadyCloned(t *testing.T) {
	dir := t.TempDir()
	// Create a .git directory to simulate an existing clone
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	repoURL := "https://github.com/owner/repo.git"
	// Mock returns the repo URL for "git remote get-url origin"
	runner := &mockCommandRunner{stdout: []byte(repoURL + "\n")}
	mgr := NewGitWorktreeManager(runner, dir, repoURL, "")

	err := mgr.EnsureRepoCloned(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runner.calls) != 4 {
		t.Fatalf("expected 4 calls (rev-parse, get-url, fetch, symbolic-ref), got %d", len(runner.calls))
	}

	if runner.calls[0].Args[0] != "rev-parse" || runner.calls[0].Args[1] != "HEAD" {
		t.Errorf("expected 'git rev-parse HEAD', got %v", runner.calls[0].Args)
	}
	if runner.calls[1].Args[0] != "remote" || runner.calls[1].Args[1] != "get-url" {
		t.Errorf("expected 'git remote get-url', got %v", runner.calls[1].Args)
	}
	if runner.calls[2].Args[0] != "fetch" {
		t.Errorf("expected 'git fetch', got %v", runner.calls[2].Args)
	}
	if runner.calls[3].Args[0] != "symbolic-ref" {
		t.Errorf("expected 'git symbolic-ref', got %v", runner.calls[3].Args)
	}
}

func TestEnsureRepoCloned_URLMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Mock returns a different URL (stale clone from another repo)
	runner := &mockCommandRunner{stdout: []byte("https://github.com/other/repo.git\n")}
	mgr := NewGitWorktreeManager(runner, dir, "https://github.com/owner/repo.git", "")

	err := mgr.EnsureRepoCloned(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runner.calls) != 5 {
		t.Fatalf("expected 5 calls (rev-parse, get-url, set-url, fetch, symbolic-ref), got %d", len(runner.calls))
	}

	if runner.calls[0].Args[0] != "rev-parse" || runner.calls[0].Args[1] != "HEAD" {
		t.Errorf("expected 'git rev-parse HEAD', got %v", runner.calls[0].Args)
	}
	if runner.calls[1].Args[0] != "remote" || runner.calls[1].Args[1] != "get-url" {
		t.Errorf("expected 'git remote get-url', got %v", runner.calls[1].Args)
	}
	if runner.calls[2].Args[0] != "remote" || runner.calls[2].Args[1] != "set-url" {
		t.Errorf("expected 'git remote set-url', got %v", runner.calls[2].Args)
	}
	if runner.calls[3].Args[0] != "fetch" {
		t.Errorf("expected 'git fetch', got %v", runner.calls[3].Args)
	}
	if runner.calls[4].Args[0] != "symbolic-ref" {
		t.Errorf("expected 'git symbolic-ref', got %v", runner.calls[4].Args)
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

	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 calls (clone, symbolic-ref), got %d", len(runner.calls))
	}

	if runner.calls[0].Name != "git" || runner.calls[0].Args[0] != "clone" {
		t.Errorf("expected 'git clone', got %q %v", runner.calls[0].Name, runner.calls[0].Args)
	}
	if runner.calls[1].Args[0] != "symbolic-ref" {
		t.Errorf("expected 'git symbolic-ref', got %v", runner.calls[1].Args)
	}
}

func TestEnsureRepoCloned_CorruptedBaseRepo(t *testing.T) {
	dir := t.TempDir()
	// Create a .git directory to simulate an existing (but corrupted) clone
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Mock that fails on rev-parse HEAD (corrupted repo)
	runner := &worktreeHealthRunner{
		mockCommandRunner: &mockCommandRunner{},
		failRevParse:      true,
	}
	mgr := NewGitWorktreeManager(runner, dir, "https://github.com/owner/repo.git", "")

	err := mgr.EnsureRepoCloned(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have nuked the corrupted repo and re-cloned.
	// Calls: rev-parse HEAD (fails) -> clone -> symbolic-ref
	foundClone := false
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "clone" {
			foundClone = true
		}
	}
	if !foundClone {
		t.Error("expected 'git clone' after corrupted base repo detected")
	}
}

func TestIsWorktreeHealthy_EmptyDirectory(t *testing.T) {
	// Empty directory (no .git file) should be detected as unhealthy
	worktreePath := t.TempDir()
	runner := &mockCommandRunner{}
	mgr := NewGitWorktreeManager(runner, "/tmp/repo", "https://github.com/owner/repo.git", "")

	if mgr.isWorktreeHealthy(context.Background(), worktreePath) {
		t.Error("expected empty directory to be unhealthy")
	}

	// git status should NOT be called when .git file is missing
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "status" {
			t.Error("git status should not be called when .git file is missing")
		}
	}
}

func TestIsWorktreeHealthy_GitStatusFails(t *testing.T) {
	// Worktree with .git file but git status fails → unhealthy
	worktreePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktreePath, ".git"), []byte("gitdir: ..."), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &mockCommandRunner{err: &mockError{msg: "fatal: not a git repository"}}
	mgr := NewGitWorktreeManager(runner, "/tmp/repo", "https://github.com/owner/repo.git", "")

	if mgr.isWorktreeHealthy(context.Background(), worktreePath) {
		t.Error("expected worktree with failing git status to be unhealthy")
	}
}

func TestIsWorktreeHealthy_Healthy(t *testing.T) {
	// Worktree with .git file and git status succeeds → healthy
	worktreePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktreePath, ".git"), []byte("gitdir: ..."), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &mockCommandRunner{}
	mgr := NewGitWorktreeManager(runner, "/tmp/repo", "https://github.com/owner/repo.git", "")

	if !mgr.isWorktreeHealthy(context.Background(), worktreePath) {
		t.Error("expected healthy worktree to pass validation")
	}
}

func TestCreateWorktree_HealthyReusesNoRecreate(t *testing.T) {
	// Healthy worktree → reused, no cleanup/recreate
	cloneDir := t.TempDir()
	worktreePath := filepath.Join(cloneDir, "worktrees", "ai", "issue-42")
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

	// Only the git status health check should have run; no prune/recreate
	foundPrune := false
	foundWorktreeAdd := false
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 1 && c.Args[0] == "worktree" && c.Args[1] == "prune" {
			foundPrune = true
		}
		if c.Name == "git" && len(c.Args) > 1 && c.Args[0] == "worktree" && c.Args[1] == "add" {
			foundWorktreeAdd = true
		}
	}
	if foundPrune {
		t.Error("should not prune when worktree is healthy")
	}
	if foundWorktreeAdd {
		t.Error("should not recreate worktree when healthy")
	}
}

func TestCreateWorktree_EmptyDirectoryRecreates(t *testing.T) {
	// Empty worktree directory (no .git file) → detected as unhealthy, cleaned up and recreated
	cloneDir := t.TempDir()
	worktreePath := filepath.Join(cloneDir, "worktrees", "ai", "issue-42")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}

	runner := &mockCommandRunner{}
	mgr := NewGitWorktreeManager(runner, cloneDir, "https://github.com/owner/repo.git", "")

	_, err := mgr.CreateWorktree(context.Background(), "ai/issue-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify cleanup + recreate was called
	foundPrune := false
	foundWorktreeAdd := false
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 1 && c.Args[0] == "worktree" && c.Args[1] == "prune" {
			foundPrune = true
		}
		if c.Name == "git" && len(c.Args) > 1 && c.Args[0] == "worktree" && c.Args[1] == "add" {
			foundWorktreeAdd = true
		}
	}
	if !foundPrune {
		t.Error("expected 'git worktree prune' for empty worktree directory")
	}
	if !foundWorktreeAdd {
		t.Error("expected 'git worktree add' to recreate empty worktree directory")
	}
}

func TestCreateWorktree_CorruptedGitStatusRecreates(t *testing.T) {
	// Worktree with .git file but git status fails → detected as unhealthy, cleaned up and recreated
	cloneDir := t.TempDir()
	worktreePath := filepath.Join(cloneDir, "worktrees", "ai", "issue-42")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, ".git"), []byte("gitdir: ..."), 0o644); err != nil {
		t.Fatal(err)
	}

	// Runner that fails on git status (simulating corrupted objects)
	// but succeeds on subsequent calls (worktree remove, prune, add, etc.)
	runner := &worktreeHealthRunner{
		mockCommandRunner: &mockCommandRunner{},
		failGitStatus:     true,
	}
	mgr := NewGitWorktreeManager(runner, cloneDir, "https://github.com/owner/repo.git", "")

	_, err := mgr.CreateWorktree(context.Background(), "ai/issue-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify cleanup + recreate was called
	foundPrune := false
	foundWorktreeAdd := false
	for _, c := range runner.calls {
		if c.Name == "git" && len(c.Args) > 1 && c.Args[0] == "worktree" && c.Args[1] == "prune" {
			foundPrune = true
		}
		if c.Name == "git" && len(c.Args) > 1 && c.Args[0] == "worktree" && c.Args[1] == "add" {
			foundWorktreeAdd = true
		}
	}
	if !foundPrune {
		t.Error("expected 'git worktree prune' after corrupted worktree detected")
	}
	if !foundWorktreeAdd {
		t.Error("expected 'git worktree add' to recreate corrupted worktree")
	}
}

// worktreeHealthRunner selectively fails git commands for health check tests.
type worktreeHealthRunner struct {
	*mockCommandRunner
	failGitStatus bool // fail on "git status"
	failRevParse  bool // fail on "git rev-parse HEAD"
}

func (r *worktreeHealthRunner) Run(ctx context.Context, workDir, name string, args ...string) (stdout, stderr []byte, err error) {
	r.mockCommandRunner.Run(ctx, workDir, name, args...) //nolint:errcheck // recording call

	if name == "git" && len(args) > 0 {
		if r.failGitStatus && args[0] == "status" {
			return nil, []byte("fatal: not a git repository"), &mockError{msg: "exit status 128"}
		}
		if r.failRevParse && args[0] == "rev-parse" && len(args) > 1 && args[1] == "HEAD" {
			return nil, []byte("fatal: unable to read sha1"), &mockError{msg: "exit status 128"}
		}
	}

	return nil, nil, nil
}

// syncPullFailRunner fails `git pull --rebase` with a scripted stderr and
// optionally fails the recovery `git reset --hard`.
type syncPullFailRunner struct {
	mockCommandRunner
	pullStderr  string
	failReset   bool
	resetStderr string
}

func (r *syncPullFailRunner) Run(ctx context.Context, workDir, name string, args ...string) (stdout, stderr []byte, err error) {
	stdout, stderr, err = r.mockCommandRunner.Run(ctx, workDir, name, args...)
	if name == "git" && len(args) > 0 {
		switch args[0] {
		case "pull":
			return nil, []byte(r.pullStderr), fmt.Errorf("exit status 1")
		case "reset":
			if r.failReset {
				return nil, []byte(r.resetStderr), fmt.Errorf("exit status 128")
			}
		case "rev-parse":
			return []byte("ai/issue-42\n"), nil, nil
		}
	}
	return stdout, stderr, err
}

func TestSyncWorktree_PullConflictResetsToRemote(t *testing.T) {
	runner := &syncPullFailRunner{pullStderr: "CONFLICT (content): Merge conflict in main.go\nerror: could not apply abc123"}
	mgr := NewGitWorktreeManager(runner, "/tmp/repo", "https://github.com/owner/repo.git", "")

	err := mgr.SyncWorktree(context.Background(), "/tmp/repo/worktrees/ai/issue-42")
	if err != nil {
		t.Fatalf("expected recovery via reset, got error: %v", err)
	}

	// Calls: fetch, rev-parse, pull (fails), rebase --abort, reset --hard origin/ai/issue-42
	if len(runner.calls) != 5 {
		t.Fatalf("expected 5 calls (fetch, rev-parse, pull, abort, reset), got %d: %v", len(runner.calls), runner.calls)
	}
	if runner.calls[3].Args[0] != "rebase" || runner.calls[3].Args[1] != "--abort" {
		t.Errorf("expected 'git rebase --abort' after failed pull, got %v", runner.calls[3].Args)
	}
	expectedReset := []string{"reset", "--hard", "origin/ai/issue-42"}
	for i, arg := range expectedReset {
		if i >= len(runner.calls[4].Args) || runner.calls[4].Args[i] != arg {
			t.Fatalf("expected reset call %v, got %v", expectedReset, runner.calls[4].Args)
		}
	}
}

func TestSyncWorktree_MissingRemoteRefIsOK(t *testing.T) {
	runner := &syncPullFailRunner{pullStderr: "fatal: couldn't find remote ref ai/issue-42"}
	mgr := NewGitWorktreeManager(runner, "/tmp/repo", "https://github.com/owner/repo.git", "")

	err := mgr.SyncWorktree(context.Background(), "/tmp/repo/worktrees/ai/issue-42")
	if err != nil {
		t.Fatalf("expected nil for missing remote ref, got: %v", err)
	}

	// No recovery commands after the failed pull: fetch, rev-parse, pull only.
	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 calls (no recovery for missing ref), got %d: %v", len(runner.calls), runner.calls)
	}
}

func TestSyncWorktree_PullFailureAndResetFailureErrors(t *testing.T) {
	runner := &syncPullFailRunner{
		pullStderr:  "error: could not apply abc123",
		failReset:   true,
		resetStderr: "fatal: ambiguous argument 'origin/ai/issue-42'",
	}
	mgr := NewGitWorktreeManager(runner, "/tmp/repo", "https://github.com/owner/repo.git", "")

	err := mgr.SyncWorktree(context.Background(), "/tmp/repo/worktrees/ai/issue-42")
	if err == nil {
		t.Fatal("expected error when both pull and recovery reset fail")
	}
	if !strings.Contains(err.Error(), "recovery reset failed") {
		t.Errorf("expected error to mention failed recovery, got: %v", err)
	}
}
