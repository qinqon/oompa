package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFormatSlackMessage_GroupsByPR(t *testing.T) {
	findings := []SlackFinding{
		{PRNumber: 100, PRTitle: "Fix flaky test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 <https://example.com/job/1|e2e-test> failed"},
		{PRNumber: 200, PRTitle: "Add feature", PRURL: "https://github.com/org/repo/pull/200", Category: "conflict", Message: "⚠️ Merge conflicts"},
		{PRNumber: 100, PRTitle: "Fix flaky test", PRURL: "https://github.com/org/repo/pull/100", Category: "rebase", Message: "⚠️ 15 commits behind main"},
	}

	body := formatSlackMessage("org", "repo", findings)
	if body == nil {
		t.Fatal("expected non-nil body")
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(msg.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(msg.Blocks))
	}

	text := msg.Blocks[0].Text.Text

	// PR #100 should appear before PR #200 (sorted by PR number)
	idx100 := strings.Index(text, "PR #100")
	idx200 := strings.Index(text, "PR #200")
	if idx100 == -1 || idx200 == -1 {
		t.Fatalf("expected both PRs in output, got: %s", text)
	}
	if idx100 > idx200 {
		t.Errorf("PR #100 should appear before PR #200")
	}

	// Both findings for PR #100 should be grouped together
	if !strings.Contains(text, "e2e-test") {
		t.Error("expected e2e-test finding")
	}
	if !strings.Contains(text, "15 commits behind main") {
		t.Error("expected rebase finding")
	}
	if !strings.Contains(text, "Merge conflicts") {
		t.Error("expected conflict finding")
	}

	// Header should contain owner/repo
	if !strings.Contains(text, "org/repo") {
		t.Error("expected owner/repo in header")
	}
}

func TestFormatSlackMessage_EmptyFindings(t *testing.T) {
	body := formatSlackMessage("org", "repo", nil)
	if body != nil {
		t.Error("expected nil body for empty findings")
	}

	body = formatSlackMessage("org", "repo", []SlackFinding{})
	if body != nil {
		t.Error("expected nil body for empty slice")
	}
}

func TestSlackReporter_Dedup(t *testing.T) {
	var postCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		postCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := NewSlackReporter(ts.URL, "org", "repo", logger)
	reporter.httpClient = ts.Client()

	findings := []SlackFinding{
		{PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 test failed", DedupKey: "ci:abc123:e2e"},
	}

	ctx := context.Background()

	// First report should send
	reporter.Report(ctx, findings)
	if postCount != 1 {
		t.Errorf("expected 1 POST, got %d", postCount)
	}

	// Second report with same DedupKey should be suppressed
	reporter.Report(ctx, findings)
	if postCount != 1 {
		t.Errorf("expected still 1 POST after dedup, got %d", postCount)
	}
}

func TestSlackReporter_DedupReset(t *testing.T) {
	var postCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		postCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := NewSlackReporter(ts.URL, "org", "repo", logger)
	reporter.httpClient = ts.Client()

	ctx := context.Background()

	// First finding
	reporter.Report(ctx, []SlackFinding{
		{PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 test failed", DedupKey: "ci:abc123:e2e"},
	})
	if postCount != 1 {
		t.Errorf("expected 1 POST, got %d", postCount)
	}

	// Different DedupKey (new SHA) should re-send
	reporter.Report(ctx, []SlackFinding{
		{PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 test failed", DedupKey: "ci:def456:e2e"},
	})
	if postCount != 2 {
		t.Errorf("expected 2 POSTs after new DedupKey, got %d", postCount)
	}
}

func TestSlackReporter_Disabled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := NewSlackReporter("", "org", "repo", logger)

	if reporter.IsEnabled() {
		t.Error("expected IsEnabled() to be false with empty webhook URL")
	}

	// Should not panic or error when disabled
	reporter.Report(context.Background(), []SlackFinding{
		{PRNumber: 100, PRTitle: "Test", Message: "test", DedupKey: "test"},
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

func TestCheckCIStatus_ReportsFailures(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "e2e-test", Status: "completed", Conclusion: "failure", HTMLURL: "https://github.com/org/repo/actions/runs/1/job/1"},
			{ID: 2, Name: "unit-test", Status: "completed", Conclusion: "success"},
		},
		prHeadSHAs: []string{"abc123"},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Agent{
		gh:     gh,
		cfg:    Config{Owner: "org", Repo: "repo", SlackWebhookURL: "http://example.com/webhook"},
		state:  NewState(),
		logger: logger,
	}

	a.state.ActiveIssues["org/repo#100"] = &IssueWork{
		PRNumber:   100,
		IssueTitle: "Fix test",
		Status:     StatusPROpen,
	}

	findings := a.CheckCIStatus(context.Background())

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Category != "ci" {
		t.Errorf("expected category ci, got %s", findings[0].Category)
	}
	if !strings.Contains(findings[0].Message, "e2e-test") {
		t.Errorf("expected message to contain check name, got: %s", findings[0].Message)
	}
	if !strings.Contains(findings[0].DedupKey, "abc123") {
		t.Errorf("expected DedupKey to contain SHA, got: %s", findings[0].DedupKey)
	}
}

func TestCheckRebaseNeeded_ReportsBehind(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "behind",
		prBehind:       true,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Agent{
		gh:     gh,
		cfg:    Config{Owner: "org", Repo: "repo", SlackWebhookURL: "http://example.com/webhook"},
		state:  NewState(),
		logger: logger,
	}

	a.state.ActiveIssues["org/repo#100"] = &IssueWork{
		PRNumber:   100,
		IssueTitle: "Fix test",
		Status:     StatusPROpen,
	}

	findings := a.CheckRebaseNeeded(context.Background())

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Category != "rebase" {
		t.Errorf("expected category rebase, got %s", findings[0].Category)
	}
	if !strings.Contains(findings[0].Message, "behind main") {
		t.Errorf("expected message about behind main, got: %s", findings[0].Message)
	}
}

func TestCheckConflicts_ReportsDirty(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "dirty",
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Agent{
		gh:     gh,
		cfg:    Config{Owner: "org", Repo: "repo", SlackWebhookURL: "http://example.com/webhook"},
		state:  NewState(),
		logger: logger,
	}

	a.state.ActiveIssues["org/repo#100"] = &IssueWork{
		PRNumber:   100,
		IssueTitle: "Fix test",
		Status:     StatusPROpen,
	}

	findings := a.CheckConflicts(context.Background())

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Category != "conflict" {
		t.Errorf("expected category conflict, got %s", findings[0].Category)
	}
	if !strings.Contains(findings[0].Message, "merge conflicts") {
		t.Errorf("expected message about merge conflicts, got: %s", findings[0].Message)
	}
}

func TestCheckNewReviews_ReportsComments(t *testing.T) {
	gh := &mockGitHubClient{
		prComments: []ReviewComment{
			{ID: 10, User: "reviewer1", Body: "Please fix this"},
			{ID: 11, User: "reviewer2", Body: "LGTM"},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Agent{
		gh:     gh,
		cfg:    Config{Owner: "org", Repo: "repo", SlackWebhookURL: "http://example.com/webhook"},
		state:  NewState(),
		logger: logger,
	}

	a.state.ActiveIssues["org/repo#100"] = &IssueWork{
		PRNumber:   100,
		IssueTitle: "Fix test",
		Status:     StatusPROpen,
	}

	findings := a.CheckNewReviews(context.Background())

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Category != "review" {
		t.Errorf("expected category review, got %s", findings[0].Category)
	}
	if !strings.Contains(findings[0].Message, "2 new review") {
		t.Errorf("expected message about 2 new reviews, got: %s", findings[0].Message)
	}
}

func TestCheckCIStatus_NoFailures(t *testing.T) {
	gh := &mockGitHubClient{
		checkRuns: []CheckRun{
			{ID: 1, Name: "e2e-test", Status: "completed", Conclusion: "success"},
		},
		prHeadSHAs: []string{"abc123"},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Agent{
		gh:     gh,
		cfg:    Config{Owner: "org", Repo: "repo", SlackWebhookURL: "http://example.com/webhook"},
		state:  NewState(),
		logger: logger,
	}

	a.state.ActiveIssues["org/repo#100"] = &IssueWork{
		PRNumber:   100,
		IssueTitle: "Fix test",
		Status:     StatusPROpen,
	}

	findings := a.CheckCIStatus(context.Background())

	if len(findings) != 0 {
		t.Errorf("expected 0 findings for passing CI, got %d", len(findings))
	}
}

func TestCheckRebaseNeeded_Clean(t *testing.T) {
	gh := &mockGitHubClient{
		mergeableState: "clean",
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Agent{
		gh:     gh,
		cfg:    Config{Owner: "org", Repo: "repo", SlackWebhookURL: "http://example.com/webhook"},
		state:  NewState(),
		logger: logger,
	}

	a.state.ActiveIssues["org/repo#100"] = &IssueWork{
		PRNumber:   100,
		IssueTitle: "Fix test",
		Status:     StatusPROpen,
	}

	findings := a.CheckRebaseNeeded(context.Background())

	if len(findings) != 0 {
		t.Errorf("expected 0 findings for clean state, got %d", len(findings))
	}
}

func TestSlackReporter_EmptyDedupKey(t *testing.T) {
	var postCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		postCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := NewSlackReporter(ts.URL, "org", "repo", logger)
	reporter.httpClient = ts.Client()

	ctx := context.Background()

	// Findings with empty DedupKey should always be sent
	findings := []SlackFinding{
		{PRNumber: 100, PRTitle: "Test", Message: "test", DedupKey: ""},
	}

	reporter.Report(ctx, findings)
	if postCount != 1 {
		t.Errorf("expected 1 POST, got %d", postCount)
	}

	reporter.Report(ctx, findings)
	if postCount != 2 {
		t.Errorf("expected 2 POSTs (empty DedupKey never suppressed), got %d", postCount)
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
