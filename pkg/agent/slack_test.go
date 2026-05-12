package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestFormatSlackMessage_GroupsByPR(t *testing.T) {
	findings := []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix flaky test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 <https://example.com/job/1|e2e-test> failed"},
		{Owner: "org", Repo: "repo", PRNumber: 200, PRTitle: "Add feature", PRURL: "https://github.com/org/repo/pull/200", Category: "conflict", Message: "⚠️ Merge conflicts"},
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix flaky test", PRURL: "https://github.com/org/repo/pull/100", Category: "rebase", Message: "⚠️ 15 commits behind main"},
	}

	body := formatSlackMessage(findings)
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

func TestFormatSlackMessage_EmptyFindings(t *testing.T) {
	body := formatSlackMessage(nil)
	if body != nil {
		t.Error("expected nil body for empty findings")
	}

	body = formatSlackMessage([]SlackFinding{})
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

	body := formatSlackMessage(findings)
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

	body := formatSlackMessage(findings)
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

	body := formatSlackMessage(findings)
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

	body := formatSlackMessage(findings)
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
	var postCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		postCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := NewSlackReporter(ts.URL, logger)
	reporter.httpClient = ts.Client()

	findings := []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 test failed", DedupKey: "ci:abc123:e2e"},
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
	reporter := NewSlackReporter(ts.URL, logger)
	reporter.httpClient = ts.Client()

	ctx := context.Background()

	// First finding
	reporter.Report(ctx, []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 test failed", DedupKey: "ci:abc123:e2e"},
	})
	if postCount != 1 {
		t.Errorf("expected 1 POST, got %d", postCount)
	}

	// Different DedupKey (new SHA) should re-send
	reporter.Report(ctx, []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org/repo/pull/100", Category: "ci", Message: "🔴 test failed", DedupKey: "ci:def456:e2e"},
	})
	if postCount != 2 {
		t.Errorf("expected 2 POSTs after new DedupKey, got %d", postCount)
	}
}

func TestSlackReporter_Disabled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := NewSlackReporter("", logger)

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
	if findings[0].Owner != "org" || findings[0].Repo != "repo" {
		t.Errorf("expected Owner/Repo org/repo, got %s/%s", findings[0].Owner, findings[0].Repo)
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
	if findings[0].Owner != "org" || findings[0].Repo != "repo" {
		t.Errorf("expected Owner/Repo org/repo, got %s/%s", findings[0].Owner, findings[0].Repo)
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
	if findings[0].Owner != "org" || findings[0].Repo != "repo" {
		t.Errorf("expected Owner/Repo org/repo, got %s/%s", findings[0].Owner, findings[0].Repo)
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
	if findings[0].Owner != "org" || findings[0].Repo != "repo" {
		t.Errorf("expected Owner/Repo org/repo, got %s/%s", findings[0].Owner, findings[0].Repo)
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
	reporter := NewSlackReporter(ts.URL, logger)
	reporter.httpClient = ts.Client()

	ctx := context.Background()

	// Findings with empty DedupKey should always be sent
	findings := []SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Test", Message: "test", DedupKey: ""},
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

func TestSlackReporter_CollectAndFlush(t *testing.T) {
	var postCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		postCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := NewSlackReporter(ts.URL, logger)
	reporter.httpClient = ts.Client()

	ctx := context.Background()

	// Collect findings from multiple projects
	reporter.Collect([]SlackFinding{
		{Owner: "org1", Repo: "repo1", PRNumber: 100, PRTitle: "Fix test", PRURL: "https://github.com/org1/repo1/pull/100", Category: "ci", Message: "🔴 failed", DedupKey: "ci:abc:e2e"},
	})
	reporter.Collect([]SlackFinding{
		{Owner: "org2", Repo: "repo2", PRNumber: 200, PRTitle: "Add feature", PRURL: "https://github.com/org2/repo2/pull/200", Category: "conflict", Message: "⚠️ conflicts", DedupKey: "conflict:200"},
	})

	// No POST until Flush
	if postCount != 0 {
		t.Errorf("expected 0 POSTs before Flush, got %d", postCount)
	}

	// Flush should send a single consolidated message
	reporter.Flush(ctx)
	if postCount != 1 {
		t.Errorf("expected 1 POST after Flush, got %d", postCount)
	}

	// Pending should be drained
	reporter.mu.Lock()
	if len(reporter.pending) != 0 {
		t.Errorf("expected pending to be empty after Flush, got %d", len(reporter.pending))
	}
	reporter.mu.Unlock()

	// Flushing again with no new findings should not POST
	reporter.Flush(ctx)
	if postCount != 1 {
		t.Errorf("expected still 1 POST after empty Flush, got %d", postCount)
	}
}

func TestSlackReporter_CollectConcurrent(t *testing.T) {
	var postCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		postCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := NewSlackReporter(ts.URL, logger)
	reporter.httpClient = ts.Client()

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
	if postCount != 1 {
		t.Errorf("expected 1 POST after Flush, got %d", postCount)
	}
}

func TestSlackReporter_FlushDedup(t *testing.T) {
	var postCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		postCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := NewSlackReporter(ts.URL, logger)
	reporter.httpClient = ts.Client()

	ctx := context.Background()

	// First collect+flush
	reporter.Collect([]SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Test", Message: "test", DedupKey: "ci:abc:e2e"},
	})
	reporter.Flush(ctx)
	if postCount != 1 {
		t.Errorf("expected 1 POST, got %d", postCount)
	}

	// Second collect+flush with same DedupKey — should be suppressed
	reporter.Collect([]SlackFinding{
		{Owner: "org", Repo: "repo", PRNumber: 100, PRTitle: "Test", Message: "test", DedupKey: "ci:abc:e2e"},
	})
	reporter.Flush(ctx)
	if postCount != 1 {
		t.Errorf("expected still 1 POST after dedup, got %d", postCount)
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

	body := formatSlackMessage(findings)
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

	body := formatSlackMessage(findings)
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

	body := formatSlackMessage(findings)
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
