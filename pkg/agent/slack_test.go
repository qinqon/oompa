package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
func newCountingWebhook(t *testing.T) (reporter *SlackReporter, posts func() int) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var mu sync.Mutex
	count := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	reporter = NewSlackReporter(ts.URL, "", discardLogger())
	reporter.httpClient = ts.Client()
	return reporter, func() int { mu.Lock(); defer mu.Unlock(); return count }
}

func TestFormatSlackMessage_GroupsByPR(t *testing.T) {
	findings := []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix flaky test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 <https://example.com/job/1|e2e-test> failed"},
		{Owner: "org", Repo: "repo", PRNumber: 200, PRTitle: "Add feature", PRURL: "https://github.com/org/repo/pull/200", Category: "conflict", Message: "⚠️ Merge conflicts"},
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix flaky test", PRURL: "https://github.com/org/repo/pull/100", Category: "rebase", Message: "⚠️ 15 commits behind main"},
	}

	body := formatSlackMessage(findings, "")
	if body == nil {
		t.Fatal("expected non-nil body")
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Should have: header + project section header + detail section + (no trailing divider)
	if len(msg.Blocks) < 3 {
		t.Fatalf("expected at least 3 blocks, got %d", len(msg.Blocks))
	}

	// Header block
	if msg.Blocks[0].Type != "header" {
		t.Errorf("expected header block, got %s", msg.Blocks[0].Type)
	}
	if !strings.Contains(msg.Blocks[0].Text.Text, "1 project(s)") {
		t.Errorf("expected header to mention 1 project, got: %s", msg.Blocks[0].Text.Text)
	}

	// Project header section
	if !strings.Contains(msg.Blocks[1].Text.Text, "org/repo") {
		t.Error("expected project header to contain org/repo")
	}

	// Detail section — find the section with PR details
	var fullTextBuilder strings.Builder
	for _, b := range msg.Blocks {
		if b.Text != nil {
			fullTextBuilder.WriteString(b.Text.Text)
		}
	}
	fullText := fullTextBuilder.String()

	// PR #100 should appear before PR #200 (sorted by PR number)
	idx100 := strings.Index(fullText, "PR #100")
	idx200 := strings.Index(fullText, "PR #200")
	if idx100 == -1 || idx200 == -1 {
		t.Fatalf("expected both PRs in output, got: %s", fullText)
	}
	if idx100 > idx200 {
		t.Errorf("PR #100 should appear before PR #200")
	}

	// Both findings for PR #100 should be grouped together
	if !strings.Contains(fullText, "e2e-test") {
		t.Error("expected e2e-test finding")
	}
	if !strings.Contains(fullText, "15 commits behind main") {
		t.Error("expected rebase finding")
	}
	if !strings.Contains(fullText, "Merge conflicts") {
		t.Error("expected conflict finding")
	}
}

func TestFormatSlackMessage_IncludesVersion(t *testing.T) {
	findings := []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 failed"},
	}

	body := formatSlackMessage(findings, "38767d1abc123")
	if body == nil {
		t.Fatal("expected non-nil body")
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Header should include the short SHA (7 chars)
	headerText := msg.Blocks[0].Text.Text
	if !strings.Contains(headerText, "(38767d1)") {
		t.Errorf("expected header to contain short SHA (38767d1), got: %s", headerText)
	}

	// Also verify the full format
	if !strings.Contains(headerText, "oompa report (38767d1)") {
		t.Errorf("expected header format 'oompa report (38767d1)', got: %s", headerText)
	}
}

func TestFormatSlackMessage_EmptyVersionOmitted(t *testing.T) {
	findings := []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 failed"},
	}

	body := formatSlackMessage(findings, "")
	if body == nil {
		t.Fatal("expected non-nil body")
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Header should NOT contain parentheses when version is empty
	headerText := msg.Blocks[0].Text.Text
	if strings.Contains(headerText, "()") {
		t.Errorf("expected no empty parens in header, got: %s", headerText)
	}
	if !strings.Contains(headerText, "oompa report —") {
		t.Errorf("expected plain header without version, got: %s", headerText)
	}
}

func TestFormatSlackMessage_EmptyFindings(t *testing.T) {
	body := formatSlackMessage(nil, "")
	if body != nil {
		t.Error("expected nil body for empty findings")
	}

	body = formatSlackMessage([]SlackFinding{}, "")
	if body != nil {
		t.Error("expected nil body for empty slice")
	}
}

func TestFormatSlackMessage_MultipleProjects(t *testing.T) {
	findings := []SlackFinding{
		{Owner: "org1", Repo: "repo1", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org1/repo1/pull/100", Category: "ci", Message: "🔴 e2e failed"},
		{Owner: "org2", Repo: "repo2", PRNumber: 200, PRTitle: "Add feature", PRURL: "https://github.com/org2/repo2/pull/200", Category: "conflict", Message: "⚠️ Merge conflicts"},
		{Owner: "org1", Repo: "repo1", PRNumber: 101, PRTitle: "Fix lint", PRURL: "https://github.com/org1/repo1/pull/101", Category: "rebase", Message: "⚠️ behind main"},
	}

	body := formatSlackMessage(findings, "")
	if body == nil {
		t.Fatal("expected non-nil body")
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Header should mention 2 projects
	if !strings.Contains(msg.Blocks[0].Text.Text, "2 project(s)") {
		t.Errorf("expected header to mention 2 projects, got: %s", msg.Blocks[0].Text.Text)
	}

	// Check that both projects appear in the output
	var fullTextBuilder strings.Builder
	for _, b := range msg.Blocks {
		if b.Text != nil {
			fullTextBuilder.WriteString(b.Text.Text)
		}
	}
	fullText := fullTextBuilder.String()
	if !strings.Contains(fullText, "org1/repo1") {
		t.Error("expected org1/repo1 in output")
	}
	if !strings.Contains(fullText, "org2/repo2") {
		t.Error("expected org2/repo2 in output")
	}

	// org1/repo1 should appear before org2/repo2 (alphabetically sorted)
	idx1 := strings.Index(fullText, "org1/repo1")
	idx2 := strings.Index(fullText, "org2/repo2")
	if idx1 > idx2 {
		t.Errorf("org1/repo1 should appear before org2/repo2 (alphabetical sort)")
	}

	// org1/repo1 should have 2 PRs
	if !strings.Contains(fullText, "2 PR(s)") {
		t.Error("expected org1/repo1 to show 2 PR(s)")
	}
}

func TestFormatSlackMessage_SingleProjectNoFindings(t *testing.T) {
	// One project with findings, no message for projects with no findings
	findings := []SlackFinding{
		{Owner: "org", Repo: "active", PRNumber: 1, PRTitle: "Work", PRURL: "https://github.com/org/active/pull/1", Category: "ci", Message: "🔴 failed"},
	}

	body := formatSlackMessage(findings, "")
	if body == nil {
		t.Fatal("expected non-nil body")
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !strings.Contains(msg.Blocks[0].Text.Text, "1 project(s)") {
		t.Errorf("expected 1 project in header, got: %s", msg.Blocks[0].Text.Text)
	}
}

func TestFormatSlackMessage_LinksCorrect(t *testing.T) {
	findings := []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 42, PRTitle: "Test PR", PRURL: "https://github.com/org/repo/pull/42", Category: "ci", Message: "🔴 <https://ci.example.com/job/1|e2e-test> failed"},
	}

	body := formatSlackMessage(findings, "")
	if body == nil {
		t.Fatal("expected non-nil body")
	}

	text := string(body)

	// Check project link
	if !strings.Contains(text, "https://github.com/org/repo") {
		t.Error("expected project GitHub link")
	}

	// Check PR link
	if !strings.Contains(text, "https://github.com/org/repo/pull/42") {
		t.Error("expected PR link")
	}

	// Check CI job link
	if !strings.Contains(text, "https://ci.example.com/job/1") {
		t.Error("expected CI job link")
	}
}

func TestFormatSlackMessage_HasDividersBetweenProjects(t *testing.T) {
	findings := []SlackFinding{
		{Owner: "org1", Repo: "repo1", PRNumber: 1, PRTitle: "PR1", PRURL: "https://github.com/org1/repo1/pull/1", Category: "ci", Message: "🔴 failed"},
		{Owner: "org2", Repo: "repo2", PRNumber: 2, PRTitle: "PR2", PRURL: "https://github.com/org2/repo2/pull/2", Category: "ci", Message: "🔴 failed"},
	}

	body := formatSlackMessage(findings, "")
	if body == nil {
		t.Fatal("expected non-nil body")
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Should have dividers between projects but not after the last one
	dividerCount := 0
	for _, b := range msg.Blocks {
		if b.Type == "divider" {
			dividerCount++
		}
	}
	if dividerCount != 1 {
		t.Errorf("expected 1 divider between 2 projects, got %d", dividerCount)
	}

	// Last block should not be a divider
	if msg.Blocks[len(msg.Blocks)-1].Type == "divider" {
		t.Error("last block should not be a divider")
	}
}

func TestSlackReporter_Dedup(t *testing.T) {
	reporter, posts := newCountingWebhook(t)

	findings := []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 test failed", DedupKey: "ci:abc123:e2e"},
	}

	ctx := context.Background()

	// First report should send
	reporter.Report(ctx, findings)
	if posts() != 1 {
		t.Errorf("expected 1 POST, got %d", posts())
	}

	// Second report with same DedupKey should be suppressed
	reporter.Report(ctx, findings)
	if posts() != 1 {
		t.Errorf("expected still 1 POST after dedup, got %d", posts())
	}
}

func TestSlackReporter_DedupReset(t *testing.T) {
	reporter, posts := newCountingWebhook(t)

	ctx := context.Background()

	// First finding
	reporter.Report(ctx, []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 test failed", DedupKey: "ci:abc123:e2e"},
	})
	if posts() != 1 {
		t.Errorf("expected 1 POST, got %d", posts())
	}

	// Different DedupKey (new SHA) should re-send
	reporter.Report(ctx, []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 test failed", DedupKey: "ci:def456:e2e"},
	})
	if posts() != 2 {
		t.Errorf("expected 2 POSTs after new DedupKey, got %d", posts())
	}
}

func TestSlackReporter_Disabled(t *testing.T) {
	logger := discardLogger()
	reporter := NewSlackReporter("", "", logger)

	if reporter.IsEnabled() {
		t.Error("expected IsEnabled() to be false with empty webhook URL")
	}

	// Should not panic or error when disabled
	reporter.Report(context.Background(), []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Test", Message: "test", DedupKey: "test"},
	})
}

func TestPostToSlack_Success(t *testing.T) {
	var receivedBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)

		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	payload := []byte(`{"text":"hello"}`)
	err := postToSlack(context.Background(), ts.Client(), ts.URL, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedBody != `{"text":"hello"}` {
		t.Errorf("unexpected body: %s", receivedBody)
	}
}

func TestPostToSlack_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	err := postToSlack(context.Background(), ts.Client(), ts.URL, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to mention status code, got: %v", err)
	}
}

// TestCheckFindings covers the Check* finding-collection surface —
// CheckCIStatus, checkRebaseNeededWithStates, checkConflictsWithStates, and
// CheckNewReviews — against a slackTestAgent with one open PR (#100),
// asserting finding count, owner/repo, category, message content, dedup key,
// and lastReportedAt-based stale filtering.
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

func TestSlackReporter_EmptyDedupKey(t *testing.T) {
	reporter, posts := newCountingWebhook(t)

	ctx := context.Background()

	// Findings with empty DedupKey should always be sent
	findings := []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Test", Message: "test", DedupKey: ""},
	}

	reporter.Report(ctx, findings)
	if posts() != 1 {
		t.Errorf("expected 1 POST, got %d", posts())
	}

	reporter.Report(ctx, findings)
	if posts() != 2 {
		t.Errorf("expected 2 POSTs (empty DedupKey never suppressed), got %d", posts())
	}
}

func TestURLHelpers(t *testing.T) {
	if got := prURL("org", "repo", 42); got != "https://github.com/org/repo/pull/42" {
		t.Errorf("prURL: %s", got)
	}
	if got := commitURL("org", "repo", "abc123"); got != "https://github.com/org/repo/commit/abc123" {
		t.Errorf("commitURL: %s", got)
	}
	if got := issueURL("org", "repo", 7); got != "https://github.com/org/repo/issues/7" {
		t.Errorf("issueURL: %s", got)
	}
}

func TestSlackReporter_CollectAndFlush(t *testing.T) {
	reporter, posts := newCountingWebhook(t)

	ctx := context.Background()

	// Collect findings from multiple projects
	reporter.Collect([]SlackFinding{
		{Owner: "org1", Repo: "repo1", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org1/repo1/pull/100", Category: "ci", Message: "🔴 failed", DedupKey: "ci:abc:e2e"},
	})
	reporter.Collect([]SlackFinding{
		{Owner: "org2", Repo: "repo2", PRNumber: 200, PRTitle: "Add feature", PRURL: "https://github.com/org2/repo2/pull/200", Category: "conflict", Message: "⚠️ conflicts", DedupKey: "conflict:200"},
	})

	// No POST until Flush
	if posts() != 0 {
		t.Errorf("expected 0 POSTs before Flush, got %d", posts())
	}

	// Flush should send a single consolidated message
	reporter.Flush(ctx)
	if posts() != 1 {
		t.Errorf("expected 1 POST after Flush, got %d", posts())
	}

	// Pending should be drained
	reporter.mu.Lock()
	if len(reporter.pending) != 0 {
		t.Errorf("expected pending to be empty after Flush, got %d", len(reporter.pending))
	}
	reporter.mu.Unlock()

	// Flushing again with no new findings should not POST
	reporter.Flush(ctx)
	if posts() != 1 {
		t.Errorf("expected still 1 POST after empty Flush, got %d", posts())
	}
}

func TestSlackReporter_CollectConcurrent(t *testing.T) {
	reporter, posts := newCountingWebhook(t)

	// Simulate concurrent collection from multiple goroutines
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			reporter.Collect([]SlackFinding{
				{Owner: "org", Repo: "repo", PRNumber: i, PRTitle: "Test", Message: "test", DedupKey: ""},
			})
		})
	}
	wg.Wait()

	// All 10 findings should be pending
	reporter.mu.Lock()
	pendingCount := len(reporter.pending)
	reporter.mu.Unlock()
	if pendingCount != 10 {
		t.Errorf("expected 10 pending findings, got %d", pendingCount)
	}

	// Flush should send all as one message
	reporter.Flush(context.Background())
	if posts() != 1 {
		t.Errorf("expected 1 POST after Flush, got %d", posts())
	}
}

func TestSlackReporter_FlushDedup(t *testing.T) {
	reporter, posts := newCountingWebhook(t)

	ctx := context.Background()

	// First collect+flush
	reporter.Collect([]SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Test", Message: "test", DedupKey: "ci:abc:e2e"},
	})
	reporter.Flush(ctx)
	if posts() != 1 {
		t.Errorf("expected 1 POST, got %d", posts())
	}

	// Second collect+flush with same DedupKey — should be suppressed
	reporter.Collect([]SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Test", Message: "test", DedupKey: "ci:abc:e2e"},
	})
	reporter.Flush(ctx)
	if posts() != 1 {
		t.Errorf("expected still 1 POST after dedup, got %d", posts())
	}
}

func TestFormatSlackMessage_BlockTextLimit(t *testing.T) {
	// Create a finding with a very long message to test block splitting
	var findings []SlackFinding
	longMsg := strings.Repeat("x", 200) // Each finding line will be ~200 chars
	for i := range 20 {
		findings = append(findings, SlackFinding{
			Owner:    "org",
			Repo:     "repo",
			PRNumber: i + 1,
			PRTitle:  "Test PR " + longMsg[:50],
			PRURL:    "https://github.com/org/repo/pull/" + strings.Repeat("1", 3),
			Category: "ci",
			Message:  "🔴 " + longMsg,
		})
	}

	body := formatSlackMessage(findings, "")
	if body == nil {
		t.Fatal("expected non-nil body")
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Verify no block text exceeds the limit
	for i, b := range msg.Blocks {
		if b.Text != nil && len(b.Text.Text) > maxSlackBlockTextLen {
			t.Errorf("block %d text exceeds limit: %d > %d", i, len(b.Text.Text), maxSlackBlockTextLen)
		}
	}
}

func TestFormatSlackMessage_BlockTextLimit_SingleLongLine(t *testing.T) {
	// A single finding with a message that exceeds 3000 chars and contains no
	// newlines, exercising the no-newline fallback in the block splitter.
	longMsg := strings.Repeat("A", maxSlackBlockTextLen+500)
	findings := []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 1, PRTitle: "Long", PRURL: "https://github.com/org/repo/pull/1", Category: "ci", Message: longMsg},
	}

	body := formatSlackMessage(findings, "")
	if body == nil {
		t.Fatal("expected non-nil body")
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Verify no block text exceeds the limit
	for i, b := range msg.Blocks {
		if b.Text != nil && len(b.Text.Text) > maxSlackBlockTextLen {
			t.Errorf("block %d text exceeds limit: %d > %d", i, len(b.Text.Text), maxSlackBlockTextLen)
		}
	}

	// Should have produced at least 2 detail blocks (split)
	detailBlocks := 0
	for _, b := range msg.Blocks {
		if b.Type == "section" && b.Text != nil && strings.Contains(b.Text.Text, "A") {
			detailBlocks++
		}
	}
	if detailBlocks < 2 {
		t.Errorf("expected at least 2 detail blocks after splitting, got %d", detailBlocks)
	}
}

func TestFormatSlackMessage_TruncatesAt50Blocks(t *testing.T) {
	// Create enough findings across many projects to exceed 50 blocks
	var findings []SlackFinding
	for i := range 30 {
		findings = append(findings, SlackFinding{
			Owner:    fmt.Sprintf("org%d", i),
			Repo:     fmt.Sprintf("repo%d", i),
			PRNumber: i + 1,
			PRTitle:  fmt.Sprintf("PR %d", i+1),
			PRURL:    fmt.Sprintf("https://github.com/org%d/repo%d/pull/%d", i, i, i+1),
			Category: "ci",
			Message:  "🔴 test failed",
		})
	}

	body := formatSlackMessage(findings, "")
	if body == nil {
		t.Fatal("expected non-nil body")
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(msg.Blocks) > maxSlackBlocks {
		t.Errorf("expected at most %d blocks, got %d", maxSlackBlocks, len(msg.Blocks))
	}

	// Last block should be the overflow notice
	lastBlock := msg.Blocks[len(msg.Blocks)-1]
	if lastBlock.Text == nil || !strings.Contains(lastBlock.Text.Text, "truncated") {
		t.Error("expected last block to be the truncation notice")
	}
}

func TestSlackReporter_FlushDedup_WithinBatch(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var mu sync.Mutex
	var postCount int
	var receivedBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		postCount++
		receivedBody = string(body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	logger := discardLogger()
	reporter := NewSlackReporter(ts.URL, "", logger)
	reporter.httpClient = ts.Client()

	ctx := context.Background()

	// Simulate multiple poll cycles collecting the same findings before Flush fires.
	// This is the exact scenario from the bug: unsynchronized timers cause 2-3 poll
	// cycles to append identical findings to pending before the flush goroutine runs.
	for range 3 {
		reporter.Collect([]SlackFinding{
			{Owner: "org", Repo: "repo", PRNumber: 8365, PRTitle: "Generate KubeVirt nmstate", PRURL: "https://github.com/org/repo/pull/8365", Category: "rebase", Message: "⚠️ PR #8365 is behind main", DedupKey: "rebase:8365"},
			{Owner: "org", Repo: "repo", PRNumber: 8365, PRTitle: "Generate KubeVirt nmstate", PRURL: "https://github.com/org/repo/pull/8365", Category: "review", Message: "💬 PR #8365 has 1 new review comment(s)", DedupKey: "review:8365:10"},
		})
	}

	// A single flush should produce exactly one message with each finding once
	reporter.Flush(ctx)

	mu.Lock()
	count := postCount
	body := receivedBody
	mu.Unlock()

	if count != 1 {
		t.Fatalf("expected 1 POST, got %d", count)
	}

	// Verify the message contains each finding exactly once
	rebaseCount := strings.Count(body, "behind main")
	if rebaseCount != 1 {
		t.Errorf("expected 1 'behind main' in message, got %d", rebaseCount)
	}
	reviewCount := strings.Count(body, "new review comment")
	if reviewCount != 1 {
		t.Errorf("expected 1 'new review comment' in message, got %d", reviewCount)
	}
}

func TestSlackReporter_FlushDedup_DifferentFindingsSamePR(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var mu sync.Mutex
	var receivedBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		receivedBody = string(b)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	logger := discardLogger()
	reporter := NewSlackReporter(ts.URL, "", logger)
	reporter.httpClient = ts.Client()

	ctx := context.Background()

	// Collect different findings for the same PR — all should appear (no over-dedup)
	reporter.Collect([]SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 e2e failed", DedupKey: "ci:abc:e2e"},
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "rebase", Message: "⚠️ behind main", DedupKey: "rebase:100"},
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "review", Message: "💬 2 new review comments", DedupKey: "review:100:5"},
	})

	reporter.Flush(ctx)

	mu.Lock()
	body := receivedBody
	mu.Unlock()

	if !strings.Contains(body, "e2e failed") {
		t.Error("expected 'e2e failed' in message")
	}
	if !strings.Contains(body, "behind main") {
		t.Error("expected 'behind main' in message")
	}
	if !strings.Contains(body, "new review comments") {
		t.Error("expected 'new review comments' in message")
	}
}

func TestSlackReporter_FlushDedup_EmptyDedupKeyNotDeduped(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var mu sync.Mutex
	var receivedBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		receivedBody = string(b)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	logger := discardLogger()
	reporter := NewSlackReporter(ts.URL, "", logger)
	reporter.httpClient = ts.Client()

	ctx := context.Background()

	// Findings without a DedupKey should always be included, even if identical
	for range 3 {
		reporter.Collect([]SlackFinding{
			{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Test", PRURL: "https://github.com/org/repo/pull/100", Category: "error", Message: "⚠️ transient error", DedupKey: ""},
		})
	}

	reporter.Flush(ctx)

	mu.Lock()
	body := receivedBody
	mu.Unlock()

	// All 3 should appear (empty DedupKey is never deduplicated)
	errorCount := strings.Count(body, "transient error")
	if errorCount != 3 {
		t.Errorf("expected 3 'transient error' in message (empty DedupKey), got %d", errorCount)
	}
}

// --- LastReportedAt persistence tests ---

func TestLoadLastReportedAt_JSONFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "last-report-at")
	expected := time.Date(2026, 5, 29, 8, 38, 23, 0, time.UTC)
	state := `{"lastReportedAt":"2026-05-29T08:38:23Z","reportedKeys":["rebase:100","conflict:200"]}`
	if err := os.WriteFile(path, []byte(state+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	logger := discardLogger()
	got, reported := loadLastReportedAt(path, logger)

	if !got.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, got)
	}
	if len(reported) != 2 {
		t.Fatalf("expected 2 dedup keys, got %d", len(reported))
	}
	if !reported["rebase:100"] {
		t.Error("expected rebase:100 in reported map")
	}
	if !reported["conflict:200"] {
		t.Error("expected conflict:200 in reported map")
	}
}

func TestLoadLastReportedAt_PlainTextBackwardCompat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "last-report-at")
	expected := time.Date(2026, 5, 29, 8, 38, 23, 0, time.UTC)
	if err := os.WriteFile(path, []byte("2026-05-29T08:38:23Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	logger := discardLogger()
	got, reported := loadLastReportedAt(path, logger)

	if !got.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, got)
	}
	if len(reported) != 0 {
		t.Errorf("expected empty reported map for old format, got %d entries", len(reported))
	}
}

func TestLoadLastReportedAt_FileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent")

	logger := discardLogger()
	before := time.Now()
	got, reported := loadLastReportedAt(path, logger)
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Errorf("expected time.Now() fallback, got %v (before=%v, after=%v)", got, before, after)
	}
	if len(reported) != 0 {
		t.Errorf("expected empty reported map, got %d entries", len(reported))
	}
}

func TestLoadLastReportedAt_FileCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "last-report-at")
	if err := os.WriteFile(path, []byte("not-a-timestamp"), 0o644); err != nil {
		t.Fatal(err)
	}

	logger := discardLogger()
	before := time.Now()
	got, reported := loadLastReportedAt(path, logger)
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Errorf("expected time.Now() fallback for corrupt file, got %v", got)
	}
	if len(reported) != 0 {
		t.Errorf("expected empty reported map, got %d entries", len(reported))
	}
}

func TestLoadLastReportedAt_FileEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "last-report-at")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	logger := discardLogger()
	before := time.Now()
	got, reported := loadLastReportedAt(path, logger)
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Errorf("expected time.Now() fallback for empty file, got %v", got)
	}
	if len(reported) != 0 {
		t.Errorf("expected empty reported map, got %d entries", len(reported))
	}
}

func TestPersistLastReportedAt_WritesAndReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "last-report-at")
	logger := discardLogger()

	ts := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	reported := map[string]bool{"rebase:100": true, "conflict:200": true}
	persistLastReportedAt(path, ts, reported, logger)

	got, gotReported := loadLastReportedAt(path, logger)
	if !got.Equal(ts) {
		t.Errorf("expected %v after persist+load, got %v", ts, got)
	}
	if len(gotReported) != 2 {
		t.Fatalf("expected 2 dedup keys after persist+load, got %d", len(gotReported))
	}
	if !gotReported["rebase:100"] {
		t.Error("expected rebase:100 in loaded reported map")
	}
	if !gotReported["conflict:200"] {
		t.Error("expected conflict:200 in loaded reported map")
	}
}

func TestPersistLastReportedAt_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "last-report-at")
	logger := discardLogger()

	ts := time.Now()
	persistLastReportedAt(path, ts, make(map[string]bool), logger)

	// Verify directory was created
	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		t.Error("expected directory to be created")
	}
	// Verify file exists and is readable
	got, _ := loadLastReportedAt(path, logger)
	if got.IsZero() {
		t.Error("expected non-zero timestamp after persist")
	}
}

func TestDefaultLastReportAtPath_SameForAllWebhooks(t *testing.T) {
	path1 := defaultLastReportAtPath("https://hooks.slack.com/services/T000/B000/xxx")
	path2 := defaultLastReportAtPath("https://hooks.slack.com/services/T111/B111/yyy")
	pathEmpty := defaultLastReportAtPath("")

	// All paths should be identical — hash was removed
	if path1 != path2 {
		t.Errorf("expected same path for different webhook URLs, got %s and %s", path1, path2)
	}
	if path1 != pathEmpty {
		t.Errorf("expected same path for non-empty and empty webhook URLs, got %s and %s", path1, pathEmpty)
	}
	// Path should end with the plain filename
	if !strings.HasSuffix(path1, lastReportAtFile) {
		t.Errorf("expected path to end with %q, got: %s", lastReportAtFile, path1)
	}
}

func TestSlackReporter_FlushPersistsLastReportedAt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "last-report-at")

	logger := discardLogger()
	reporter := &SlackReporter{
		webhookURL:     ts.URL,
		reported:       make(map[string]bool),
		logger:         logger,
		httpClient:     ts.Client(),
		lastReportedAt: time.Now().Add(-1 * time.Hour),
		stateFilePath:  stateFile,
	}

	before := time.Now()
	reporter.Collect([]SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 1, PRTitle: "Test", Message: "test", DedupKey: "test:1"},
	})
	reporter.Flush(context.Background())
	after := time.Now()

	// Verify lastReportedAt was updated.
	// Flush subtracts a 2-minute safety margin, so lastReportedAt should be ~2min before now.
	safetyMargin := 2 * time.Minute
	if reporter.LastReportedAt().Before(before.Add(-safetyMargin-1*time.Second)) || reporter.LastReportedAt().After(after.Add(-safetyMargin+1*time.Second)) {
		t.Errorf("expected LastReportedAt to be ~now minus 2min safety margin, got %v", reporter.LastReportedAt())
	}

	// Verify the file was written.
	// RFC 3339 truncates to seconds, so the loaded timestamp may be up to 1s off.
	got, gotReported := loadLastReportedAt(stateFile, logger)
	if got.Before(before.Add(-safetyMargin-1*time.Second)) || got.After(after.Add(-safetyMargin+1*time.Second)) {
		t.Errorf("expected persisted timestamp to be ~now minus 2min, got %v (before=%v, after=%v)", got, before, after)
	}
	// Verify the dedup key was persisted
	if !gotReported["test:1"] {
		t.Error("expected dedup key 'test:1' in persisted state")
	}
}

func TestSlackReporter_LastReportedAtSurvivesRestart(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "last-report-at")
	logger := discardLogger()

	// First reporter: flush to persist timestamp
	reporter1 := &SlackReporter{
		webhookURL:     ts.URL,
		reported:       make(map[string]bool),
		logger:         logger,
		httpClient:     ts.Client(),
		lastReportedAt: time.Now().Add(-1 * time.Hour),
		stateFilePath:  stateFile,
	}

	reporter1.Collect([]SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 1, PRTitle: "Test", Message: "test", DedupKey: "test:1"},
	})
	reporter1.Flush(context.Background())
	firstReportTime := reporter1.LastReportedAt()

	// Second reporter: simulate restart by loading from the same file
	lastReportedAt, reported := loadLastReportedAt(stateFile, logger)
	reporter2 := &SlackReporter{
		webhookURL:     ts.URL,
		reported:       reported,
		logger:         logger,
		httpClient:     ts.Client(),
		lastReportedAt: lastReportedAt,
		stateFilePath:  stateFile,
	}

	// The second reporter should have the same lastReportedAt as the first.
	// RFC 3339 truncates to seconds, so compare at second precision.
	firstTrunc := firstReportTime.UTC().Truncate(time.Second)
	secondTrunc := reporter2.LastReportedAt().UTC().Truncate(time.Second)
	if !secondTrunc.Equal(firstTrunc) {
		t.Errorf("expected LastReportedAt to survive restart: first=%v, second=%v",
			firstTrunc, secondTrunc)
	}
}

func TestSlackReporter_DedupKeysSurviveRestart(t *testing.T) {
	var mu sync.Mutex
	postCount := 0
	posts := func() int { mu.Lock(); defer mu.Unlock(); return postCount }
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		postCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "last-report-at")
	logger := discardLogger()

	// First reporter: report rebase and conflict findings
	reporter1 := &SlackReporter{
		webhookURL:     ts.URL,
		reported:       make(map[string]bool),
		logger:         logger,
		httpClient:     ts.Client(),
		lastReportedAt: time.Now().Add(-1 * time.Hour),
		stateFilePath:  stateFile,
	}

	reporter1.Collect([]SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 8365, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/8365", Category: "rebase", Message: "behind main", DedupKey: "rebase:8365"},
		{Owner: "org", Repo: "repo", PRNumber: 8380, PRTitle: "Add feature", PRURL: "https://github.com/org/repo/pull/8380", Category: "conflict", Message: "merge conflicts", DedupKey: "conflict:8380"},
	})
	reporter1.Flush(context.Background())
	if posts() != 1 {
		t.Fatalf("expected 1 POST, got %d", posts())
	}

	// Simulate restart: create new reporter from persisted state
	lastReportedAt, reported := loadLastReportedAt(stateFile, logger)
	reporter2 := &SlackReporter{
		webhookURL:     ts.URL,
		reported:       reported,
		logger:         logger,
		httpClient:     ts.Client(),
		lastReportedAt: lastReportedAt,
		stateFilePath:  stateFile,
	}

	// Same findings should be suppressed — dedup keys survived restart
	reporter2.Collect([]SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 8365, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/8365", Category: "rebase", Message: "behind main", DedupKey: "rebase:8365"},
		{Owner: "org", Repo: "repo", PRNumber: 8380, PRTitle: "Add feature", PRURL: "https://github.com/org/repo/pull/8380", Category: "conflict", Message: "merge conflicts", DedupKey: "conflict:8380"},
	})
	reporter2.Flush(context.Background())
	if posts() != 1 {
		t.Errorf("expected still 1 POST after restart (dedup keys survived), got %d", posts())
	}

	// New finding should still be reported
	reporter2.Collect([]SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 9000, PRTitle: "New PR", PRURL: "https://github.com/org/repo/pull/9000", Category: "rebase", Message: "behind main", DedupKey: "rebase:9000"},
	})
	reporter2.Flush(context.Background())
	if posts() != 2 {
		t.Errorf("expected 2 POSTs (new finding after restart), got %d", posts())
	}
}

func TestMigrateOldHashedFiles(t *testing.T) {
	t.Run("current file exists — old files deleted", func(t *testing.T) {
		dir := t.TempDir()
		stateDir := filepath.Join(dir, "oompa")
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}

		// Create old hashed files
		oldFiles := []string{
			"last-report-at-ff4b1805",
			"last-report-at-abc12345",
		}
		for _, f := range oldFiles {
			if err := os.WriteFile(filepath.Join(stateDir, f), []byte("2026-05-29T10:00:00Z\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// Create the current (non-hashed) file — should NOT be removed
		currentFile := filepath.Join(stateDir, "last-report-at")
		if err := os.WriteFile(currentFile, []byte("2026-05-29T10:00:00Z\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		logger := discardLogger()
		migrateOldHashedFiles(currentFile, logger)

		// Old hashed files should be removed
		for _, f := range oldFiles {
			if _, err := os.Stat(filepath.Join(stateDir, f)); !os.IsNotExist(err) {
				t.Errorf("expected old file %q to be removed", f)
			}
		}

		// Current file should still exist
		if _, err := os.Stat(currentFile); os.IsNotExist(err) {
			t.Error("expected current file to survive migration")
		}
	})

	t.Run("current file missing — first old file renamed to preserve state", func(t *testing.T) {
		dir := t.TempDir()
		stateDir := filepath.Join(dir, "oompa")
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}

		// Create old hashed files with known content
		oldContent := `{"lastReportedAt":"2026-05-29T08:00:00Z","reportedKeys":["rebase:100"]}` + "\n"
		oldFiles := []string{
			"last-report-at-abc12345",
			"last-report-at-ff4b1805",
		}
		for _, f := range oldFiles {
			if err := os.WriteFile(filepath.Join(stateDir, f), []byte(oldContent), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		// Do NOT create the current file — simulate first run after upgrade
		currentFile := filepath.Join(stateDir, "last-report-at")

		logger := discardLogger()
		migrateOldHashedFiles(currentFile, logger)

		// Current file should now exist (renamed from the first old file)
		data, err := os.ReadFile(currentFile)
		if err != nil {
			t.Fatalf("expected current file to exist after migration, got error: %v", err)
		}
		if string(data) != oldContent {
			t.Errorf("expected migrated content %q, got %q", oldContent, string(data))
		}

		// All old hashed files should be gone
		for _, f := range oldFiles {
			if _, err := os.Stat(filepath.Join(stateDir, f)); !os.IsNotExist(err) {
				t.Errorf("expected old file %q to be removed after migration", f)
			}
		}

		// Verify the state is loadable and correct
		ts, reported := loadLastReportedAt(currentFile, logger)
		expected := time.Date(2026, 5, 29, 8, 0, 0, 0, time.UTC)
		if !ts.Equal(expected) {
			t.Errorf("expected timestamp %v, got %v", expected, ts)
		}
		if !reported["rebase:100"] {
			t.Error("expected rebase:100 in reported map after migration")
		}
	})

	t.Run("tmp files are ignored", func(t *testing.T) {
		dir := t.TempDir()
		stateDir := filepath.Join(dir, "oompa")
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}

		// Create a .tmp file that looks like an old hashed file
		tmpFile := filepath.Join(stateDir, "last-report-at-ff4b1805.tmp")
		if err := os.WriteFile(tmpFile, []byte("temp"), 0o644); err != nil {
			t.Fatal(err)
		}

		currentFile := filepath.Join(stateDir, "last-report-at")

		logger := discardLogger()
		migrateOldHashedFiles(currentFile, logger)

		// .tmp file should NOT be touched (not renamed, not deleted)
		if _, err := os.Stat(tmpFile); os.IsNotExist(err) {
			t.Error("expected .tmp file to be left alone")
		}
		// Current file should NOT exist (nothing to migrate)
		if _, err := os.Stat(currentFile); !os.IsNotExist(err) {
			t.Error("expected current file to not exist when only .tmp files present")
		}
	})
}

func TestPersistLastReportedAt_PersistsAllKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "last-report-at")
	logger := discardLogger()

	// Create a large set of keys — all should be persisted since the in-memory
	// map is the bound (maxDedupEntries), not a separate persist cap.
	reported := make(map[string]bool)
	for i := range 700 {
		reported[fmt.Sprintf("key:%d", i)] = true
	}

	ts := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	persistLastReportedAt(path, ts, reported, logger)

	_, loaded := loadLastReportedAt(path, logger)
	if len(loaded) != 700 {
		t.Errorf("expected all 700 keys persisted, got %d", len(loaded))
	}
}
