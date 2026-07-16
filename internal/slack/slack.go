// Package slack posts per-cycle finding reports to a Slack webhook, with
// cross-restart dedup state persisted under the user state directory.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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

	// lastReportAtFile is the filename (under stateDir) where the last successful
	// Slack report timestamp is persisted. Follows XDG Base Directory spec for
	// state data: ~/.local/state/oompa/last-report-at
	lastReportAtFile = "last-report-at"
	defaultStateDir  = ".local/state/oompa"
)

// reportState is the JSON-serializable state persisted to the last-report-at file.
// It contains both the timestamp and the dedup keys to survive restarts.
type reportState struct {
	LastReportedAt string   `json:"lastReportedAt"`
	ReportedKeys   []string `json:"reportedKeys"`
}

// Finding represents a single reportable finding from a poll cycle.
// Findings should have a PR context (PRNumber, PRURL) when available.
type Finding struct {
	Owner    string // repository owner (e.g. "ovn-kubernetes")
	Repo     string // repository name (e.g. "ovn-kubernetes")
	PRNumber int    // PR number this finding relates to
	PRTitle  string // PR title for display
	PRURL    string // clickable PR URL
	Category string // "ci", "rebase", "conflict", "review", "error"
	Message  string // Slack mrkdwn formatted message line (e.g. "🔴 <url|name> failed")
	DedupKey string // unique key for dedup (e.g. "ci:sha:checkName")
}

// Reporter collects findings from multiple projects and posts a consolidated
// Slack message. Thread-safe: multiple agent goroutines can call Collect concurrently.
// Zero external dependencies — uses only Go stdlib net/http + encoding/json.
type Reporter struct {
	webhookURL     string
	version        string          // build version (short commit SHA) included in report header
	reported       map[string]bool // tracks DedupKeys already reported
	logger         *slog.Logger
	httpClient     *http.Client // injectable for testing
	lastReportedAt time.Time    // when findings were last successfully flushed to Slack
	stateFilePath  string       // path to the persisted last-report-at file

	flushMu sync.Mutex // serializes Flush calls (protects reported map)
	mu      sync.Mutex // protects pending
	pending []Finding  // findings collected since last Flush
}

// NewReporter creates a new reporter. If webhookURL is empty, IsEnabled() returns false.
// A nil logger defaults to slog.Default(). The version string (typically a short commit SHA)
// is included in the Slack report header to identify which build generated the report.
//
// On construction, reads ~/.local/state/oompa/last-report-at to restore LastReportedAt
// across restarts. If the file is missing or unparseable, falls back to time.Now()
// (first cycle is silent baseline, subsequent cycles report changes).
func NewReporter(webhookURL, version string, logger *slog.Logger) *Reporter {
	if logger == nil {
		logger = slog.Default()
	}

	stateFilePath := defaultLastReportAtPath(webhookURL)

	// Migrate: remove old hashed state files (e.g. last-report-at-ff4b1805)
	migrateOldHashedFiles(stateFilePath, logger)

	lastReportedAt, reported := loadLastReportedAt(stateFilePath, logger)

	return &Reporter{
		webhookURL:     webhookURL,
		version:        version,
		reported:       reported,
		logger:         logger,
		httpClient:     &http.Client{Timeout: slackRequestTimeout},
		lastReportedAt: lastReportedAt,
		stateFilePath:  stateFilePath,
	}
}

// LastReportedAt returns the timestamp of the last successful Slack report flush.
// Report-only check methods use this to filter out stale findings.
// Thread-safe: protects against concurrent reads while Flush() updates the field.
func (r *Reporter) LastReportedAt() time.Time {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	return r.lastReportedAt
}

// defaultLastReportAtPath returns the default path for the last-report-at state file,
// following XDG Base Directory spec for state data.
// Uses $XDG_STATE_HOME/oompa/ if set, otherwise ~/.local/state/oompa/.
// The webhookURL parameter is accepted for API compatibility but ignored —
// only one oompa instance runs per machine, so hashing the URL is unnecessary.
func defaultLastReportAtPath(_ string) string {
	// Honor XDG_STATE_HOME per the XDG Base Directory spec.
	if xdgState := os.Getenv("XDG_STATE_HOME"); xdgState != "" {
		return filepath.Join(xdgState, "oompa", lastReportAtFile)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback: write directly to tmpdir with prefix to avoid shared-directory
		// permission conflicts on multi-user systems.
		return filepath.Join(os.TempDir(), "oompa-"+lastReportAtFile)
	}
	return filepath.Join(home, defaultStateDir, lastReportAtFile)
}

// migrateOldHashedFiles migrates old hashed state files (e.g. last-report-at-ff4b1805)
// from the state directory. These were created by a previous version that hashed the
// webhook URL into the filename.
//
// If the current (non-hashed) state file does not exist, the first old hashed file
// found is renamed to the current path to preserve the last reported timestamp.
// Any remaining old hashed files are deleted.
func migrateOldHashedFiles(currentPath string, logger *slog.Logger) {
	dir := filepath.Dir(currentPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // Directory doesn't exist yet — nothing to migrate
	}

	// Check whether the current (non-hashed) state file already exists.
	// If it does, we only need to clean up old files. If not, we rename
	// the first old file to preserve state across the upgrade.
	_, statErr := os.Stat(currentPath)
	currentExists := statErr == nil

	promoted := false
	for _, entry := range entries {
		name := entry.Name()
		// Match files like "last-report-at-ff4b1805" but not "last-report-at" itself
		// or "last-report-at.tmp"
		if !strings.HasPrefix(name, lastReportAtFile+"-") || name == lastReportAtFile {
			continue
		}
		// Skip .tmp files left over from atomic writes
		if strings.HasSuffix(name, ".tmp") {
			continue
		}
		oldPath := filepath.Join(dir, name)
		if !currentExists && !promoted {
			// Rename the first old file to the current path to preserve state
			if err := os.Rename(oldPath, currentPath); err != nil {
				logger.Warn("failed to rename old hashed state file", "from", oldPath, "to", currentPath, "error", err)
			} else {
				logger.Info("migrated old hashed state file", "from", oldPath, "to", currentPath)
				promoted = true
			}
			continue
		}
		if err := os.Remove(oldPath); err != nil {
			logger.Warn("failed to remove old hashed state file", "path", oldPath, "error", err)
		} else {
			logger.Info("removed old hashed state file", "path", oldPath)
		}
	}
}

// loadLastReportedAt reads and parses the persisted state from the state file.
// Returns (time.Now(), empty map) if the file is missing, empty, or unparseable.
//
// Backward compatible: tries JSON first, then falls back to plain RFC 3339
// timestamp (old format before dedup keys were persisted).
func loadLastReportedAt(path string, logger *slog.Logger) (lastReported time.Time, reported map[string]bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		// File missing (first run) or unreadable — use time.Now() as baseline
		return time.Now(), make(map[string]bool)
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return time.Now(), make(map[string]bool)
	}

	// Try JSON format first (new format with dedup keys)
	var state reportState
	if err := json.Unmarshal(data, &state); err == nil && state.LastReportedAt != "" {
		t, err := time.Parse(time.RFC3339, state.LastReportedAt)
		if err != nil {
			logger.Warn("unparseable lastReportedAt in JSON state file, using time.Now() as baseline", "path", path, "error", err)
			return time.Now(), make(map[string]bool)
		}
		reported := make(map[string]bool, len(state.ReportedKeys))
		for _, key := range state.ReportedKeys {
			reported[key] = true
		}
		return t, reported
	}

	// Fall back to plain RFC 3339 timestamp (old format)
	t, err := time.Parse(time.RFC3339, content)
	if err != nil {
		logger.Warn("unparseable last-report-at file, using time.Now() as baseline", "path", path, "content", content, "error", err)
		return time.Now(), make(map[string]bool)
	}

	return t, make(map[string]bool)
}

// persistLastReportedAt writes the timestamp and dedup keys to the state file as JSON.
// Creates the directory if it doesn't exist. Uses atomic write (temp file + rename)
// to prevent corruption if the process is killed mid-write.
// The in-memory reported map is already bounded by maxDedupEntries (cleared when
// exceeded in Flush), so persisting all keys is safe and avoids pruning bias.
func persistLastReportedAt(path string, t time.Time, reported map[string]bool, logger *slog.Logger) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Error("failed to create state directory", "dir", dir, "error", err)
		return
	}

	keys := make([]string, 0, len(reported))
	for k := range reported {
		keys = append(keys, k)
	}
	// Sort for deterministic file output (no cap needed — the in-memory map
	// is already bounded by maxDedupEntries, cleared when exceeded in Flush).
	sort.Strings(keys)

	state := reportState{
		LastReportedAt: t.UTC().Format(time.RFC3339),
		ReportedKeys:   keys,
	}
	data, err := json.Marshal(state)
	if err != nil {
		logger.Error("failed to marshal report state", "error", err)
		return
	}
	data = append(data, '\n')

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		logger.Error("failed to write temporary last-report-at file", "path", tmpPath, "error", err)
		return
	}

	if err := os.Rename(tmpPath, path); err != nil {
		logger.Error("failed to rename last-report-at file", "from", tmpPath, "to", path, "error", err)
		_ = os.Remove(tmpPath)
	}
}

// IsEnabled returns true if the Slack webhook URL is configured.
func (r *Reporter) IsEnabled() bool {
	return r.webhookURL != ""
}

// Collect appends findings to the pending buffer. Thread-safe for concurrent callers.
// Findings accumulate until Flush is called.
func (r *Reporter) Collect(findings []Finding) {
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
func (r *Reporter) Flush(ctx context.Context) {
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

	// Deduplicate within the pending batch (multiple poll cycles may have
	// collected the same finding before this Flush ran).
	seen := make(map[string]bool)
	deduped := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if f.DedupKey != "" {
			if seen[f.DedupKey] {
				continue
			}
			seen[f.DedupKey] = true
		}
		deduped = append(deduped, f)
	}
	findings = deduped

	// Prune dedup map if it has grown too large to prevent unbounded memory growth.
	if len(r.reported) > maxDedupEntries {
		r.logger.Info("pruning Slack dedup map", "entries", len(r.reported))
		clear(r.reported)
	}

	// Dedup: filter out findings already reported
	var newFindings []Finding
	for _, f := range findings {
		if f.DedupKey == "" || !r.reported[f.DedupKey] {
			newFindings = append(newFindings, f)
		}
	}

	if len(newFindings) == 0 {
		return
	}

	body := formatSlackMessage(newFindings, r.version)
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

	// Persist the report timestamp and dedup keys so they survive restarts.
	// Future report-only checks use this to filter out stale findings.
	// Subtract a 2-minute safety margin to avoid missing findings created
	// between when the GitHub API was queried and when Flush runs. The
	// in-memory DedupKey map filters out duplicates within the overlap window.
	r.lastReportedAt = time.Now().Add(-2 * time.Minute)
	persistLastReportedAt(r.stateFilePath, r.lastReportedAt, r.reported, r.logger)

	r.logger.Info("posted Slack report", "findings", len(newFindings))
}

// Report deduplicates findings, formats a Slack message, and POSTs it to the webhook.
// This is the single-call path used in single-repo mode (collect + flush in one step).
// No-op if there are no new findings after dedup, or if the reporter is disabled.
func (r *Reporter) Report(ctx context.Context, findings []Finding) {
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
// Returns nil if there are no findings. The version string (short commit SHA) is included
// in the header when non-empty.
//
// Format:
//
//	🏭 oompa report (38767d1) — N project(s) with activity
//
//	*<repo-link|owner/repo>* (M PRs)
//	📋 <pr-link|PR #N> — title
//	  🔴 <job-link|check-name> — failed
//	  ⚠️ behind main
//
// Each project gets a header section + detail section + divider.
func formatSlackMessage(findings []Finding, version string) []byte {
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
		owner   string
		repo    string
		prs     map[int]*prGroup
		prOrder []int
		prCount int
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
	var headerText string
	if version != "" {
		headerText = fmt.Sprintf("🏭 oompa report (%s) — %d project(s) with activity", shortSHA(version), len(projects))
	} else {
		headerText = fmt.Sprintf("🏭 oompa report — %d project(s) with activity", len(projects))
	}
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

// shortSHA returns the first 7 characters of a SHA (or the string itself
// when shorter), for compact display in the report header.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
