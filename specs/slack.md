# Slack Reporting

## Overview

Automatic Slack reporting at the end of each poll cycle. When `OOMPA_SLACK_WEBHOOK` is set, oompa posts a **single consolidated Slack message** across all projects with concrete findings and clickable GitHub links. Silent cycles produce no Slack message.

In multi-project mode, findings from all project goroutines are collected into a shared reporter and flushed periodically as one message. Projects with no findings are omitted.

## Trigger

`OOMPA_SLACK_WEBHOOK` environment variable contains a Slack Incoming Webhook URL. If not set, no Slack integration, no behavior change.

## Files

| File | Purpose |
|------|---------|
| `pkg/agent/slack.go` | SlackFinding, SlackReporter (thread-safe: `mu` protects pending buffer, `flushMu` serializes Flush), postToSlack, message formatter, dedup tracker |
| `pkg/agent/slack_test.go` | Tests for formatting, dedup, empty findings, link construction, collect/flush, concurrency, block splitting |
| `pkg/agent/loop.go` | Agent loop integration: Check* methods, ShouldCheckReaction, RunReportOnlyChecks, CollectSlackFindings, FlushSlackReport |
| `pkg/agent/config.go` | `SlackWebhookURL` field on Config |
| `cmd/oompa/main.go` | Read `OOMPA_SLACK_WEBHOOK` env var, create shared reporter in multi-project mode, flush goroutine |

## Data Types

```go
type SlackFinding struct {
    Owner     string   // repository owner (e.g. "ovn-kubernetes")
    Repo      string   // repository name (e.g. "ovn-kubernetes")
    PRNumber  int
    PRTitle   string
    PRURL     string
    Category  string   // "ci", "rebase", "conflict", "review", "error"
    Message   string   // Slack mrkdwn formatted message line
    DedupKey  string   // unique key for dedup (e.g. "ci:sha:checkName")
}

type SlackReporter struct {
    webhookURL string
    reported   map[string]bool // tracks DedupKeys already reported
    logger     *slog.Logger
    httpClient *http.Client    // injectable for testing
    flushMu    sync.Mutex      // serializes Flush calls (protects reported map)
    mu         sync.Mutex      // protects pending
    pending    []SlackFinding  // findings collected since last Flush
}
```

## SlackReporter Methods

- `NewSlackReporter(webhookURL string, logger *slog.Logger) *SlackReporter`
- `(r *SlackReporter) IsEnabled() bool` — returns true if webhookURL is non-empty
- `(r *SlackReporter) Collect(findings []SlackFinding)` — thread-safe append to pending buffer
- `(r *SlackReporter) Flush(ctx context.Context)` — dedup, format, POST, clear pending
- `(r *SlackReporter) Report(ctx context.Context, findings []SlackFinding)` — convenience: Collect + Flush in one call (single-repo mode)
- `formatSlackMessage(findings []SlackFinding) []byte` — builds Slack Block Kit JSON grouped by project then PR
- `postToSlack(ctx context.Context, client *http.Client, webhookURL string, body []byte) error` — HTTP POST

## Collection Architecture

### Single-repo mode
Each `runLoop` call collects findings and flushes immediately at the end of the cycle (one agent, one reporter).

### Multi-project mode
1. A shared `SlackReporter` is created once and passed to all agent goroutines
2. Each goroutine calls `CollectSlackFindings()` at the end of its poll cycle
3. A separate flush goroutine runs on a timer matching the poll interval, calling `Flush()` periodically
4. This produces one consolidated Slack message per flush window across all projects
5. A final `Flush()` runs on shutdown to capture remaining findings

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
- Projects with no findings (omitted from message entirely)

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

Slack Block Kit with header + per-project sections. One consolidated message per cycle, grouped by project then by PR:

```text
🏭 oompa report — 3 project(s) with activity

*<repo-link|owner1/repo1>* (2 PRs)
📋 <pr-link|PR #100> — Fix kubevirt test flake
  🔴 <job-link|e2e-test> failed
  ⚠️ behind main
📋 <pr-link|PR #101> — kubevirt vm hostname
  💬 3 new review comment(s)

---

*<repo-link|owner2/repo2>* (1 PR)
📋 <pr-link|PR #200> — KubeVirt nmstate
  🔴 <job-link|e2e-kubevirt> failed
```

### Block structure

1. `header` block — "🏭 oompa report — N project(s) with activity"
2. Per project:
   - `section` block (mrkdwn) — project name with link and PR count
   - `section` block (mrkdwn) — PR details with findings (split if >3000 chars)
   - `divider` block (between projects, not after last)

### Links

Every item links to the relevant GitHub resource:
- Project name → `github.com/owner/repo`
- PR number → `github.com/owner/repo/pull/N`
- CI check name → `CheckRun.HTMLURL`

## Dedup

Don't re-report the same finding every poll cycle. Each finding has a DedupKey. Report once when first detected, suppress until the key changes:

- CI failure: `ci:<sha>:<checkName>` — new SHA or new check → re-report
- Rebase needed: `rebase-needed:<prNumber>` — report once, stays suppressed until resolved
- Conflicts: `conflict:<prNumber>` — report once
- Reviews: `review:<prNumber>:<maxCommentID>` — new comments → re-report

## Tests

- `TestFormatSlackMessage_GroupsByPR` — findings grouped by PR number within a project
- `TestFormatSlackMessage_EmptyFindings` — returns nil
- `TestFormatSlackMessage_MultipleProjects` — findings from multiple projects in one message
- `TestFormatSlackMessage_SingleProjectNoFindings` — only projects with findings appear
- `TestFormatSlackMessage_LinksCorrect` — project, PR, and CI links correct
- `TestFormatSlackMessage_HasDividersBetweenProjects` — dividers between projects, not after last
- `TestFormatSlackMessage_BlockTextLimit` — blocks split when text exceeds 3000 chars
- `TestFormatSlackMessage_BlockTextLimit_SingleLongLine` — single line exceeding 3000 chars splits correctly
- `TestFormatSlackMessage_TruncatesAt50Blocks` — messages with >50 blocks are truncated
- `TestSlackReporter_Dedup` — same DedupKey suppressed on second call
- `TestSlackReporter_DedupReset` — different DedupKey re-sends
- `TestSlackReporter_Disabled` — no webhook → IsEnabled() false
- `TestSlackReporter_EmptyDedupKey` — empty DedupKey always sent
- `TestSlackReporter_CollectAndFlush` — collect from multiple projects, flush as one POST
- `TestSlackReporter_CollectConcurrent` — concurrent Collect calls are thread-safe
- `TestSlackReporter_FlushDedup` — dedup works across collect/flush cycles
- `TestPostToSlack_Success` — HTTP POST with correct body
- `TestPostToSlack_HTTPError` — non-200 status returns error
- `TestCheckCIStatus_ReportsFailures` — report-only check produces findings with Owner/Repo
- `TestCheckRebaseNeeded_ReportsBehind` — report-only check produces findings with Owner/Repo
- `TestCheckConflicts_ReportsDirty` — report-only check produces findings with Owner/Repo
- `TestCheckNewReviews_ReportsComments` — report-only check produces findings with Owner/Repo
