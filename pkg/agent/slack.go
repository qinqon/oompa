package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	// slackRequestTimeout is the HTTP timeout for Slack webhook requests.
	// Prevents a hanging Slack request from blocking the entire poll loop.
	slackRequestTimeout = 15 * time.Second

	// maxDedupEntries is the maximum number of dedup entries to keep in memory.
	// When exceeded, the map is cleared to prevent unbounded growth in long-running agents.
	// 1000 entries is generous — each entry is a small string key, so memory is negligible.
	maxDedupEntries = 1000
)

// SlackFinding represents a single reportable finding from a poll cycle.
// Findings should have a PR context (PRNumber, PRURL) when available.
type SlackFinding struct {
	PRNumber int    // PR number this finding relates to
	PRTitle  string // PR title for display
	PRURL    string // clickable PR URL
	Category string // "ci", "rebase", "conflict", "review", "error"
	Message  string // Slack mrkdwn formatted message line (e.g. "🔴 <url|name> failed")
	DedupKey string // unique key for dedup (e.g. "ci:sha:checkName")
}

// SlackReporter collects findings and posts them to a Slack webhook.
// Zero external dependencies — uses only Go stdlib net/http + encoding/json.
type SlackReporter struct {
	webhookURL string
	owner      string
	repo       string
	reported   map[string]bool // tracks DedupKeys already reported
	logger     *slog.Logger
	httpClient *http.Client // injectable for testing
}

// NewSlackReporter creates a new reporter. If webhookURL is empty, IsEnabled() returns false.
// A nil logger defaults to slog.Default().
func NewSlackReporter(webhookURL, owner, repo string, logger *slog.Logger) *SlackReporter {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlackReporter{
		webhookURL: webhookURL,
		owner:      owner,
		repo:       repo,
		reported:   make(map[string]bool),
		logger:     logger,
		httpClient: &http.Client{Timeout: slackRequestTimeout},
	}
}

// IsEnabled returns true if the Slack webhook URL is configured.
func (r *SlackReporter) IsEnabled() bool {
	return r.webhookURL != ""
}

// Report deduplicates findings, formats a Slack message, and POSTs it to the webhook.
// No-op if there are no new findings after dedup, or if the reporter is disabled.
func (r *SlackReporter) Report(ctx context.Context, findings []SlackFinding) {
	if !r.IsEnabled() || len(findings) == 0 {
		return
	}

	// Prune dedup map if it has grown too large to prevent unbounded memory growth.
	if len(r.reported) > maxDedupEntries {
		r.logger.Info("pruning Slack dedup map", "entries", len(r.reported))
		clear(r.reported)
	}

	// Dedup: filter out findings already reported
	var newFindings []SlackFinding
	for _, f := range findings {
		if f.DedupKey == "" || !r.reported[f.DedupKey] {
			newFindings = append(newFindings, f)
		}
	}

	if len(newFindings) == 0 {
		return
	}

	body := formatSlackMessage(r.owner, r.repo, newFindings)
	if body == nil {
		return
	}

	if err := postToSlack(ctx, r.httpClient, r.webhookURL, body); err != nil {
		r.logger.Error("failed to post to Slack", "error", err)
		return
	}

	// Mark as reported only after successful POST
	for _, f := range newFindings {
		if f.DedupKey != "" {
			r.reported[f.DedupKey] = true
		}
	}

	r.logger.Info("posted Slack report", "findings", len(newFindings))
}

// slackBlock represents a Slack Block Kit block.
type slackBlock struct {
	Type string     `json:"type"`
	Text *slackText `json:"text,omitempty"`
}

// slackText represents a Slack Block Kit text object.
type slackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// slackMessage represents a Slack webhook message payload.
type slackMessage struct {
	Blocks []slackBlock `json:"blocks"`
}

// formatSlackMessage builds a Slack Block Kit JSON payload grouped by PR.
// Returns nil if there are no findings.
func formatSlackMessage(owner, repo string, findings []SlackFinding) []byte {
	if len(findings) == 0 {
		return nil
	}

	// Group findings by PR number, sorted for deterministic output.
	type prGroup struct {
		prNumber int
		prTitle  string
		prURL    string
		messages []string
	}
	groupMap := make(map[int]*prGroup)
	var order []int

	for _, f := range findings {
		g, ok := groupMap[f.PRNumber]
		if !ok {
			g = &prGroup{
				prNumber: f.PRNumber,
				prTitle:  f.PRTitle,
				prURL:    f.PRURL,
			}
			groupMap[f.PRNumber] = g
			order = append(order, f.PRNumber)
		}
		g.messages = append(g.messages, f.Message)
	}

	// Sort by PR number for deterministic output
	sort.Ints(order)

	var sb strings.Builder
	fmt.Fprintf(&sb, "🏭 oompa — %s/%s\n", owner, repo)

	for _, prNum := range order {
		g := groupMap[prNum]
		sb.WriteString("\n")
		if g.prURL != "" {
			fmt.Fprintf(&sb, "📋 <%s|PR #%d> — %s\n", g.prURL, g.prNumber, g.prTitle)
		} else {
			fmt.Fprintf(&sb, "📋 PR #%d — %s\n", g.prNumber, g.prTitle)
		}
		for _, msg := range g.messages {
			fmt.Fprintf(&sb, "  %s\n", msg)
		}
	}

	msg := slackMessage{
		Blocks: []slackBlock{
			{
				Type: "section",
				Text: &slackText{
					Type: "mrkdwn",
					Text: sb.String(),
				},
			},
		},
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return nil
	}
	return body
}

// postToSlack sends a JSON payload to a Slack Incoming Webhook URL.
func postToSlack(ctx context.Context, client *http.Client, webhookURL string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating Slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("posting to Slack: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("slack webhook returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// prURL returns the GitHub URL for a PR.
func prURL(owner, repo string, prNumber int) string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, prNumber)
}

// commitURL returns the GitHub URL for a commit.
func commitURL(owner, repo, sha string) string {
	return fmt.Sprintf("https://github.com/%s/%s/commit/%s", owner, repo, sha)
}

// issueURL returns the GitHub URL for an issue.
func issueURL(owner, repo string, issueNumber int) string {
	return fmt.Sprintf("https://github.com/%s/%s/issues/%d", owner, repo, issueNumber)
}
