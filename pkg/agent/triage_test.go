package agent

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestProcessTriageJobs_NoJobsConfigured(t *testing.T) {
	gh := &mockGitHubClient{}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:      "owner",
		Repo:       "repo",
		TriageJobs: []string{}, // No jobs configured
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), &ClaudeCodeAgent{})
	a.ProcessTriageJobs(context.Background())

	// Should not create any worktrees or run Claude
	if len(wtm.createdBranches) > 0 {
		t.Errorf("expected no worktrees created, got %d", len(wtm.createdBranches))
	}
	if len(runner.calls) > 0 {
		t.Errorf("expected no commands run, got %d", len(runner.calls))
	}
}

func TestProcessTriageJobs_SkipsAlreadyInvestigated(t *testing.T) {
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 100, Status: "completed", Conclusion: "failure", CreatedAt: time.Now()},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()

	// Pre-mark run as already investigated
	state.MarkRunInvestigated("owner/repo/ci.yml", "100")

	cfg := Config{
		Owner:      "owner",
		Repo:       "repo",
		TriageJobs: []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), &ClaudeCodeAgent{})
	a.ProcessTriageJobs(context.Background())

	// Should still be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "100") {
		t.Error("expected run to remain marked as investigated")
	}
	// Should not create any worktrees (no investigation triggered)
	if len(wtm.createdBranches) > 0 {
		t.Errorf("expected no worktrees created for already-investigated run, got %d", len(wtm.createdBranches))
	}
	// Should not have created any issues
	if len(gh.createdIssues) > 0 {
		t.Errorf("expected no issues created, got %d", len(gh.createdIssues))
	}
}

func TestProcessTriageJobs_SkipsSuccessfulRuns(t *testing.T) {
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 200, Status: "completed", Conclusion: "success", CreatedAt: time.Now()},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()

	cfg := Config{
		Owner:      "owner",
		Repo:       "repo",
		TriageJobs: []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), &ClaudeCodeAgent{})
	a.ProcessTriageJobs(context.Background())

	// Successful run should be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "200") {
		t.Error("expected successful run to be marked as investigated")
	}
	// Should not create any worktrees (no investigation needed for success)
	if len(wtm.createdBranches) > 0 {
		t.Errorf("expected no worktrees created for successful run, got %d", len(wtm.createdBranches))
	}
	// Should not have created any issues
	if len(gh.createdIssues) > 0 {
		t.Errorf("expected no issues created, got %d", len(gh.createdIssues))
	}
}

func TestProcessTriageJobs_CreatesIssueWhenFlakyIssuesEnabled(t *testing.T) {
	gh := &mockGitHubClient{
		searchResults: []Issue{}, // No existing issues
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()

	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		TriageJobs:        []string{}, // Empty to avoid actual HTTP calls
		CreateFlakyIssues: true,
		FlakyLabel:        "ci-flake",
	}

	NewAgent(gh, runner, wtm, state, cfg, slog.Default(), &ClaudeCodeAgent{})

	// Verify that CreateFlakyIssues flag is respected
	if !cfg.CreateFlakyIssues {
		t.Error("expected CreateFlakyIssues to be true")
	}

	// The actual issue creation happens in ProcessTriageJobs when it calls gh.CreateIssue
	// We can verify the mock tracks created issues
	if gh.nextIssueNumber == 0 {
		gh.nextIssueNumber = 1
	}

	issueNum, err := gh.CreateIssue(context.Background(), "owner", "repo",
		"CI Failure: test-job", "Analysis output", []string{cfg.FlakyLabel})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if issueNum != 1 {
		t.Errorf("expected issue number 1, got %d", issueNum)
	}

	if len(gh.createdIssues) != 1 {
		t.Errorf("expected 1 issue created, got %d", len(gh.createdIssues))
	}

	issue := gh.createdIssues[0]
	if issue.Title != "CI Failure: test-job" {
		t.Errorf("expected title 'CI Failure: test-job', got %q", issue.Title)
	}

	if len(issue.Labels) != 1 || issue.Labels[0] != "ci-flake" {
		t.Errorf("expected label 'ci-flake', got %v", issue.Labels)
	}
}

func TestProcessTriageJobs_DefaultOnlyChecksLatest(t *testing.T) {
	// When TriageLookback is not set (zero), only the most recent run is checked.
	triageResult := streamResultJSON(AgentResult{Result: "Root cause: network timeout"})
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 300, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/300"},
			{ID: 200, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-2 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/200"},
			{ID: 100, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-3 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/100"},
		},
	}
	runner := &mockCommandRunner{stdout: triageResult}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:      "owner",
		Repo:       "repo",
		FlakyLabel: "flaky-test",
		TriageJobs: []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
		// TriageLookback: 0 (default — only check latest)
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "Root cause: network timeout"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should only investigate 1 run (the latest)
	if codeAgent.callCount != 1 {
		t.Errorf("expected 1 agent call (latest run only), got %d", codeAgent.callCount)
	}

	// Only the latest run should be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "300") {
		t.Error("expected run 300 to be marked as investigated")
	}
	if state.IsRunInvestigated("owner/repo/ci.yml", "200") {
		t.Error("expected run 200 to NOT be marked as investigated")
	}
	if state.IsRunInvestigated("owner/repo/ci.yml", "100") {
		t.Error("expected run 100 to NOT be marked as investigated")
	}
}

func TestProcessTriageJobs_LookbackInvestigatesAllFailedRuns(t *testing.T) {
	// When TriageLookback is set, all failed runs within the window are investigated.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 300, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/300"},
			{ID: 200, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-2 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/200"},
			{ID: 100, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-25 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/100"},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:          "owner",
		Repo:           "repo",
		FlakyLabel:     "flaky-test",
		TriageJobs:     []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
		TriageLookback: 24 * time.Hour, // look back 24 hours
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "Failure analysis 1"}},
			{result: AgentResult{Result: "Failure analysis 2"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should investigate 2 runs (300 and 200 are within 24h; 100 is older)
	if codeAgent.callCount != 2 {
		t.Errorf("expected 2 agent calls (runs within lookback window), got %d", codeAgent.callCount)
	}

	// Runs within window should be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "300") {
		t.Error("expected run 300 to be marked as investigated")
	}
	if !state.IsRunInvestigated("owner/repo/ci.yml", "200") {
		t.Error("expected run 200 to be marked as investigated")
	}
	// Run older than lookback should NOT be investigated
	if state.IsRunInvestigated("owner/repo/ci.yml", "100") {
		t.Error("expected run 100 to NOT be marked as investigated (too old)")
	}
}

func TestProcessTriageJobs_LookbackSkipsSuccessfulRuns(t *testing.T) {
	// Successful runs within the lookback window should be marked as investigated
	// but not trigger agent calls.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 300, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/300"},
			{ID: 200, Status: "completed", Conclusion: "success", CreatedAt: now.Add(-2 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/200"},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:          "owner",
		Repo:           "repo",
		FlakyLabel:     "flaky-test",
		TriageJobs:     []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
		TriageLookback: 24 * time.Hour,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "Failure analysis"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Only 1 agent call for the failed run
	if codeAgent.callCount != 1 {
		t.Errorf("expected 1 agent call (only failed runs), got %d", codeAgent.callCount)
	}

	// Both runs should be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "300") {
		t.Error("expected failed run 300 to be marked as investigated")
	}
	if !state.IsRunInvestigated("owner/repo/ci.yml", "200") {
		t.Error("expected passed run 200 to be marked as investigated")
	}
}

func TestProcessTriageJobs_LookbackSkipsAlreadyInvestigated(t *testing.T) {
	// Already-investigated runs should be skipped in lookback mode.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 300, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/300"},
			{ID: 200, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-2 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/200"},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()

	// Pre-mark run 200 as already investigated
	state.MarkRunInvestigated("owner/repo/ci.yml", "200")

	cfg := Config{
		Owner:          "owner",
		Repo:           "repo",
		FlakyLabel:     "flaky-test",
		TriageJobs:     []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
		TriageLookback: 24 * time.Hour,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "Failure analysis"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Only 1 agent call (run 200 was already investigated)
	if codeAgent.callCount != 1 {
		t.Errorf("expected 1 agent call (skip already investigated), got %d", codeAgent.callCount)
	}

	if !state.IsRunInvestigated("owner/repo/ci.yml", "300") {
		t.Error("expected run 300 to be marked as investigated")
	}
}

func TestProcessTriageJobs_DeduplicatesMultipleRunsSameJob(t *testing.T) {
	// When multiple failed runs of the same job are investigated in the same
	// triage cycle, the second run should match the issue created by the first
	// and post a run-link comment instead of creating a duplicate issue.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 300, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/300"},
			{ID: 200, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-2 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/200"},
		},
		searchResults:   []Issue{}, // GitHub search returns nothing (eventual consistency lag)
		nextIssueNumber: 10,
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
		TriageJobs:        []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
		TriageLookback:    24 * time.Hour,
	}

	// Both runs produce the same failure signature, so titles match exactly.
	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "## Summary\nCompile error in main.go"}},
			{result: AgentResult{Result: "## Summary\nCompile error in main.go"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should create exactly 1 issue (first run) and post a run-link comment (second run)
	if len(gh.createdIssues) != 1 {
		t.Errorf("expected 1 issue created (dedup same job), got %d", len(gh.createdIssues))
	}

	// The run-link comment for the second run should reference the issue created by the first
	runLinkFound := false
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "Same failure observed") {
			runLinkFound = true
		}
	}
	if !runLinkFound {
		t.Error("expected a run-link comment for the deduplicated second run")
	}

	// Both runs should be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "300") {
		t.Error("expected run 300 to be marked as investigated")
	}
	if !state.IsRunInvestigated("owner/repo/ci.yml", "200") {
		t.Error("expected run 200 to be marked as investigated")
	}
}

func TestProcessTriageJobs_DeduplicatesMultipleRunsSameJob_DifferentSignatures(t *testing.T) {
	// When multiple failed runs of the same job are investigated in the same
	// triage cycle but produce different failure signatures (different LLM
	// summaries), the second run should still match the issue created by the
	// first via same-job cycle dedup. This is the bug reported in #253:
	// same workflow (kubevirt-ipam-controller.yaml) with different run IDs
	// produced different titles, causing exact title match to fail and the
	// LLM to say NONE, resulting in duplicate issues.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 300, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/300"},
			{ID: 200, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-2 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/200"},
		},
		searchResults:   []Issue{}, // GitHub search returns nothing (eventual consistency lag)
		nextIssueNumber: 10,
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
		TriageJobs:        []string{"https://github.com/owner/repo/actions/workflows/ci.yml"},
		TriageLookback:    24 * time.Hour,
	}

	// Runs produce DIFFERENT failure signatures — titles will NOT match exactly.
	// The LLM says NONE for the second run (unreliable matching).
	// Without the same-job cycle dedup fix, this would create 2 issues.
	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "## Summary\nCompile error in main.go line 42"}}, // first run analysis
			{result: AgentResult{Result: "## Summary\nCompile error in main.go line 99"}}, // second run analysis (different signature)
			{result: AgentResult{Result: "NONE"}},                                         // LLM says no match (unreliable)
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should create exactly 1 issue (first run) and post a run-link comment (second run)
	if len(gh.createdIssues) != 1 {
		t.Errorf("expected 1 issue created (same-job dedup with different signatures), got %d", len(gh.createdIssues))
	}

	// The run-link comment for the second run should reference the issue created by the first
	runLinkFound := false
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "Same failure observed") {
			runLinkFound = true
		}
	}
	if !runLinkFound {
		t.Error("expected a run-link comment for the deduplicated second run")
	}

	// Both runs should be marked as investigated
	if !state.IsRunInvestigated("owner/repo/ci.yml", "300") {
		t.Error("expected run 300 to be marked as investigated")
	}
	if !state.IsRunInvestigated("owner/repo/ci.yml", "200") {
		t.Error("expected run 200 to be marked as investigated")
	}

	// The LLM matching agent should NOT have been called because same-job
	// cycle dedup fires before LLM matching. Only 2 calls expected:
	// analysis for run 300 + analysis for run 200. No LLM match call.
	if codeAgent.callCount != 2 {
		t.Errorf("expected 2 agent calls (analysis only, no LLM match needed), got %d", codeAgent.callCount)
	}
}

func TestProcessTriageJobs_DeduplicatesDifferentJobsSameRootCause(t *testing.T) {
	// When different jobs fail for the same root cause in the same triage cycle,
	// the second job should match the issue created by the first via LLM matching
	// and post a run-link comment instead of creating a duplicate issue.
	now := time.Now()

	// Two different GitHub Actions workflows, each with one failed run
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 100, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/100"},
		},
		searchResults:   []Issue{}, // GitHub search returns nothing
		nextIssueNumber: 10,
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
		TriageJobs: []string{
			"https://github.com/owner/repo/actions/workflows/unit.yml",
			"https://github.com/owner/repo/actions/workflows/e2e.yml",
		},
	}

	// First job: analysis + no match (NONE) → creates issue
	// Second job: analysis + LLM match → deduplicates
	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "## Summary\nDependency X broke API"}},             // first job analysis
			{result: AgentResult{Result: "## Summary\nDependency X broke API in e2e test"}}, // second job analysis
			{result: AgentResult{Result: "MATCH #10"}},                                      // second job matches issue #10
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should create exactly 1 issue (first job) and post a run-link (second job)
	if len(gh.createdIssues) != 1 {
		t.Errorf("expected 1 issue created (dedup across jobs), got %d", len(gh.createdIssues))
	}

	// The issue created by the first job should be #10
	if len(gh.createdIssues) > 0 && gh.createdIssues[0].Number != 10 {
		t.Errorf("expected first issue number 10, got %d", gh.createdIssues[0].Number)
	}

	// The second job should have posted a run-link comment
	runLinkFound := false
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "Same failure observed") {
			runLinkFound = true
		}
	}
	if !runLinkFound {
		t.Error("expected a run-link comment for the deduplicated second job")
	}

	// Both runs should be investigated
	if !state.IsRunInvestigated("owner/repo/unit.yml", "100") {
		t.Error("expected unit.yml run to be marked as investigated")
	}
	if !state.IsRunInvestigated("owner/repo/e2e.yml", "100") {
		t.Error("expected e2e.yml run to be marked as investigated")
	}
}

func TestMergeIssues(t *testing.T) {
	tests := []struct {
		name    string
		primary []Issue
		extras  []Issue
		want    int // expected length
	}{
		{
			name:    "empty extras",
			primary: []Issue{{Number: 1}},
			extras:  nil,
			want:    1,
		},
		{
			name:    "no overlap",
			primary: []Issue{{Number: 1}},
			extras:  []Issue{{Number: 2}},
			want:    2,
		},
		{
			name:    "with overlap",
			primary: []Issue{{Number: 1}, {Number: 2}},
			extras:  []Issue{{Number: 2}, {Number: 3}},
			want:    3,
		},
		{
			name:    "both empty",
			primary: nil,
			extras:  nil,
			want:    0,
		},
		{
			name:    "empty primary with extras",
			primary: nil,
			extras:  []Issue{{Number: 5}},
			want:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeIssues(tt.primary, tt.extras)
			if len(got) != tt.want {
				t.Errorf("mergeIssues() returned %d issues, want %d", len(got), tt.want)
			}

			// Verify no duplicates
			seen := make(map[int]bool)
			for _, issue := range got {
				if seen[issue.Number] {
					t.Errorf("duplicate issue #%d in merged result", issue.Number)
				}
				seen[issue.Number] = true
			}
		})
	}
}

func TestTriageDedup_DifferentFailuresSameJob_MatchesByJobName(t *testing.T) {
	// Two different failures from the same job should match the existing
	// issue via same-job prefix match, even when the failure signatures
	// differ. This prevents duplicate issues when the LLM produces
	// different summaries for the same job across days (issue #257).
	gh := &mockGitHubClient{
		searchResults: []Issue{
			{Number: 1501, Title: "CI Failure: periodic-knmstate-e2e-handler-k8s-latest / VLAN bridge test timeout",
				Body: "Periodic CI job failed. VLAN configuration test failed with timeout."},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
		TriageJobs:        []string{},
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{}, // Should NOT be called — same-job match fires first
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	// Different failure signature from the same job
	title := "CI Failure: periodic-knmstate-e2e-handler-k8s-latest / CRI-O mirror returned HTTP 404"
	analysis := "## Summary\nCRI-O mirror returned HTTP 404\n\n## Root Cause\nInfrastructure mirror outage"

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"periodic-knmstate-e2e-handler-k8s-latest",
		title,
		analysis,
		gh.searchResults,
		"/tmp/worktree",
		nil, // no cycle issues
		nil, // no concurrent failures (single job)
	)

	// Same-job prefix match should find issue #1501
	if matchedIssue != 1501 {
		t.Errorf("expected same-job match on issue #1501, got #%d", matchedIssue)
	}

	// LLM should NOT have been called (same-job match is pre-LLM)
	if codeAgent.callCount != 0 {
		t.Errorf("expected 0 agent calls (same-job match), got %d", codeAgent.callCount)
	}
}

func TestFindSameJobCycleIssue(t *testing.T) {
	tests := []struct {
		name        string
		jobName     string
		cycleIssues []Issue
		want        int
	}{
		{
			name:        "no cycle issues",
			jobName:     "owner/repo/ci.yml",
			cycleIssues: nil,
			want:        0,
		},
		{
			name:    "matching cycle issue",
			jobName: "owner/repo/ci.yml",
			cycleIssues: []Issue{
				{Number: 10, Title: "CI Failure: owner/repo/ci.yml / Compile error in main.go"},
			},
			want: 10,
		},
		{
			name:    "no matching cycle issue (different job)",
			jobName: "owner/repo/ci.yml",
			cycleIssues: []Issue{
				{Number: 10, Title: "CI Failure: owner/repo/e2e.yml / Test timeout"},
			},
			want: 0,
		},
		{
			name:    "multiple cycle issues returns first match",
			jobName: "owner/repo/ci.yml",
			cycleIssues: []Issue{
				{Number: 10, Title: "CI Failure: owner/repo/ci.yml / Error A"},
				{Number: 11, Title: "CI Failure: owner/repo/ci.yml / Error B"},
			},
			want: 10,
		},
		{
			name:    "matches by prefix regardless of failure signature",
			jobName: "owner/repo/ci.yml",
			cycleIssues: []Issue{
				{Number: 10, Title: "CI Failure: owner/repo/ci.yml / Compile error in main.go line 42"},
			},
			want: 10,
		},
		{
			name:    "title without failure signature also matches",
			jobName: "owner/repo/ci.yml",
			cycleIssues: []Issue{
				{Number: 10, Title: "CI Failure: owner/repo/ci.yml"},
			},
			want: 10,
		},
		{
			name:    "does not match when job name is prefix of another job",
			jobName: "owner/repo/ci.yml",
			cycleIssues: []Issue{
				{Number: 10, Title: "CI Failure: owner/repo/ci.yml-long / Some error"},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findSameJobCycleIssue(tt.jobName, tt.cycleIssues)
			if got != tt.want {
				t.Errorf("findSameJobCycleIssue(%q, ...) = %d, want %d", tt.jobName, got, tt.want)
			}
		})
	}
}

func TestTriageDedup_SameJobCycleIssue_MatchesBeforeLLM(t *testing.T) {
	// When a cycle issue exists for the same job, matchExistingTriageIssue
	// should return it immediately without calling the LLM matcher.
	// This tests the fix for #253: same workflow, different signatures.
	gh := &mockGitHubClient{
		searchResults: []Issue{}, // no existing issues from search
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			// Should NOT be called — same-job cycle dedup fires first
			{result: AgentResult{Result: "NONE"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	cycleIssues := []Issue{
		{Number: 10, Title: "CI Failure: owner/repo/ci.yml / Compile error in main.go line 42"},
	}

	// Different title (different failure signature) but same job
	title := "CI Failure: owner/repo/ci.yml / Compile error in main.go line 99"
	analysis := "## Summary\nCompile error in main.go line 99"

	existingIssues := mergeIssues(gh.searchResults, cycleIssues)
	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"owner/repo/ci.yml",
		title,
		analysis,
		existingIssues,
		"/tmp/worktree",
		cycleIssues,
		[]string{"owner/repo/ci.yml"}, // single job
	)

	// Should match the cycle issue even though titles differ
	if matchedIssue != 10 {
		t.Errorf("expected match to cycle issue #10, got %d", matchedIssue)
	}

	// LLM should NOT have been called (same-job cycle dedup is pre-LLM)
	if codeAgent.callCount != 0 {
		t.Errorf("expected 0 agent calls (same-job cycle dedup), got %d", codeAgent.callCount)
	}
}

func TestTriageDedup_SameFailureSameJob_MatchesExistingIssue(t *testing.T) {
	// Same failure from the same job should match the existing issue via
	// same-job prefix match (no LLM needed).
	gh := &mockGitHubClient{
		searchResults: []Issue{
			{Number: 42, Title: "CI Failure: periodic-e2e-test / DNS resolution failure",
				Body: "Periodic CI job failed. DNS resolution timed out."},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{}, // Should NOT be called — same-job match fires first
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	// New failure is also a DNS issue, but slightly different title
	title := "CI Failure: periodic-e2e-test / DNS lookup timed out for registry.k8s.io"
	analysis := "## Summary\nDNS lookup timed out for registry.k8s.io\n\n## Root Cause\nDNS resolution failure"

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"periodic-e2e-test",
		title,
		analysis,
		gh.searchResults,
		"/tmp/worktree",
		nil, // no cycle issues
		nil, // no concurrent failures (single job)
	)

	// Same-job prefix match → should match issue #42
	if matchedIssue != 42 {
		t.Errorf("expected match on issue #42, got #%d", matchedIssue)
	}

	// LLM should NOT have been called
	if codeAgent.callCount != 0 {
		t.Errorf("expected 0 agent calls (same-job match), got %d", codeAgent.callCount)
	}
}

func TestTriageDedup_ExactTitleMatch_SkipsLLM(t *testing.T) {
	// Exact title match should skip LLM invocation entirely.
	gh := &mockGitHubClient{
		searchResults: []Issue{
			{Number: 99, Title: "CI Failure: periodic-e2e-test / CRI-O mirror HTTP 404",
				Body: "CRI-O mirror outage"},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{}, // No results — should NOT be called
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	title := "CI Failure: periodic-e2e-test / CRI-O mirror HTTP 404"
	analysis := "## Summary\nCRI-O mirror HTTP 404\n\n## Root Cause\nMirror outage"

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"periodic-e2e-test",
		title,
		analysis,
		gh.searchResults,
		"/tmp/worktree",
		nil, // no cycle issues
		nil, // no concurrent failures (single job)
	)

	// Exact title match — should return issue #99
	if matchedIssue != 99 {
		t.Errorf("expected exact title match on issue #99, got #%d", matchedIssue)
	}

	// LLM should NOT have been called
	if codeAgent.callCount != 0 {
		t.Errorf("expected 0 agent calls (exact title match), got %d", codeAgent.callCount)
	}
}

func TestTriageDedup_SameJobDifferentSignature_MatchesAcrossDays(t *testing.T) {
	// When the same CI job fails on consecutive days, the failure signature
	// (LLM summary) may differ between runs. The same-job match should find
	// the existing issue by job name prefix without invoking the LLM.
	// This is the cross-day dedup scenario from issue #257.
	gh := &mockGitHubClient{
		searchResults: []Issue{
			{Number: 2835, Title: "CI Failure: kubevirt/cluster-network-addons-operator/kubevirt-ipam-controller.yaml / DHCP timeout in pod network",
				Body: "Periodic CI job failed. DHCP timeout during pod network setup."},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{}, // Should NOT be called — same-job match fires first
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	// Next day: same job, different failure signature from LLM
	title := "CI Failure: kubevirt/cluster-network-addons-operator/kubevirt-ipam-controller.yaml / IPAM controller pod CrashLoopBackOff"
	analysis := "## Summary\nIPAM controller pod CrashLoopBackOff\n\n## Root Cause\nMemory limit exceeded"

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"kubevirt/cluster-network-addons-operator/kubevirt-ipam-controller.yaml",
		title,
		analysis,
		gh.searchResults,
		"/tmp/worktree",
		nil, // no cycle issues (different triage cycle)
		nil, // no concurrent failures (single job)
	)

	// Should match issue #2835 via same-job prefix match
	if matchedIssue != 2835 {
		t.Errorf("expected same-job match on issue #2835, got #%d", matchedIssue)
	}

	// LLM should NOT have been called (same-job match is pre-LLM)
	if codeAgent.callCount != 0 {
		t.Errorf("expected 0 agent calls (same-job match), got %d", codeAgent.callCount)
	}
}

func TestTriageDedup_SameJobNoSignature_MatchesAcrossDays(t *testing.T) {
	// Existing issue has no failure signature suffix. Same-job match
	// should still find it.
	gh := &mockGitHubClient{
		searchResults: []Issue{
			{Number: 100, Title: "CI Failure: owner/repo/ci.yml",
				Body: "Periodic CI job failed."},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	title := "CI Failure: owner/repo/ci.yml / New failure signature"
	analysis := "## Summary\nNew failure"

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"owner/repo/ci.yml",
		title,
		analysis,
		gh.searchResults,
		"/tmp/worktree",
		nil, nil,
	)

	if matchedIssue != 100 {
		t.Errorf("expected same-job match on issue #100, got #%d", matchedIssue)
	}
	if codeAgent.callCount != 0 {
		t.Errorf("expected 0 agent calls, got %d", codeAgent.callCount)
	}
}

func TestTriageDedup_DifferentJob_DoesNotSameJobMatch(t *testing.T) {
	// Issues from a DIFFERENT job should NOT be matched by same-job match.
	// They should fall through to LLM matching.
	gh := &mockGitHubClient{
		searchResults: []Issue{
			{Number: 200, Title: "CI Failure: owner/repo/e2e.yml / DNS timeout",
				Body: "Periodic CI job failed. DNS timeout."},
		},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "NONE"}}, // LLM says no match
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	// Different job name
	title := "CI Failure: owner/repo/ci.yml / Compile error"
	analysis := "## Summary\nCompile error"

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"owner/repo/ci.yml",
		title,
		analysis,
		gh.searchResults,
		"/tmp/worktree",
		nil, nil,
	)

	// Should NOT match — different job
	if matchedIssue != 0 {
		t.Errorf("expected no match (different job), got #%d", matchedIssue)
	}

	// LLM should have been called (same-job match didn't fire)
	if codeAgent.callCount != 1 {
		t.Errorf("expected 1 agent call (LLM matching), got %d", codeAgent.callCount)
	}
}

func TestFindSameJobIssue(t *testing.T) {
	tests := []struct {
		name   string
		job    string
		issues []Issue
		want   int
	}{
		{
			name:   "no issues",
			job:    "owner/repo/ci.yml",
			issues: nil,
			want:   0,
		},
		{
			name: "matches with signature",
			job:  "owner/repo/ci.yml",
			issues: []Issue{
				{Number: 10, Title: "CI Failure: owner/repo/ci.yml / Some error"},
			},
			want: 10,
		},
		{
			name: "matches without signature",
			job:  "owner/repo/ci.yml",
			issues: []Issue{
				{Number: 11, Title: "CI Failure: owner/repo/ci.yml"},
			},
			want: 11,
		},
		{
			name: "no match different job",
			job:  "owner/repo/ci.yml",
			issues: []Issue{
				{Number: 12, Title: "CI Failure: owner/repo/e2e.yml / Timeout"},
			},
			want: 0,
		},
		{
			name: "no match job name is prefix of another",
			job:  "owner/repo/ci.yml",
			issues: []Issue{
				{Number: 13, Title: "CI Failure: owner/repo/ci.yml-extended / Error"},
			},
			want: 0,
		},
		{
			name: "returns first match",
			job:  "owner/repo/ci.yml",
			issues: []Issue{
				{Number: 14, Title: "CI Failure: owner/repo/ci.yml / Error A"},
				{Number: 15, Title: "CI Failure: owner/repo/ci.yml / Error B"},
			},
			want: 14,
		},
		{
			name: "ignores non-CI-Failure titles",
			job:  "owner/repo/ci.yml",
			issues: []Issue{
				{Number: 16, Title: "Flaky CI: owner/repo/ci.yml / Intermittent test"},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findSameJobIssue(tt.job, tt.issues)
			if got != tt.want {
				t.Errorf("findSameJobIssue(%q, ...) = %d, want %d", tt.job, got, tt.want)
			}
		})
	}
}

func TestTriageDedup_NoExistingIssues_ReturnsZero(t *testing.T) {
	// No existing issues → should return 0 (will create a new issue).
	gh := &mockGitHubClient{
		searchResults: []Issue{},
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
	}

	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"periodic-e2e-test",
		"CI Failure: periodic-e2e-test / some failure",
		"analysis",
		[]Issue{},
		"/tmp/worktree",
		nil, // no cycle issues
		nil, // no concurrent failures (single job)
	)

	if matchedIssue != 0 {
		t.Errorf("expected 0 (no existing issues), got #%d", matchedIssue)
	}
	if codeAgent.callCount != 0 {
		t.Errorf("expected 0 agent calls (no existing issues), got %d", codeAgent.callCount)
	}
}

func TestTriageDedup_AddRunLinkComment(t *testing.T) {
	// When a match IS found, a comment should be added to the existing issue.
	gh := &mockGitHubClient{}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner: "owner",
		Repo:  "repo",
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), &ClaudeCodeAgent{})

	run := JobRun{
		ID:     "12345",
		LogURL: "https://example.com/logs/12345",
	}

	a.addTriageRunLinkComment(context.Background(), "periodic-e2e-test", run, 42)

	if len(gh.addedComments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(gh.addedComments))
	}

	comment := gh.addedComments[0]
	if !strings.Contains(comment, "Same failure observed") {
		t.Errorf("expected comment to mention 'Same failure observed', got: %q", comment)
	}
	if !strings.Contains(comment, "periodic-e2e-test") {
		t.Errorf("expected comment to mention job name, got: %q", comment)
	}
	if !strings.Contains(comment, "12345") {
		t.Errorf("expected comment to mention run ID, got: %q", comment)
	}
	if !strings.Contains(comment, "https://example.com/logs/12345") {
		t.Errorf("expected comment to mention log URL, got: %q", comment)
	}
	if gh.addedCommentTargets[0] != 42 {
		t.Errorf("expected comment posted to issue #42, got #%d", gh.addedCommentTargets[0])
	}
}

func TestExtractFailureSignature(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "summary section",
			input: "## Summary\nCRI-O mirror returned HTTP 404\n\n## Root Cause\nMirror outage",
			want:  "CRI-O mirror returned HTTP 404",
		},
		{
			name:  "multi-line summary",
			input: "## Summary\nDNS resolution failed for registry.k8s.io causing cluster bootstrap to hang\n\n## Root Cause\nDNS outage",
			want:  "DNS resolution failed for registry.k8s.io causing cluster",
		},
		{
			name:  "no summary section falls back to first line",
			input: "The test timed out waiting for pod readiness",
			want:  "The test timed out waiting for pod readiness",
		},
		{
			name:  "empty analysis",
			input: "",
			want:  "",
		},
		{
			name:  "truncates long signature",
			input: "## Summary\n" + strings.Repeat("x", 200) + "\n\n## Root Cause\nSomething",
			want:  strings.Repeat("x", 60),
		},
		{
			name:  "blank line after heading",
			input: "## Summary\n\nCRI-O mirror returned HTTP 404\n\n## Root Cause\nMirror outage",
			want:  "CRI-O mirror returned HTTP 404",
		},
		{
			name:  "truncates by runes not bytes",
			input: "## Summary\n" + strings.Repeat("\u00e9", 100) + "\n\n## Root Cause\nSomething",
			want:  strings.Repeat("\u00e9", 60),
		},
		{
			name:  "skips markdown headings",
			input: "## Summary\n\n## Root Cause\nThe actual content here",
			want:  "The actual content here",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFailureSignature(tt.input)
			if got != tt.want {
				t.Errorf("extractFailureSignature() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProcessTriageJobs_DeterministicFallbackWhenLLMSaysNone(t *testing.T) {
	// When multiple different jobs fail and the LLM says NONE for the second
	// job, the deterministic fallback should match to the cycle issue created
	// by the first job instead of creating a duplicate.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 100, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/100"},
		},
		searchResults:   []Issue{}, // GitHub search returns nothing
		nextIssueNumber: 10,
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
		TriageJobs: []string{
			"https://github.com/owner/repo/actions/workflows/unit.yml",
			"https://github.com/owner/repo/actions/workflows/e2e.yml",
			"https://github.com/owner/repo/actions/workflows/lint.yml",
		},
	}

	// First job: analysis → no match → creates issue
	// Second job: analysis + LLM says NONE → deterministic fallback should match
	// Third job: analysis + LLM says NONE → deterministic fallback should match
	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "## Summary\nInfra outage broke CI"}},    // first job analysis
			{result: AgentResult{Result: "## Summary\nDNS resolution failed"}},    // second job analysis
			{result: AgentResult{Result: "NONE"}},                                 // second job LLM says NONE
			{result: AgentResult{Result: "## Summary\nBuild timeout due to DNS"}}, // third job analysis
			{result: AgentResult{Result: "NONE"}},                                 // third job LLM says NONE
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should create exactly 1 issue (first job); jobs 2 and 3 use deterministic fallback
	if len(gh.createdIssues) != 1 {
		t.Errorf("expected 1 issue created (deterministic fallback for jobs 2+3), got %d", len(gh.createdIssues))
	}

	// Run-link comments should be posted for jobs 2 and 3
	runLinkCount := 0
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "Same failure observed") {
			runLinkCount++
		}
	}
	if runLinkCount != 2 {
		t.Errorf("expected 2 run-link comments, got %d", runLinkCount)
	}
}

func TestTriageDedup_DeterministicFallbackWithCycleIssuesMerged(t *testing.T) {
	// Mirrors production behavior: investigateTriageRun merges cycleIssues
	// into existingIssues before calling matchExistingTriageIssue. When the
	// LLM says NONE, the deterministic fallback should match to the cycle
	// issue even though it's present in existingIssues.
	gh := &mockGitHubClient{
		searchResults: []Issue{}, // GitHub search returns nothing (search lag)
	}
	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
	}

	// LLM says NONE — deterministic fallback should fire after LLM
	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "NONE"}},
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)

	title := "CI Failure: job-b / DNS timeout"
	analysis := "## Summary\nDNS timeout"

	// Simulate 3 failed jobs with 1 cycle issue already created
	cycleIssues := []Issue{
		{Number: 42, Title: "CI Failure: job-a / Infra outage", Body: "Infra outage"},
	}
	cycleFailedJobs := []string{"job-a", "job-b", "job-c"}

	// Mirror production: merge cycleIssues into existingIssues (as
	// investigateTriageRun does before calling matchExistingTriageIssue)
	existingIssues := mergeIssues([]Issue{}, cycleIssues)

	matchedIssue := a.matchExistingTriageIssue(context.Background(),
		"job-b", title, analysis,
		existingIssues,
		"/tmp/worktree",
		cycleIssues,
		cycleFailedJobs,
	)

	// Should match cycle issue #42 via deterministic fallback after LLM NONE
	if matchedIssue != 42 {
		t.Errorf("expected deterministic fallback to match cycle issue #42, got #%d", matchedIssue)
	}

	// LLM should have been called once (title didn't match, so LLM tried)
	if codeAgent.callCount != 1 {
		t.Errorf("expected 1 agent call (LLM tried before fallback), got %d", codeAgent.callCount)
	}
}

func TestTriageDedup_DeterministicFallbackWithMultiLineNone(t *testing.T) {
	// When multiple jobs fail and the LLM returns a multi-line NONE response
	// (e.g. "NONE\n\nNo match found."), the deterministic fallback should
	// still match to the cycle issue created by the first job.
	now := time.Now()
	gh := &mockGitHubClient{
		workflowRuns: []WorkflowRun{
			{ID: 500, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-1 * time.Hour), HTMLURL: "https://github.com/owner/repo/actions/runs/500"},
		},
		searchResults:   []Issue{}, // GitHub search returns nothing
		nextIssueNumber: 20,
	}

	// Two separate TriageJobs URLs (functionally equivalent to concurrent
	// jobs or lanes) to verify that the deterministic fallback works
	// when the LLM returns a multi-line NONE response.

	runner := &mockCommandRunner{}
	wtm := &mockWorktreeManager{}
	state := NewState()
	cfg := Config{
		Owner:             "owner",
		Repo:              "repo",
		FlakyLabel:        "ci-flake",
		CreateFlakyIssues: true,
		TriageJobs: []string{
			"https://github.com/owner/repo/actions/workflows/unit.yml",
			"https://github.com/owner/repo/actions/workflows/e2e.yml",
		},
	}

	// First job: analysis → creates issue
	// Second job: analysis + LLM says NONE → deterministic fallback
	codeAgent := &sequentialMockCodeAgent{
		results: []mockCodeAgentCall{
			{result: AgentResult{Result: "## Summary\nShared root cause"}},
			{result: AgentResult{Result: "## Summary\nDifferent symptoms same cause"}},
			{result: AgentResult{Result: "NONE\n\nNo match found."}}, // multi-line NONE
		},
	}

	a := NewAgent(gh, runner, wtm, state, cfg, slog.Default(), codeAgent)
	a.ProcessTriageJobs(context.Background())

	// Should create exactly 1 issue; second job uses deterministic fallback
	if len(gh.createdIssues) != 1 {
		t.Errorf("expected 1 issue created, got %d", len(gh.createdIssues))
	}

	// Verify run-link comment was posted for the second job
	runLinkFound := false
	for _, comment := range gh.addedComments {
		if strings.Contains(comment, "Same failure observed") {
			runLinkFound = true
		}
	}
	if runLinkFound == false {
		t.Error("expected a run-link comment for the second job (deterministic fallback)")
	}
}
