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

	prompt := buildImplementationPrompt(issue, "", "owner", "repo")

	checks := []string{
		"#42",
		"Fix nil pointer in handler",
		"The handler crashes when input is nil.",
		"/kind",
		"Fixes #42",
		"release-note",
		"<user-provided-content>",
		"</user-provided-content>",
		"untrusted user input",
		"--repo owner/repo",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	// With signed-off-by
	prompt = buildImplementationPrompt(issue, "Test User <test@example.com>", "owner", "repo")
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

	prompt := buildReviewResponsePrompt(work, comments, nil, "", "owner", "repo")

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
		"<user-provided-content>",
		"</user-provided-content>",
		"untrusted user input",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
