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

	prompt := buildImplementationPrompt(issue, "", "")

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
	prompt = buildImplementationPrompt(issue, "Test User <test@example.com>", "")
	if !strings.Contains(prompt, "Signed-off-by: Test User <test@example.com>") {
		t.Error("prompt missing Signed-off-by when provided")
	}

	// With assisted-by
	prompt = buildImplementationPrompt(issue, "", "Claude <noreply@anthropic.com>")
	if !strings.Contains(prompt, "Assisted-by: Claude <noreply@anthropic.com>") {
		t.Error("prompt missing Assisted-by when provided")
	}

	// With both trailers
	prompt = buildImplementationPrompt(issue, "Test User <test@example.com>", "Claude <noreply@anthropic.com>")
	if !strings.Contains(prompt, "Signed-off-by: Test User <test@example.com>") {
		t.Error("prompt missing Signed-off-by when both trailers provided")
	}
	if !strings.Contains(prompt, "Assisted-by: Claude <noreply@anthropic.com>") {
		t.Error("prompt missing Assisted-by when both trailers provided")
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

	prompt := buildReviewResponsePrompt(work, comments, nil, nil, "owner", "repo", " handler.go | 5 ++---\n 1 file changed")

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
		"/ce-resolve-pr-feedback",
		"<user-provided-content>",
		"</user-provided-content>",
		"untrusted user input",
		"Do NOT commit",
		"leave them UNCOMMITTED",
		"Post per-comment replies",
		"Decline invalid suggestions",
		"Resolve addressed review threads",
		"skip step 7",
		"SCOPE CONSTRAINT",
		"Files changed in this PR",
		"handler.go | 5",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildReviewResponsePrompt_WithPRComments(t *testing.T) {
	work := IssueWork{
		IssueNumber: 42,
		IssueTitle:  "Fix nil pointer in handler",
		PRNumber:    100,
	}

	prComments := []ReviewComment{
		{ID: 10, User: "reviewer1", Body: "/oompa fix the commit message"},
		{ID: 11, User: "reviewer2", Body: "/oompa add Signed-off-by trailers"},
	}

	prompt := buildReviewResponsePrompt(work, nil, nil, prComments, "owner", "repo", "")

	checks := []string{
		"PR conversation directives",
		"Directive by reviewer1",
		"fix the commit message",
		"Directive by reviewer2",
		"add Signed-off-by trailers",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	// The /oompa prefix should be stripped from directives
	if strings.Contains(prompt, "/oompa fix") {
		t.Error("prompt should strip /oompa prefix from directives")
	}
	if strings.Contains(prompt, "/oompa add") {
		t.Error("prompt should strip /oompa prefix from directives")
	}
}

func TestBuildConflictResolutionPrompt(t *testing.T) {
	work := IssueWork{
		IssueNumber: 42,
		IssueTitle:  "Fix nil pointer in handler",
		PRNumber:    100,
	}

	prompt := buildConflictResolutionPrompt(work, "origin/main")

	checks := []string{
		"PR #100",
		"issue #42",
		"Fix nil pointer in handler",
		"merge conflicts",
		"git remote -v",
		"git fetch",
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

	prompt := buildCIFixPrompt(work, failures, diff, commits, false)

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
		"INFRASTRUCTURE",
		"Do NOT push",
		// Investigation methodology
		"/ce-debug",
		// Fix criteria
		"Fix the code so that CI passes",
		// INFRASTRUCTURE classification criteria
		"transient environment or infrastructure issue",
		"HTTP 502/503",
		"temporary outages that resolve themselves",
		// Structured output format
		"ERROR_SUMMARY:",
		"ROOT_CAUSE:",
		"EVIDENCE:",
		"RECOMMENDATION:",
		"FAILING_TEST:",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildPeriodicCITriagePrompt(t *testing.T) {
	jobName := "periodic-knmstate-e2e-handler-k8s-latest"
	runID := "1234567890"
	buildLog := "=== RUN TestHandler\n--- FAIL: TestHandler (0.00s)\n    handler_test.go:42: unexpected nil pointer\nFAIL"
	owner := "nmstate"
	repo := "kubernetes-nmstate"

	t.Run("schedule event omits PR context", func(t *testing.T) {
		prompt := buildPeriodicCITriagePrompt(jobName, runID, buildLog, owner, repo, "schedule", "main", "")

		checks := []string{
			jobName,
			runID,
			owner + "/" + repo,
			"investigating a CI job failure",
			"<user-provided-content>",
			"</user-provided-content>",
			"untrusted input",
			buildLog,
			"AGENTS.md",
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

		// Should NOT contain PR context for non-PR events
		if strings.Contains(prompt, "pull_request event") {
			t.Error("schedule event should not include PR context section")
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
	})

	t.Run("pull_request event includes PR context inside user-provided-content", func(t *testing.T) {
		headBranch := "fix-vm-image"
		displayTitle := "Replace fcos image with fedora"
		prompt := buildPeriodicCITriagePrompt(jobName, runID, buildLog, owner, repo, "pull_request", headBranch, displayTitle)

		checks := []string{
			"pull_request event",
			headBranch,
			displayTitle,
			"CAUSED by the PR changes",
			"CODE_BUG",
			"FLAKY_TEST",
		}

		for _, want := range checks {
			if !strings.Contains(prompt, want) {
				t.Errorf("prompt missing %q", want)
			}
		}

		// PR context must be inside <user-provided-content> boundary
		upcStart := strings.Index(prompt, "<user-provided-content>")
		upcEnd := strings.Index(prompt, "</user-provided-content>")
		prCtxIdx := strings.Index(prompt, "pull_request event")
		if prCtxIdx < upcStart || prCtxIdx > upcEnd {
			t.Error("PR context should be inside <user-provided-content> boundary")
		}
	})

	t.Run("pull_request event sanitizes tag-like characters", func(t *testing.T) {
		headBranch := "feat/<inject>"
		displayTitle := "PR <script>alert('xss')</script>"
		prompt := buildPeriodicCITriagePrompt(jobName, runID, buildLog, owner, repo, "pull_request", headBranch, displayTitle)

		// Sanitized versions should be present
		if !strings.Contains(prompt, "feat/&lt;inject&gt;") {
			t.Error("headBranch angle brackets should be escaped")
		}
		if !strings.Contains(prompt, "&lt;script&gt;") {
			t.Error("displayTitle angle brackets should be escaped")
		}

		// Raw angle brackets from user input should NOT be present
		if strings.Contains(prompt, "feat/<inject>") {
			t.Error("raw headBranch angle brackets should not appear in prompt")
		}
		if strings.Contains(prompt, "<script>") {
			t.Error("raw displayTitle angle brackets should not appear in prompt")
		}
	})
}

func TestBuildFlakyMatchPrompt_RootCauseInstructions(t *testing.T) {
	existingIssues := []Issue{
		{Number: 50, Title: "Flaky CI: Build-PR", Body: "koji 502 error"},
	}

	prompt := buildFlakyMatchPrompt("Build-PR", "HTTP 503 from koji.fedoraproject.org", existingIssues)

	checks := []string{
		"Build-PR",
		"HTTP 503 from koji.fedoraproject.org",
		"Issue #50",
		"Flaky CI: Build-PR",
		// Root-cause matching instructions
		"ROOT CAUSE",
		"not error message",
		"same underlying problem",
		"Different HTTP errors from the same server",
		"same cause",
		"Which component/service failed",
		"Do NOT require exact error message matches",
		"MATCH",
		"NONE",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildReviewResponsePrompt_CommitMessageInstructions(t *testing.T) {
	work := IssueWork{
		IssueNumber: 42,
		IssueTitle:  "Fix nil pointer in handler",
		PRNumber:    100,
	}

	prompt := buildReviewResponsePrompt(work, nil, nil, nil, "owner", "repo", "")

	checks := []string{
		".oompa-commit-msg",
		"commit message",
		"Do NOT run \"git commit --amend\"",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildReviewResponsePrompt_ScopeConstraint(t *testing.T) {
	work := IssueWork{
		IssueNumber: 42,
		IssueTitle:  "Fix nil pointer in handler",
		PRNumber:    100,
	}

	t.Run("with diff stat includes scope constraint and file list", func(t *testing.T) {
		diffStat := " pkg/agent/triage.go | 45 ++++++++++-\n pkg/agent/loop_test.go | 205 +++++++++\n 2 files changed, 246 insertions(+), 4 deletions(-)"
		prompt := buildReviewResponsePrompt(work, nil, nil, nil, "owner", "repo", diffStat)

		checks := []string{
			"SCOPE CONSTRAINT",
			"MUST only modify files",
			"Files changed in this PR",
			"triage.go",
			"loop_test.go",
		}
		for _, want := range checks {
			if !strings.Contains(prompt, want) {
				t.Errorf("prompt missing %q", want)
			}
		}
	})

	t.Run("without diff stat omits both file list and scope constraint", func(t *testing.T) {
		prompt := buildReviewResponsePrompt(work, nil, nil, nil, "owner", "repo", "")

		if strings.Contains(prompt, "Files changed in this PR") {
			t.Error("prompt should not include file list section when diff stat is empty")
		}
		if strings.Contains(prompt, "SCOPE CONSTRAINT") {
			t.Error("prompt should not include scope constraint when diff stat is empty")
		}
	})
}

func TestBuildTriageMatchPrompt(t *testing.T) {
	existingIssues := []Issue{
		{Number: 1501, Title: "CI Failure: periodic-e2e / VLAN test timeout",
			Body: "VLAN configuration test failed with timeout."},
	}

	prompt := buildTriageMatchPrompt("periodic-e2e", "CRI-O mirror HTTP 404 error", existingIssues, nil)

	checks := []string{
		"periodic-e2e",
		"CRI-O mirror HTTP 404 error",
		"Issue #1501",
		"CI Failure: periodic-e2e / VLAN test timeout",
		"ROOT CAUSE",
		"not error message",
		"same underlying problem",
		"MATCH",
		"NONE",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	// Without concurrent failures, the prompt should NOT contain cycle context
	if strings.Contains(prompt, "Concurrent failure context") {
		t.Error("prompt should NOT contain concurrent failure context when cycleFailedJobs is nil")
	}
}

func TestBuildTriageMatchPrompt_ConcurrentFailures(t *testing.T) {
	existingIssues := []Issue{
		{Number: 2761, Title: "CI Failure: kubevirt-ipam-controller / setup timeout",
			Body: "Setup phase timed out waiting for cluster."},
	}

	cycleFailedJobs := []string{
		"kubevirt-ipam-controller",
		"workflow-k8s-s390x",
		"unit-test-release-0.102",
		"monitoring-k8s",
	}

	prompt := buildTriageMatchPrompt("workflow-k8s-s390x", "cluster setup failed", existingIssues, cycleFailedJobs)

	checks := []string{
		// Concurrent failure context section
		"Concurrent failure context",
		"4 CI jobs failed concurrently",
		"kubevirt-ipam-controller",
		"workflow-k8s-s390x",
		"unit-test-release-0.102",
		"monitoring-k8s",
		"STRONG signal",
		"common root cause",
		// Instructions about concurrent failures
		"Multiple CI jobs failed concurrently",
		"Bias toward MATCH",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildTriageMatchPrompt_SingleJobNoConcurrentContext(t *testing.T) {
	existingIssues := []Issue{
		{Number: 100, Title: "CI Failure: unit-test / nil pointer",
			Body: "nil pointer dereference in handler.go"},
	}

	// A single failing job should not produce concurrent context
	cycleFailedJobs := []string{"unit-test"}

	prompt := buildTriageMatchPrompt("unit-test", "nil pointer in handler", existingIssues, cycleFailedJobs)

	if strings.Contains(prompt, "Concurrent failure context") {
		t.Error("prompt should NOT contain concurrent failure context when only 1 job failed")
	}
}
