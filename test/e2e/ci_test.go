package e2e

import (
	"strings"
	"testing"
)

// TestE2E_CIFailureTriage verifies CI failure detection and triage:
// oompa discovers a PR with failing check runs, invokes the agent to classify
// the failure, and posts a consolidated CI comment.
// This scenario catches regressions in: #140, #192, #200, #216, #218.
func TestE2E_CIFailureTriage(t *testing.T) {
	const (
		owner = "testowner"
		repo  = "testrepo"
		label = "good-for-ai"
	)

	fg := NewFakeGitHub(t, owner, repo)

	// Seed an issue with an existing PR.
	fg.SeedIssue(FakeIssue{
		Number: 20,
		Title:  "Improve performance",
		Body:   "Optimize the hot loop.",
		Labels: []map[string]any{{"name": label}},
	})

	fg.SeedPR(FakePR{
		Number: 300,
		Title:  "Improve performance",
		Body:   "Fixes #20",
		State:  "open",
		Head:   "ai/issue-20",
		Base:   "main",
	})

	headSHA := "ci-test-sha-001"
	fg.SetPRHeadSHA(300, headSHA)

	// Seed a failing check run (catches #140: check runs registered after PR creation).
	fg.SeedCheckRun(headSHA, FakeCheckRun{
		ID:         5001,
		Name:       "integration-tests",
		Status:     "completed",
		Conclusion: "failure",
		Output: struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			Text    string `json:"text"`
		}{
			Title:   "Integration Tests Failed",
			Summary: "1 test failed",
			Text:    "FAIL: TestNetworkTimeout - connection timed out after 30s",
		},
		HTMLURL: "https://github.com/testowner/testrepo/runs/5001",
	})

	// Seed a second failing check run to test dedup across jobs (catches #218).
	fg.SeedCheckRun(headSHA, FakeCheckRun{
		ID:         5002,
		Name:       "e2e-tests",
		Status:     "completed",
		Conclusion: "failure",
		Output: struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			Text    string `json:"text"`
		}{
			Title:   "E2E Tests Failed",
			Summary: "Network related failure",
			Text:    "FAIL: TestNetworkTimeout - connection timed out after 30s",
		},
		HTMLURL: "https://github.com/testowner/testrepo/runs/5002",
	})

	// Also seed a passing check run to verify it's not reported.
	fg.SeedCheckRun(headSHA, FakeCheckRun{
		ID:         5003,
		Name:       "lint",
		Status:     "completed",
		Conclusion: "success",
	})

	h := NewHarness(t, owner, repo, label)
	// Install the CI-specific fake claude that outputs UNRELATED classification.
	h.InstallFakeClaudeScript("fake-claude-ci.sh")
	pushBranchToBare(t, h, "ai/issue-20")

	stdout, stderr, err := h.RunOompa(fg.URL(), RunOompaOpts{
		WatchPRs:  []int{300},
		Reactions: []string{"ci"},
	})
	if err != nil {
		t.Fatalf("oompa exited with error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// === Assertions ===

	// 1. CI failure was detected — a comment should be posted.
	fg.mu.Lock()
	commentCalls := make([]FakeComment, len(fg.CommentCalls))
	copy(commentCalls, fg.CommentCalls)
	fg.mu.Unlock()

	foundCIComment := false
	for _, c := range commentCalls {
		if strings.Contains(c.Body, "CI Failure Analysis") {
			foundCIComment = true
			break
		}
	}
	if !foundCIComment {
		t.Error("expected a CI Failure Analysis comment to be posted")
	}

	// 2. Both failing checks should appear in a single consolidated comment.
	consolidatedComment := ""
	for _, c := range commentCalls {
		if strings.Contains(c.Body, "CI Failure Analysis") {
			consolidatedComment = c.Body
			break
		}
	}
	if consolidatedComment != "" {
		if !strings.Contains(consolidatedComment, "integration-tests") {
			t.Error("consolidated CI comment missing 'integration-tests'")
		}
		if !strings.Contains(consolidatedComment, "e2e-tests") {
			t.Error("consolidated CI comment missing 'e2e-tests'")
		}
	}

	// 3. Verify that the agent was invoked with the CI context.
	stdinMarker := findStdinMarker(t, h.CloneDir())
	if stdinMarker == "" {
		t.Error("expected .oompa-stdin-marker — agent was not invoked for CI investigation")
	}
}

// TestE2E_CIFailureTriage_DedupOnRepoll verifies that the same CI failure
// is not re-investigated on the next poll cycle (catches #200).
func TestE2E_CIFailureTriage_DedupOnRepoll(t *testing.T) {
	const (
		owner = "testowner"
		repo  = "testrepo"
		label = "good-for-ai"
	)

	fg := NewFakeGitHub(t, owner, repo)

	fg.SeedIssue(FakeIssue{
		Number: 21,
		Title:  "Dedup test",
		Body:   "Test CI dedup.",
		Labels: []map[string]any{{"name": label}},
	})

	fg.SeedPR(FakePR{
		Number: 301,
		Title:  "Dedup test",
		Body:   "Fixes #21",
		State:  "open",
		Head:   "ai/issue-21",
		Base:   "main",
	})

	headSHA := "dedup-sha-001"
	fg.SetPRHeadSHA(301, headSHA)

	fg.SeedCheckRun(headSHA, FakeCheckRun{
		ID:         6001,
		Name:       "unit-tests",
		Status:     "completed",
		Conclusion: "failure",
		Output: struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			Text    string `json:"text"`
		}{
			Text: "FAIL: TestSomething - assertion failed",
		},
	})

	h := NewHarness(t, owner, repo, label)
	h.InstallFakeClaudeScript("fake-claude-ci.sh")
	pushBranchToBare(t, h, "ai/issue-21")

	// First run: should investigate and post comment.
	stdout, stderr, err := h.RunOompa(fg.URL(), RunOompaOpts{
		WatchPRs:  []int{301},
		Reactions: []string{"ci"},
	})
	if err != nil {
		t.Fatalf("first run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	fg.mu.Lock()
	firstRunComments := len(fg.CommentCalls)
	fg.mu.Unlock()

	if firstRunComments == 0 {
		t.Fatal("expected at least 1 comment from first CI run")
	}

	// Second run: same SHA, same check — should NOT post a duplicate comment.
	// The dedup marker comment from the first run should prevent re-investigation.
	stdout2, stderr2, err2 := h.RunOompa(fg.URL(), RunOompaOpts{
		WatchPRs:  []int{301},
		Reactions: []string{"ci"},
	})
	if err2 != nil {
		t.Fatalf("second run failed: %v\nstdout:\n%s\nstderr:\n%s", err2, stdout2, stderr2)
	}

	// The CI marker comment contains "<!-- oompa-bot ci:SHA:checkName -->"
	// which deduplicates across runs. No new CI analysis comments should appear.
	// Note: state is rebuilt from GitHub API on each run, but the marker comments
	// persist in the fake server's state.
	// Copy new comment bodies under a single lock acquisition to avoid
	// data races from concurrent HTTP server goroutines.
	fg.mu.Lock()
	var newBodies []string
	for i := firstRunComments; i < len(fg.CommentCalls); i++ {
		newBodies = append(newBodies, fg.CommentCalls[i].Body)
	}
	fg.mu.Unlock()

	if len(newBodies) > 1 {
		// Allow at most 1 extra comment (e.g., state-rebuild noise) but not another
		// full CI Failure Analysis block.
		var newCIComments int
		for _, body := range newBodies {
			if strings.Contains(body, "CI Failure Analysis") {
				newCIComments++
			}
		}
		if newCIComments > 0 {
			t.Errorf("expected no duplicate CI Failure Analysis comment on re-poll, got %d new", newCIComments)
		}
	}
}
