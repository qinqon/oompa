# CLI Flags

Every flag has a corresponding environment variable. Flags take precedence over environment variables.

## Core

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--repo` | `OOMPA_REPO` | -- | GitHub repo as `owner/repo` (required) |
| `--agent` | `OOMPA_AGENT` | `claudecode` | Coding agent backend: `claudecode` or `opencode` |
| `--agent-model` | `OOMPA_AGENT_MODEL` | -- | Model override for OpenCode (e.g. `google-vertex-anthropic/claude-opus-4-6@default`) |
| `--label` | `OOMPA_LABEL` | `good-for-ai` | Issue label to watch |
| `--clone-dir` | `OOMPA_CLONE_DIR` | `/tmp/oompa-work` | Working directory for clones and worktrees |
| `--poll-interval` | `OOMPA_POLL_INTERVAL` | `2m` | How often to poll GitHub |

## Logging

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--log-level` | `OOMPA_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `--log-file` | `OOMPA_LOG_FILE` | stderr | Write logs to a file instead of stderr |

## Git Identity

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--signed-off-by` | `OOMPA_SIGNED_OFF_BY` | auto-detected | `Signed-off-by` line for commits |
| `--assisted-by` | `OOMPA_ASSISTED_BY` | auto-detected | `Assisted-by` trailer for AI-assisted commits (auto-detected from `--agent`) |
| `--git-author-name` | `GIT_AUTHOR_NAME` | auto-detected | Git commit author name |
| `--git-author-email` | `GIT_AUTHOR_EMAIL` | auto-detected | Git commit author email |

## GitHub Authentication

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| -- | `GITHUB_TOKEN` | -- | GitHub personal access token (falls back to `gh auth token`) |
| `--github-app-id` | `GITHUB_APP_ID` | -- | GitHub App ID |
| `--github-app-private-key` | `GITHUB_APP_PRIVATE_KEY_PATH` | -- | Path to GitHub App private key PEM file |
| `--github-app-installation-id` | `GITHUB_APP_INSTALLATION_ID` | -- | GitHub App installation ID |
| `--github-user` | `GITHUB_USER` | auto-detected | GitHub username (e.g. `myapp[bot]`) |

## Behavior

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--reviewers` | `OOMPA_REVIEWERS` | all | Comma-separated allowlist of reviewers to respond to |
| `--fork` | `OOMPA_FORK` | -- | Fork repo as `owner/repo` for pushing branches |
| `--watch-prs` | `OOMPA_WATCH_PRS` | -- | Comma-separated PR numbers to monitor (bypasses issue discovery) |
| `--reactions` | `OOMPA_REACTIONS` | all | Comma-separated list: `reviews`, `ci`, `conflicts`, `rebase` |
| `--skip-comment` | `OOMPA_SKIP_COMMENTS` | none | Comma-separated comment categories to suppress |
| `--only-assigned` | `OOMPA_ONLY_ASSIGNED` | `false` | Only process issues assigned to the agent user |
| `--dry-run` | -- | `false` | Log actions without executing them |
| `--one-shot` | -- | `false` | Run one poll cycle and exit |
| `--max-workers` | `OOMPA_MAX_WORKERS` | `1` | Maximum parallel agent invocations |
| `--exit-on-new-version` | `OOMPA_EXIT_ON_NEW_VERSION` | -- | Exit when a new release is available (`owner/repo`) |

## CI and Flaky Tests

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--create-flaky-issues` | `OOMPA_CREATE_FLAKY_ISSUES` | `false` | Create issues for unrelated CI failures (opt-in) |
| `--flaky-label` | `OOMPA_FLAKY_LABEL` | `flaky-test` | Label to apply to flaky CI issues |
| `--triage-jobs` | `OOMPA_TRIAGE_JOBS` | -- | Comma-separated CI job URLs to monitor for periodic job triage |
| `--triage-lookback` | `OOMPA_TRIAGE_LOOKBACK` | `0` (latest only) | Time window to check for failed triage runs (e.g. `24h`, `12h`) |

## Notes

- `GITHUB_TOKEN` is optional -- if not set, oompa falls back to `gh auth token`.
- When all three `--github-app-*` flags are provided, the agent uses App auth instead of PAT auth.
- `--reactions` controls which processing phases run. An empty list disables all reactions (useful for report-only mode).
- `--skip-comment` categories: `ci-unrelated`, `ci-infrastructure`, `ci-related`, `conflict`, `rebase`, `flaky`, `issue-in-progress`.
