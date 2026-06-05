package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// FakeSlack is an httptest server that records received Slack webhook payloads.
type FakeSlack struct {
	mu       sync.Mutex
	t        *testing.T
	server   *httptest.Server
	payloads []json.RawMessage
}

// NewFakeSlack creates a fake Slack webhook receiver.
func NewFakeSlack(t *testing.T) *FakeSlack {
	fs := &FakeSlack{t: t}

	fs.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		fs.mu.Lock()
		fs.payloads = append(fs.payloads, json.RawMessage(body))
		fs.mu.Unlock()

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	t.Cleanup(fs.server.Close)
	return fs
}

// URL returns the webhook URL.
func (fs *FakeSlack) URL() string {
	return fs.server.URL
}

// Payloads returns a copy of all received payloads.
func (fs *FakeSlack) Payloads() []json.RawMessage {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	result := make([]json.RawMessage, len(fs.payloads))
	copy(result, fs.payloads)
	return result
}

// TestE2E_SlackReporting verifies that oompa sends Slack webhook reports
// when CI failures are detected (via report-only checks).
// This scenario catches regressions in: #195, #202, #205.
func TestE2E_SlackReporting(t *testing.T) {
	const (
		owner = "testowner"
		repo  = "testrepo"
		label = "good-for-ai"
	)

	fg := NewFakeGitHub(t, owner, repo)
	slack := NewFakeSlack(t)

	// Seed an issue with an existing PR that has a failing check run.
	fg.SeedIssue(FakeIssue{
		Number: 30,
		Title:  "Slack test issue",
		Body:   "Test Slack reporting.",
		Labels: []map[string]any{{"name": label}},
	})

	fg.SeedPR(FakePR{
		Number: 400,
		Title:  "Slack test PR",
		Body:   "Fixes #30",
		State:  "open",
		Head:   "ai/issue-30",
		Base:   "main",
	})

	headSHA := "slack-test-sha-001"
	fg.SetPRHeadSHA(400, headSHA)

	// Seed a failing check run — report-only CI check should detect this.
	fg.SeedCheckRun(headSHA, FakeCheckRun{
		ID:         7001,
		Name:       "build",
		Status:     "completed",
		Conclusion: "failure",
		HTMLURL:    "https://github.com/testowner/testrepo/runs/7001",
	})

	h := NewHarness(t, owner, repo, label)
	pushBranchToBare(t, h, "ai/issue-30")

	// Run oompa with Slack webhook and with CI NOT in reactions
	// (so it runs as report-only).
	stdout, stderr, err := h.RunOompa(fg.URL(), RunOompaOpts{
		WatchPRs:  []int{400},
		Reactions: []string{"reviews"}, // ci NOT in reactions → report-only mode
		ExtraEnv:  []string{"OOMPA_SLACK_WEBHOOK=" + slack.URL()},
	})
	if err != nil {
		t.Fatalf("oompa exited with error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// === Assertions ===

	// 1. At least one Slack payload was delivered.
	payloads := slack.Payloads()
	if len(payloads) == 0 {
		t.Fatal("expected at least 1 Slack webhook payload, got 0")
	}

	// 2. Payload contains correct structure (Block Kit JSON).
	var msg struct {
		Blocks []struct {
			Type string `json:"type"`
			Text *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"text,omitempty"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(payloads[0], &msg); err != nil {
		t.Fatalf("failed to parse Slack payload as JSON: %v\npayload: %s", err, string(payloads[0]))
	}
	if len(msg.Blocks) == 0 {
		t.Fatal("expected Slack message to contain blocks")
	}

	// 3. Header block mentions "oompa report".
	foundHeader := false
	for _, block := range msg.Blocks {
		if block.Type == "header" && block.Text != nil && strings.Contains(block.Text.Text, "oompa report") {
			foundHeader = true
			break
		}
	}
	if !foundHeader {
		t.Error("expected Slack message header containing 'oompa report'")
	}

	// 4. Content mentions the failing check run.
	payload := string(payloads[0])
	if !strings.Contains(payload, "build") {
		t.Error("expected Slack payload to mention the 'build' check run")
	}
}

// TestE2E_SlackReporting_DedupWithinBatch verifies that findings within
// the same flush batch are deduplicated (catches #195).
func TestE2E_SlackReporting_DedupWithinBatch(t *testing.T) {
	const (
		owner = "testowner"
		repo  = "testrepo"
		label = "good-for-ai"
	)

	fg := NewFakeGitHub(t, owner, repo)
	slack := NewFakeSlack(t)

	fg.SeedIssue(FakeIssue{
		Number: 31,
		Title:  "Slack dedup test",
		Body:   "Test dedup.",
		Labels: []map[string]any{{"name": label}},
	})

	fg.SeedPR(FakePR{
		Number: 401,
		Title:  "Slack dedup PR",
		Body:   "Fixes #31",
		State:  "open",
		Head:   "ai/issue-31",
		Base:   "main",
	})

	headSHA := "slack-dedup-sha-001"
	fg.SetPRHeadSHA(401, headSHA)

	// Seed two failing check runs with the same name to exercise in-batch dedup.
	// Both should collapse into a single finding in the Slack message.
	fg.SeedCheckRun(headSHA, FakeCheckRun{
		ID:         7010,
		Name:       "unit-tests",
		Status:     "completed",
		Conclusion: "failure",
	})
	fg.SeedCheckRun(headSHA, FakeCheckRun{
		ID:         7011,
		Name:       "unit-tests",
		Status:     "completed",
		Conclusion: "failure",
	})

	// Set PR as behind to also trigger rebase finding — tests multi-finding dedup.
	fg.SetPRMergeState(401, "behind")

	h := NewHarness(t, owner, repo, label)
	pushBranchToBare(t, h, "ai/issue-31")

	// Use RunOompaOpts.Reactions to enable ONLY reviews. This means CI, rebase,
	// and conflict checks run in report-only mode and findings go to Slack.
	stdout, stderr, err := h.RunOompa(fg.URL(), RunOompaOpts{
		WatchPRs:  []int{401},
		Reactions: []string{"reviews"}, // only reviews active → ci runs as report-only
		ExtraEnv:  []string{"OOMPA_SLACK_WEBHOOK=" + slack.URL()},
	})
	if err != nil {
		t.Fatalf("oompa exited with error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	payloads := slack.Payloads()
	if len(payloads) == 0 {
		t.Fatal("expected at least 1 Slack webhook payload")
	}

	// Verify the payload mentions the check run.
	payload := string(payloads[0])
	if !strings.Contains(payload, "unit-tests") {
		t.Error("expected Slack payload to mention 'unit-tests'")
	}

	// Two check runs with the same name were seeded. The dedup logic should
	// collapse them into a single finding. Assert structurally on the parsed
	// blocks rather than string-counting (more robust against format changes).
	var dedupMsg struct {
		Blocks []struct {
			Type string `json:"type"`
			Text *struct {
				Text string `json:"text"`
			} `json:"text,omitempty"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(payloads[0], &dedupMsg); err != nil {
		t.Fatalf("failed to parse Slack dedup payload: %v", err)
	}
	// Count how many section blocks mention "unit-tests" — should be exactly 1
	// after dedup (the detail section). The header and divider blocks don't count.
	unitTestSections := 0
	for _, block := range dedupMsg.Blocks {
		if block.Type == "section" && block.Text != nil && strings.Contains(block.Text.Text, "unit-tests") {
			unitTestSections++
		}
	}
	if unitTestSections != 1 {
		t.Errorf("expected exactly 1 section block mentioning 'unit-tests' after dedup, got %d", unitTestSections)
	}
}
