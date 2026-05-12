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
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// slackRequestTimeout is the HTTP timeout for Slack webhook requests.
	// Prevents a hanging Slack request from blocking the entire poll loop.
	slackRequestTimeout = 15 * time.Second

	// maxDedupEntries is the maximum number of dedup entries to keep in memory.
	// When exceeded, the map is cleared to prevent unbounded growth in long-running agents.
	// 1000 entries is generous — each entry is a small string key, so memory is negligible.
	maxDedupEntries = 1000

	// maxSlackBlockTextLen is the maximum text length for a single Slack Block Kit
	// section block. Slack enforces a 3000-character limit per block text field.
	maxSlackBlockTextLen = 3000

	// maxSlackBlocks is the maximum number of blocks Slack accepts per message.
	// Messages exceeding this limit are rejected by the API.
	maxSlackBlocks = 50
)

// SlackFinding represents a single reportable finding from a poll cycle.
// Findings should have a PR context (PRNumber, PRURL) when available.
type SlackFinding struct {
	Owner    string // repository owner (e.g. "ovn-kubernetes")
	Repo     string // repository name (e.g. "ovn-kubernetes")
	PRNumber int    // PR number this finding relates to
	PRTitle  string // PR title for display
	PRURL    string // clickable PR URL
	Category string // "ci", "rebase", "conflict", "review", "error"
	Message  string // Slack mrkdwn formatted message line (e.g. "🔴 <url|name> failed")
	DedupKey string // unique key for dedup (e.g. "ci:sha:checkName")
}

// SlackReporter collects findings from multiple projects and posts a consolidated
// Slack message. Thread-safe: multiple agent goroutines can call Collect concurrently.
// Zero external dependencies — uses only Go stdlib net/http + encoding/json.
type SlackReporter struct {
	webhookURL string
	reported   map[string]bool // tracks DedupKeys already reported
	logger     *slog.Logger
	httpClient *http.Client // injectable for testing

	flushMu  sync.Mutex     // serializes Flush calls (protects reported map)
	mu       sync.Mutex     // protects pending
	pending  []SlackFinding // findings collected since last Flush
}

// NewSlackReporter creates a new reporter. If webhookURL is empty, IsEnabled() returns false.
// A nil logger defaults to slog.Default().
func NewSlackReporter(webhookURL string, logger *slog.Logger) *SlackReporter {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlackReporter{
		webhookURL: webhookURL,
		reported:   make(map[string]bool),
		logger:     logger,
		httpClient: &http.Client{Timeout: slackRequestTimeout},
	}
}

// IsEnabled returns true if the Slack webhook URL is configured.
func (r *SlackReporter) IsEnabled() bool {
	return r.webhookURL != ""
}

// Collect appends findings to the pending buffer. Thread-safe for concurrent callers.
// Findings accumulate until Flush is called.
func (r *SlackReporter) Collect(findings []SlackFinding) {
	if len(findings) == 0 {
		return
	}
	r.mu.Lock()
	r.pending = append(r.pending, findings...)
	r.mu.Unlock()
}

// Flush deduplicates all pending findings, formats a single consolidated Slack message
// across all projects, and POSTs it to the webhook. Clears the pending buffer afterward.
// No-op if there are no new findings after dedup, or if the reporter is disabled.
func (r *SlackReporter) Flush(ctx context.Context) {
	if !r.IsEnabled() {
		return
	}

	// Serialize Flush calls to protect the reported map from concurrent access.
	// The pending buffer has its own mutex, but reported is only accessed in Flush.
	r.flushMu.Lock()
	defer r.flushMu.Unlock()

	// Atomically drain the pending buffer
	r.mu.Lock()
	findings := r.pending
	r.pending = nil
	r.mu.Unlock()

	if len(findings) == 0 {
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

	body := formatSlackMessage(newFindings)
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

// Report deduplicates findings, formats a Slack message, and POSTs it to the webhook.
// This is the single-call path used in single-repo mode (collect + flush in one step).
// No-op if there are no new findings after dedup, or if the reporter is disabled.
func (r *SlackReporter) Report(ctx context.Context, findings []SlackFinding) {
	if !r.IsEnabled() || len(findings) == 0 {
		return
	}
	r.Collect(findings)
	r.Flush(ctx)
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

// projectKey returns the "owner/repo" string for grouping.
func projectKey(owner, repo string) string {
	return owner + "/" + repo
}

// formatSlackMessage builds a Slack Block Kit JSON payload grouped by project and then by PR.
// Returns nil if there are no findings.
//
// Format:
//
//	🏭 oompa report — N projects with activity
//
//	*<repo-link|owner/repo>* (M PRs)
//	📋 <pr-link|PR #N> — title
//	  🔴 <job-link|check-name> — failed
//	  ⚠️ behind main
//
// Each project gets a header section + detail section + divider.
func formatSlackMessage(findings []SlackFinding) []byte {
	if len(findings) == 0 {
		return nil
	}

	// Group findings by project, then by PR number.
	type prGroup struct {
		prNumber int
		prTitle  string
		prURL    string
		messages []string
	}
	type projectGroup struct {
		owner    string
		repo     string
		prs      map[int]*prGroup
		prOrder  []int
		prCount  int
	}

	projects := make(map[string]*projectGroup)
	var projectOrder []string

	for _, f := range findings {
		pk := projectKey(f.Owner, f.Repo)
		pg, ok := projects[pk]
		if !ok {
			pg = &projectGroup{
				owner: f.Owner,
				repo:  f.Repo,
				prs:   make(map[int]*prGroup),
			}
			projects[pk] = pg
			projectOrder = append(projectOrder, pk)
		}

		pr, ok := pg.prs[f.PRNumber]
		if !ok {
			pr = &prGroup{
				prNumber: f.PRNumber,
				prTitle:  f.PRTitle,
				prURL:    f.PRURL,
			}
			pg.prs[f.PRNumber] = pr
			pg.prOrder = append(pg.prOrder, f.PRNumber)
			pg.prCount++
		}
		pr.messages = append(pr.messages, f.Message)
	}

	// Sort projects alphabetically for deterministic output
	sort.Strings(projectOrder)

	// Sort PRs within each project
	for _, pg := range projects {
		sort.Ints(pg.prOrder)
	}

	// Build blocks
	var blocks []slackBlock

	// Header block
	headerText := fmt.Sprintf("🏭 oompa report — %d project(s) with activity", len(projects))
	blocks = append(blocks, slackBlock{
		Type: "header",
		Text: &slackText{
			Type: "plain_text",
			Text: headerText,
		},
	})

	// Per-project blocks
	for _, pk := range projectOrder {
		pg := projects[pk]

		// Project header section
		repoLink := fmt.Sprintf("https://github.com/%s/%s", pg.owner, pg.repo)
		projectHeader := fmt.Sprintf("*<%s|%s/%s>* (%d PR(s))", repoLink, pg.owner, pg.repo, pg.prCount)
		blocks = append(blocks, slackBlock{
			Type: "section",
			Text: &slackText{
				Type: "mrkdwn",
				Text: projectHeader,
			},
		})

		// Project detail section — all PRs and their findings
		var sb strings.Builder
		for _, prNum := range pg.prOrder {
			pr := pg.prs[prNum]
			if pr.prURL != "" {
				fmt.Fprintf(&sb, "📋 <%s|PR #%d> — %s\n", pr.prURL, pr.prNumber, pr.prTitle)
			} else {
				fmt.Fprintf(&sb, "📋 PR #%d — %s\n", pr.prNumber, pr.prTitle)
			}
			for _, msg := range pr.messages {
				fmt.Fprintf(&sb, "  %s\n", msg)
			}
		}

		detailText := sb.String()
		// Split into multiple blocks if exceeding Slack's 3000-char limit
		for detailText != "" {
			chunk := detailText
			if len(chunk) > maxSlackBlockTextLen {
				// Find the last newline within the limit to avoid cutting mid-line
				cutIdx := strings.LastIndex(chunk[:maxSlackBlockTextLen], "\n")
				if cutIdx < 0 {
					// No newline found — hard-cut at the limit on a valid UTF-8 boundary
					cut := maxSlackBlockTextLen
					for cut > 0 && !utf8.ValidString(detailText[:cut]) {
						cut--
					}
					if cut == 0 {
						// Malformed input — skip remaining text to avoid infinite loop
						break
					}
					chunk = detailText[:cut]
				} else {
					chunk = detailText[:cutIdx+1]
				}
			}
			blocks = append(blocks, slackBlock{
				Type: "section",
				Text: &slackText{
					Type: "mrkdwn",
					Text: chunk,
				},
			})
			detailText = detailText[len(chunk):]
		}

		// Divider between projects
		blocks = append(blocks, slackBlock{Type: "divider"})
	}

	// Remove trailing divider
	if len(blocks) > 0 && blocks[len(blocks)-1].Type == "divider" {
		blocks = blocks[:len(blocks)-1]
	}

	// Slack rejects messages with more than 50 blocks. Truncate and add an
	// overflow notice so the message is still delivered.
	if len(blocks) > maxSlackBlocks {
		blocks = blocks[:maxSlackBlocks-1]
		blocks = append(blocks, slackBlock{
			Type: "section",
			Text: &slackText{
				Type: "mrkdwn",
				Text: "⚠️ _Message truncated — too many findings to fit in one Slack message._",
			},
		})
	}

	msg := slackMessage{Blocks: blocks}
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
