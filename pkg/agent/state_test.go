package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadState_Empty(t *testing.T) {
	s := LoadState("/nonexistent/path/state.json")
	if s == nil {
		t.Fatal("expected non-nil state")
	}
	if len(s.ActiveIssues) != 0 {
		t.Errorf("expected empty ActiveIssues, got %d", len(s.ActiveIssues))
	}
}

func TestLoadState_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	original := &State{
		ActiveIssues: map[int]*IssueWork{
			42: {
				IssueNumber:   42,
				IssueTitle:    "Fix bug",
				WorktreePath:  "/tmp/worktree-42",
				BranchName:    "ai/issue-42",
				PRNumber:      100,
				LastCommentID: 555,
				Status:        "pr-open",
				CreatedAt:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
		path: path,
	}

	if err := original.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded := LoadState(path)
	if len(loaded.ActiveIssues) != 1 {
		t.Fatalf("expected 1 active issue, got %d", len(loaded.ActiveIssues))
	}

	work := loaded.ActiveIssues[42]
	if work == nil {
		t.Fatal("expected issue 42 in state")
	}
	if work.IssueTitle != "Fix bug" {
		t.Errorf("expected title 'Fix bug', got %q", work.IssueTitle)
	}
	if work.PRNumber != 100 {
		t.Errorf("expected PRNumber 100, got %d", work.PRNumber)
	}
	if work.Status != "pr-open" {
		t.Errorf("expected status 'pr-open', got %q", work.Status)
	}
}

func TestLoadState_Corrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := os.WriteFile(path, []byte("{corrupt json!!!"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := LoadState(path)
	if s == nil {
		t.Fatal("expected non-nil state")
	}
	if len(s.ActiveIssues) != 0 {
		t.Errorf("expected empty ActiveIssues on corrupt JSON, got %d", len(s.ActiveIssues))
	}
}

func TestSaveState_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "deep", "state.json")

	s := &State{
		ActiveIssues: map[int]*IssueWork{
			1: {IssueNumber: 1, Status: "implementing"},
		},
		path: path,
	}

	if err := s.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected state file to be created")
	}

	loaded := LoadState(path)
	if len(loaded.ActiveIssues) != 1 {
		t.Errorf("expected 1 active issue after reload, got %d", len(loaded.ActiveIssues))
	}
}
