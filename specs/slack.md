# Slack Reporting

## Overview

Automatic Slack reporting at the end of each poll cycle. When `OOMPA_SLACK_WEBHOOK` is set, oompa posts a consolidated Slack message with concrete findings and clickable GitHub links. Silent cycles produce no Slack message.

## Trigger

`OOMPA_SLACK_WEBHOOK` environment variable contains a Slack Incoming Webhook URL. If not set, no Slack integration, no behavior change.

## Files

| File | Purpose |
|------|---------|
| `pkg/agent/slack.go` | SlackFinding, SlackReporter, postToSlack, message formatter, dedup tracker |
| `pkg/agent/slack_test.go` | Tests for formatting, dedup, empty findings, link construction |
| `pkg/agent/loop.go` | Agent loop integration: Check* methods, ShouldCheckReaction, RunReportOnlyChecks, cycle-end reporting |
| `pkg/agent/config.go` | `SlackWebhookURL` field on Config |
| `cmd/oompa/main.go` | Read `OOMPA_SLACK_WEBHOOK` env var |

## Data Types

```go
type SlackFinding struct {
    PRNumber  int
    PRTitle   string
    PRURL     string
    Category  string   // "ci", "rebase", "conflict", "review", "error"
    Message   string   // Slack mrkdwn formatted message line
    DedupKey  string   // unique key for dedup (e.g. "ci:sha:checkName")
}

type SlackReporter struct {
    webhookURL string
    owner      string
    repo       string
    reported   map[string]bool // tracks DedupKeys already reported
    logger     *slog.Logger
    httpClient *http.Client    // injectable for testing
}
```

## SlackReporter Methods

- `NewSlackReporter(webhookURL, owner, repo string, logger *slog.Logger) *SlackReporter`
- `(r *SlackReporter) Report(ctx context.Context, findings []SlackFinding)` — dedup, format, POST
- `(r *SlackReporter) IsEnabled() bool` — returns true if webhookURL is non-empty
- `formatSlackMessage(owner, repo string, findings []SlackFinding) []byte` — builds Slack Block Kit JSON
- `postToSlack(ctx context.Context, client *http.Client, webhookURL string, body []byte) error` — HTTP POST

## What Gets Reported

| Finding | Slack format | DedupKey |
|---------|-------------|----------|
| CI check failed | `🔴 <job-link\|check-name> failed` | `ci:<sha>:<checkName>` |
| CI failed + flaky match | `🔴 <job-link\|check-name> — flaky <issue-link\|#N>` | `ci:<sha>:<checkName>` |
| CI fix pushed | `🔧 Fixed <job-link\|check-name>, pushed <commit-link\|SHA>` | `ci-fix:<sha>:<checkName>` |
| Rebase done | `✅ Rebased <pr-link\|PR #N> → <commit-link\|SHA>` | `rebase:<prNumber>:<newSHA>` |
| Rebase needed (report-only) | `⚠️ <pr-link\|PR #N> is behind main` | `rebase-needed:<prNumber>` |
| Conflicts detected | `⚠️ <pr-link\|PR #N> has merge conflicts` | `conflict:<prNumber>` |
| Conflicts resolved | `✅ Resolved conflicts on <pr-link\|PR #N> → <commit-link\|SHA>` | `conflict-resolved:<prNumber>:<sha>` |
| New review comments | `💬 <pr-link\|PR #N> has N new reviews` | `review:<prNumber>:<maxCommentID>` |
| Reviews addressed | `✅ Addressed reviews on <pr-link\|PR #N> → <commit-link\|SHA>` | `review-addressed:<prNumber>:<sha>` |
| Flaky issue created | `📋 Opened <issue-link\|#N> for flaky test` | `flaky:<issueNumber>` |
| Error | `❌ Failed: <pr-link\|PR #N> — error message` | `error:<prNumber>:<errorHash>` |

## What is NOT Reported

- Poll cycle start/end
- Routine checks that found nothing
- Any idle/no-op cycle

## Reactions Behavior with Slack

| Reaction in list? | Webhook set? | Behavior |
|-------------------|-------------|----------|
| Yes | Yes | Fix + report to Slack |
| Yes | No | Fix only (current behavior) |
| No | Yes | Check status + report to Slack (no fix) |
| No | No | Skip entirely (current behavior) |

`ShouldCheckReaction(reaction)` returns true when webhook is set AND reaction is NOT in the list. Check methods are lightweight API-only calls:

- `CheckCIStatus(ctx)` — fetch check runs, report failures
- `CheckRebaseNeeded(ctx)` — check mergeable state, report if behind
- `CheckConflicts(ctx)` — check mergeable state, report if conflicting
- `CheckNewReviews(ctx)` — count new review comments, report if any

## Slack Message Format

Slack Block Kit with mrkdwn. One message per cycle, grouped by PR:

```text
🏭 oompa — owner/repo

📋 <pr-link|PR #N> — title
  🔴 <job-link|check-name> — failed
  ⚠️ 15 commits behind main

📋 <pr-link|PR #N> — title
  💬 3 new reviews
  ⚠️ Merge conflicts
```

## Dedup

Don't re-report the same finding every poll cycle. Each finding has a DedupKey. Report once when first detected, suppress until the key changes:

- CI failure: `ci:<sha>:<checkName>` — new SHA or new check → re-report
- Rebase needed: `rebase-needed:<prNumber>` — report once, stays suppressed until resolved
- Conflicts: `conflict:<prNumber>` — report once
- Reviews: `review:<prNumber>:<maxCommentID>` — new comments → re-report

## Tests

- `TestFormatSlackMessage_GroupsByPR` — findings grouped by PR number
- `TestFormatSlackMessage_EmptyFindings` — returns nil
- `TestSlackReporter_Dedup` — same DedupKey suppressed on second call
- `TestSlackReporter_DedupReset` — different DedupKey re-sends
- `TestSlackReporter_Disabled` — no webhook → IsEnabled() false
- `TestPostToSlack_Success` — HTTP POST with correct body
- `TestPostToSlack_HTTPError` — non-200 status returns error
- `TestCheckCIStatus_ReportsFailures` — report-only check produces findings
- `TestCheckRebaseNeeded_ReportsBehind` — report-only check produces findings
- `TestCheckConflicts_ReportsDirty` — report-only check produces findings
- `TestCheckNewReviews_ReportsComments` — report-only check produces findings
