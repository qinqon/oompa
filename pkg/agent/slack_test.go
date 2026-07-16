package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// slackTestAgent returns an agent configured for org/repo Slack checks with
// one open PR (#100) tracked in state.
func slackTestAgent(gh *mockGitHubClient) *Agent {
	a := newTestAgent(gh, &mockCommandRunner{}, &mockWorktreeManager{}, withCfg(func(c *Config) {
		c.Owner = "org"
		c.SlackWebhookURL = "http://example.com/webhook"
	}))
	a.state.ActiveIssues["org/repo#100"] = &IssueWork{
		PRNumber:   100,
		IssueTitle: "Fix test",
		Status:     StatusPROpen,
	}
	return a
}

// newCountingWebhook builds a SlackReporter posting to a local test server
// with dedup state isolated under a temp XDG_STATE_HOME. The returned func
// reports how many webhook posts were received.
func TestCheckFindings(t *testing.T) {
	checkCI := func(ctx context.Context, a *Agent, last time.Time) []SlackFinding {
		return a.CheckCIStatus(ctx, last)
	}
	checkRebase := func(ctx context.Context, a *Agent, _ time.Time) []SlackFinding {
		return a.checkRebaseNeededWithStates(ctx, a.fetchMergeableStates(ctx))
	}
	checkConflicts := func(ctx context.Context, a *Agent, _ time.Time) []SlackFinding {
		return a.checkConflictsWithStates(ctx, a.fetchMergeableStates(ctx))
	}
	checkReviews := func(ctx context.Context, a *Agent, last time.Time) []SlackFinding {
		return a.CheckNewReviews(ctx, last)
	}

	tests := []struct {
		name              string
		gh                *mockGitHubClient
		lastReportedAt    time.Time
		check             func(context.Context, *Agent, time.Time) []SlackFinding
		wantCount         int
		wantCategory      string
		wantMsgContains   string
		wantDedupContains string
	}{
		{
			name: "CheckCIStatus reports failures",
			gh: &mockGitHubClient{
				checkRuns: []CheckRun{
					{ID: 1, Name: "e2e-test", Status: "completed", Conclusion: "failure", HTMLURL: "https://github.com/org/repo/actions/runs/1/job/1"},
					{ID: 2, Name: "unit-test", Status: "completed", Conclusion: "success"},
				},
				prHeadSHAs: []string{"abc123"},
			},
			check:             checkCI,
			wantCount:         1,
			wantCategory:      "ci",
			wantMsgContains:   "e2e-test",
			wantDedupContains: "abc123",
		},
		{
			name: "CheckCIStatus no failures for passing CI",
			gh: &mockGitHubClient{
				checkRuns: []CheckRun{
					{ID: 1, Name: "e2e-test", Status: "completed", Conclusion: "success"},
				},
				prHeadSHAs: []string{"abc123"},
			},
			check:     checkCI,
			wantCount: 0,
		},
		{
			name: "CheckCIStatus filters stale failures",
			gh: &mockGitHubClient{
				checkRuns: []CheckRun{
					{ID: 1, Name: "stale-test", Status: "completed", Conclusion: "failure",
						CompletedAt: time.Date(2026, 5, 29, 9, 0, 0, 0, time.UTC), // Before lastReportedAt
						HTMLURL:     "https://github.com/org/repo/actions/runs/1/job/1"},
					{ID: 2, Name: "new-test", Status: "completed", Conclusion: "failure",
						CompletedAt: time.Date(2026, 5, 29, 11, 0, 0, 0, time.UTC), // After lastReportedAt
						HTMLURL:     "https://github.com/org/repo/actions/runs/2/job/2"},
				},
				prHeadSHAs: []string{"abc123"},
			},
			lastReportedAt:    time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC),
			check:             checkCI,
			wantCount:         1,
			wantCategory:      "ci",
			wantMsgContains:   "new-test",
			wantDedupContains: "abc123",
		},
		{
			// Failures with zero CompletedAt (e.g. commit statuses) should not be filtered.
			name: "CheckCIStatus includes failures with zero CompletedAt",
			gh: &mockGitHubClient{
				checkRuns: []CheckRun{
					{ID: 1, Name: "no-timestamp", Status: "completed", Conclusion: "failure",
						HTMLURL: "https://github.com/org/repo/actions/runs/1/job/1"},
				},
				prHeadSHAs: []string{"abc123"},
			},
			lastReportedAt:    time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC),
			check:             checkCI,
			wantCount:         1,
			wantCategory:      "ci",
			wantMsgContains:   "no-timestamp",
			wantDedupContains: "abc123",
		},
		{
			name: "CheckRebaseNeeded reports behind",
			gh: &mockGitHubClient{
				mergeableState: "behind",
				prBehind:       true,
			},
			check:             checkRebase,
			wantCount:         1,
			wantCategory:      "rebase",
			wantMsgContains:   "behind main",
			wantDedupContains: "rebase-needed:100",
		},
		{
			name:      "CheckRebaseNeeded no findings for clean state",
			gh:        &mockGitHubClient{mergeableState: "clean"},
			check:     checkRebase,
			wantCount: 0,
		},
		{
			name:              "CheckConflicts reports dirty",
			gh:                &mockGitHubClient{mergeableState: "dirty"},
			check:             checkConflicts,
			wantCount:         1,
			wantCategory:      "conflict",
			wantMsgContains:   "merge conflicts",
			wantDedupContains: "conflict:100",
		},
		{
			name: "CheckNewReviews reports comments",
			gh: &mockGitHubClient{
				prComments: []ReviewComment{
					{ID: 10, User: "reviewer1", Body: "Please fix this"},
					{ID: 11, User: "reviewer2", Body: "LGTM"},
				},
			},
			check:             checkReviews,
			wantCount:         1,
			wantCategory:      "review",
			wantMsgContains:   "2 new review",
			wantDedupContains: "review:100:11",
		},
		{
			name: "CheckNewReviews filters stale comments",
			gh: &mockGitHubClient{
				prComments: []ReviewComment{
					{ID: 10, User: "reviewer1", Body: "Old comment",
						CreatedAt: time.Date(2026, 5, 29, 9, 0, 0, 0, time.UTC)}, // Before lastReportedAt
					{ID: 11, User: "reviewer2", Body: "New comment",
						CreatedAt: time.Date(2026, 5, 29, 11, 0, 0, 0, time.UTC)}, // After lastReportedAt
				},
			},
			lastReportedAt:    time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC),
			check:             checkReviews,
			wantCount:         1,
			wantCategory:      "review",
			wantMsgContains:   "1 new review comment",
			wantDedupContains: "review:100:11",
		},
		{
			// Comments with zero CreatedAt should not be filtered.
			name: "CheckNewReviews includes comments with zero CreatedAt",
			gh: &mockGitHubClient{
				prComments: []ReviewComment{
					{ID: 10, User: "reviewer1", Body: "No timestamp comment"},
				},
			},
			lastReportedAt:    time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC),
			check:             checkReviews,
			wantCount:         1,
			wantCategory:      "review",
			wantMsgContains:   "1 new review comment",
			wantDedupContains: "review:100:10",
		},
		{
			name: "CheckNewReviews all stale no findings",
			gh: &mockGitHubClient{
				prComments: []ReviewComment{
					{ID: 10, User: "reviewer1", Body: "Old comment",
						CreatedAt: time.Date(2026, 5, 29, 9, 0, 0, 0, time.UTC)},
					{ID: 11, User: "reviewer2", Body: "Also old",
						CreatedAt: time.Date(2026, 5, 29, 8, 0, 0, 0, time.UTC)},
				},
			},
			lastReportedAt: time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC),
			check:          checkReviews,
			wantCount:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := slackTestAgent(tt.gh)

			findings := tt.check(context.Background(), a, tt.lastReportedAt)

			if len(findings) != tt.wantCount {
				t.Fatalf("expected %d finding(s), got %d", tt.wantCount, len(findings))
			}
			if tt.wantCount == 0 {
				return
			}
			f := findings[0]
			if f.Owner != "org" || f.Repo != "repo" {
				t.Errorf("expected Owner/Repo org/repo, got %s/%s", f.Owner, f.Repo)
			}
			if f.Category != tt.wantCategory {
				t.Errorf("expected category %s, got %s", tt.wantCategory, f.Category)
			}
			if !strings.Contains(f.Message, tt.wantMsgContains) {
				t.Errorf("expected message to contain %q, got: %s", tt.wantMsgContains, f.Message)
			}
			if !strings.Contains(f.DedupKey, tt.wantDedupContains) {
				t.Errorf("expected DedupKey to contain %q, got: %s", tt.wantDedupContains, f.DedupKey)
			}
		})
	}
}
