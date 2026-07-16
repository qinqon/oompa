package agent

import (
	"log/slog"

	"github.com/qinqon/oompa/internal/slack"
)

// Slack reporting lives in internal/slack; these aliases keep the
// package-local names used by the agent and cmd/oompa.
type (
	// SlackReporter posts per-cycle finding reports to a Slack webhook.
	SlackReporter = slack.Reporter
	// SlackFinding is one reportable finding tied to a PR or issue.
	SlackFinding = slack.Finding
)

// NewSlackReporter creates a reporter posting to webhookURL; empty URL
// disables reporting.
func NewSlackReporter(webhookURL, version string, logger *slog.Logger) *SlackReporter {
	return slack.NewReporter(webhookURL, version, logger)
}
