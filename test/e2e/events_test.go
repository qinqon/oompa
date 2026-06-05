package e2e

import (
	"strings"
	"testing"
)

// TestE2E_EventCategories is an end-to-end smoke test for issue discovery and
// processing. It verifies that oompa successfully scans for issues, invokes the
// agent, and creates a PR — exercising the full event emission pipeline.
// The event server emits category-tagged events internally; this test validates
// the observable outcomes (PR creation, log entries) rather than inspecting
// the event stream directly, since the Unix socket is unavailable after the
// subprocess exits.
func TestE2E_EventCategories(t *testing.T) {
	const (
		owner = "testowner"
		repo  = "testrepo"
		label = "good-for-ai"
	)

	fg := NewFakeGitHub(t, owner, repo)

	// Seed an issue so oompa has something to discover and process.
	fg.SeedIssue(FakeIssue{
		Number: 50,
		Title:  "Event category test",
		Body:   "Test event categories.",
		Labels: []map[string]any{{"name": label}},
	})

	h := NewHarness(t, owner, repo, label)

	stdout, stderr, err := h.RunOompa(fg.URL())
	if err != nil {
		t.Fatalf("oompa exited with error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// Verify oompa processes the issue correctly. The main assertions are
	// that issue scanning and processing work end-to-end with events being
	// emitted (verified by the fact that the PR is created successfully).

	// Verify the issue was processed (PR created).
	fg.mu.Lock()
	prCalls := fg.CreatePRCalls
	fg.mu.Unlock()

	if len(prCalls) != 1 {
		t.Fatalf("expected exactly 1 CreatePR call, got %d\nstderr:\n%s", len(prCalls), stderr)
	}

	// Verify through logs that issue processing occurred.
	if !strings.Contains(stderr, "processing new issue") {
		t.Error("expected 'processing new issue' log entry in debug output")
	}
	if !strings.Contains(stderr, "created PR") {
		t.Error("expected 'created PR' log entry in debug output")
	}
}

// TestE2E_EventCategories_WatchPRs verifies that --watch-prs mode correctly
// bootstraps tracked PRs and runs the poll cycle. This is a smoke test for
// the watch-prs bootstrapping path — it checks that the PR is discovered and
// the review check runs without errors, validating the observable log output.
func TestE2E_EventCategories_WatchPRs(t *testing.T) {
	const (
		owner = "testowner"
		repo  = "testrepo"
		label = "good-for-ai"
	)

	fg := NewFakeGitHub(t, owner, repo)

	// Seed an existing PR to watch.
	fg.SeedIssue(FakeIssue{
		Number: 51,
		Title:  "PR watch event test",
		Body:   "Test events in watch mode.",
		Labels: []map[string]any{{"name": label}},
	})

	fg.SeedPR(FakePR{
		Number: 500,
		Title:  "PR watch event test",
		Body:   "Fixes #51",
		State:  "open",
		Head:   "ai/issue-51",
		Base:   "main",
	})
	fg.SetPRHeadSHA(500, "event-test-sha-001")

	h := NewHarness(t, owner, repo, label)
	pushBranchToBare(t, h, "ai/issue-51")

	// Run in watch mode with reviews — should emit "Checking for review comments".
	stdout, stderr, err := h.RunOompa(fg.URL(), RunOompaOpts{
		WatchPRs:  []int{500},
		Reactions: []string{"reviews"},
	})
	if err != nil {
		t.Fatalf("oompa exited with error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// Verify oompa ran in watch-prs mode. The bootstrapping should log the PR.
	if !strings.Contains(stderr, "recovered watched PR state") &&
		!strings.Contains(stderr, "bootstrapped watched PR") {
		t.Error("expected PR bootstrap log message in debug output")
	}
}
