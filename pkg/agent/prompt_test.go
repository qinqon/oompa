package agent

import (
	"strings"
	"testing"
)

func TestBuildImplementationPrompt(t *testing.T) {
	issue := Issue{
		Number: 42,
		Title:  "Fix nil pointer in handler",
		Body:   "The handler crashes when input is nil.",
		Labels: []string{"good-for-ai"},
	}

	prompt := buildImplementationPrompt(issue, "")

	checks := []string{
		"#42",
		"Fix nil pointer in handler",
		"The handler crashes when input is nil.",
		"<user-provided-content>",
		"</user-provided-content>",
		"untrusted user input",
		"Do NOT push",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	// Must NOT contain push/PR instructions
	forbidden := []string{
		"gh pr create",
		"git push",
	}
	for _, bad := range forbidden {
		if strings.Contains(prompt, bad) {
			t.Errorf("prompt should NOT contain %q", bad)
		}
	}

	// With signed-off-by
	prompt = buildImplementationPrompt(issue, "Test User <test@example.com>")
	if !strings.Contains(prompt, "Signed-off-by: Test User <test@example.com>") {
		t.Error("prompt missing Signed-off-by when provided")
	}
}

func TestBuildReviewResponsePrompt(t *testing.T) {
	work := IssueWork{
		IssueNumber: 42,
		IssueTitle:  "Fix nil pointer in handler",
		PRNumber:    100,
	}

	comments := []ReviewComment{
		{
			ID:   1,
			User: "reviewer1",
			Body: "Please add a nil check here",
			Path: "handler.go",
			Line: 15,
		},
		{
			ID:   2,
			User: "reviewer2",
			Body: "Missing test case for empty input",
			Path: "handler_test.go",
			Line: 30,
		},
	}

	prompt := buildReviewResponsePrompt(work, comments, nil, "owner", "repo")

	checks := []string{
		"reviewer1",
		"handler.go",
		"line 15",
		"Please add a nil check here",
		"reviewer2",
		"handler_test.go",
		"line 30",
		"Missing test case for empty input",
		"owner/repo",
		"comment ID: 1",
		"comment ID: 2",
		"pulls/comments/COMMENT_ID/replies",
		"Do NOT post issue-level",
		"<user-provided-content>",
		"</user-provided-content>",
		"untrusted user input",
		"Do NOT commit",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildConflictResolutionPrompt(t *testing.T) {
	work := IssueWork{
		IssueNumber: 42,
		IssueTitle:  "Fix nil pointer in handler",
		PRNumber:    100,
	}

	prompt := buildConflictResolutionPrompt(work, "origin/main", "")

	checks := []string{
		"PR #100",
		"issue #42",
		"Fix nil pointer in handler",
		"merge conflicts",
		"git fetch origin",
		"git rebase origin/main",
		"git add",
		"git rebase --continue",
		"Do NOT run \"git rebase --abort\"",
		"Do NOT create new standalone commits",
		"original commit structure preserved",
		"Do NOT push",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	// With signed-off-by
	prompt = buildConflictResolutionPrompt(work, "origin/main", "Test User <test@example.com>")
	if !strings.Contains(prompt, "Signed-off-by: Test User <test@example.com>") {
		t.Error("prompt missing Signed-off-by when provided")
	}
}

func TestBuildCIFixPrompt(t *testing.T) {
	work := IssueWork{
		IssueNumber: 42,
		IssueTitle:  "Fix nil pointer in handler",
		PRNumber:    100,
	}

	failures := []CheckRun{
		{
			Name:       "lint",
			Conclusion: "failure",
			Output:     "golangci-lint found issues",
		},
	}

	commits := []Commit{
		{SHA: "abc123def456", Subject: "Fix handler"},
		{SHA: "def456abc789", Subject: "Add tests"},
	}

	diff := "handler.go | 10 +++++++---\n"

	// Test without signed-off-by
	prompt := buildCIFixPrompt(work, failures, diff, commits, "")

	checks := []string{
		"PR #100",
		"issue #42",
		"Fix nil pointer in handler",
		"Failed checks:",
		"lint",
		"golangci-lint found issues",
		"handler.go",
		"UNRELATED",
		"RELATED",
		"Do NOT push",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	// With signed-off-by (multi-commit PR should include instruction)
	prompt = buildCIFixPrompt(work, failures, diff, commits, "Test User <test@example.com>")
	if !strings.Contains(prompt, "Signed-off-by: Test User <test@example.com>") {
		t.Error("prompt missing Signed-off-by when provided for multi-commit PR")
	}

	// Single-commit PR should NOT include signed-off-by instruction (no new commits created)
	singleCommit := []Commit{
		{SHA: "abc123def456", Subject: "Fix handler"},
	}
	prompt = buildCIFixPrompt(work, failures, diff, singleCommit, "Test User <test@example.com>")
	if strings.Contains(prompt, "Signed-off-by:") {
		t.Error("prompt should NOT include Signed-off-by for single-commit PR (no new commits)")
	}
}

func TestBuildPeriodicCITriagePrompt(t *testing.T) {
	jobName := "periodic-knmstate-e2e-handler-k8s-latest"
	runID := "1234567890"
	buildLog := "=== RUN TestHandler\n--- FAIL: TestHandler (0.00s)\n    handler_test.go:42: unexpected nil pointer\nFAIL"
	owner := "nmstate"
	repo := "kubernetes-nmstate"

	prompt := buildPeriodicCITriagePrompt(jobName, runID, buildLog, owner, repo)

	checks := []string{
		jobName,
		runID,
		owner + "/" + repo,
		"investigating a periodic CI job failure",
		"<user-provided-content>",
		"</user-provided-content>",
		"untrusted CI output",
		buildLog,
		"CLAUDE.md",
		"FLAKY_TEST",
		"INFRASTRUCTURE",
		"CODE_BUG",
		"Summary",
		"Root Cause",
		"Classification",
		"Suggested Fix",
		"READ-ONLY investigation",
		"Do NOT modify any files",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	// Should NOT contain instructions to commit or push
	forbidden := []string{
		"git commit",
		"git push",
		"gh pr create",
	}
	for _, bad := range forbidden {
		if strings.Contains(prompt, bad) {
			t.Errorf("prompt should NOT contain %q (read-only investigation)", bad)
		}
	}
}
