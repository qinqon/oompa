# oompa

An autonomous AI-powered code maintenance agent. It uses [OpenCode](https://opencode.ai) or [Claude Code](https://docs.anthropic.com/en/docs/claude-code) to implement fixes, address reviews, resolve merge conflicts, fix CI failures, and triage flaky tests — all without human intervention beyond the final merge.

## What it does

- **Resolve issues** — picks up GitHub issues with a configurable label, implements fixes, and opens pull requests.
- **Address reviews** — reads reviewer comments and iterates on the code until reviewers are satisfied.
- **Fix CI failures** — detects failing checks, analyzes logs, and pushes fixes.
- **Resolve merge conflicts** — attempts an automatic rebase and falls back to Claude when that fails.
- **Babysit PRs** — monitors specific PRs for any combination of the above (reviews, CI, conflicts, rebase).
- **Triage periodic CI** — analyzes nightly/scheduled job failures and creates issues with root-cause analysis.

Claude never merges; a human must approve and merge every PR.

## Prerequisites

- Go 1.26+
- A coding agent CLI on `PATH`: either [OpenCode](https://opencode.ai) (recommended) or [Claude Code](https://docs.anthropic.com/en/docs/claude-code)
- Provider credentials configured (e.g. `gcloud auth application-default login` for Vertex AI, or `ANTHROPIC_API_KEY` for direct API)
- GitHub authentication: either `gh auth login` (recommended), a personal access token (PAT) with repo scope, or a GitHub App (see below)
- `gh` CLI installed and configured as a git credential helper (`gh auth setup-git`)

## Build

```bash
go build -o oompa ./cmd/oompa
```

## Usage

### With OpenCode (recommended)

```bash
gh auth login                    # one-time setup
gcloud auth application-default login  # for Vertex AI

./oompa --agent opencode --agent-model google-vertex-anthropic/claude-opus-4-6@default --repo myorg/myrepo
```

### With Claude Code

```bash
gh auth login
export CLAUDE_CODE_USE_VERTEX=1
export CLOUD_ML_REGION="us-east5"
export ANTHROPIC_VERTEX_PROJECT_ID="my-gcp-project"

./oompa --repo myorg/myrepo
```

### With a GitHub App

```bash
export GITHUB_APP_ID="123456"
export GITHUB_APP_PRIVATE_KEY_PATH="/path/to/private-key.pem"
export GITHUB_APP_INSTALLATION_ID="78901234"

./oompa --agent opencode --agent-model google-vertex-anthropic/claude-opus-4-6@default --repo myorg/myrepo
```

### Setting up a GitHub App

1. Go to your organization's settings: `https://github.com/organizations/<org>/settings/apps/new`
2. Fill in the basic info:
   - **App name**: a unique name (e.g. `myorg-oompa`)
   - **Homepage URL**: your repo or org URL
   - **Webhook**: uncheck "Active" (the agent uses polling, not webhooks)
3. Set **repository permissions**:
   | Permission | Access |
   |---|---|
   | Actions | Read-only |
   | Checks | Read-only |
   | Contents | Read and write |
   | Issues | Read and write |
   | Metadata | Read-only |
   | Pull requests | Read and write |
4. Leave all organization/account permissions as "No access" and all event subscriptions unchecked.
5. Under "Where can this GitHub App be installed?", select "Only on this account".
6. Click **Create GitHub App** and note the **App ID**.
7. On the app settings page, scroll to "Private keys" and click **Generate a private key**. Save the downloaded `.pem` file.
8. In the left sidebar, click **Install App**, then install it on the target repository.
9. Get the **Installation ID**:
   ```bash
   gh api /orgs/<org>/installations \
     --jq '.installations[] | select(.app_slug | contains("<app-slug>")) | .id'
   ```

When using GitHub App auth, the agent pushes branches directly to the upstream repository (no fork) and authenticates as `<app-slug>[bot]`. Installation tokens are automatically refreshed before each poll cycle.

### Flags and environment variables

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `--repo` | `OOMPA_REPO` | — | GitHub repo as `owner/repo` (required) |
| `--agent` | `OOMPA_AGENT` | `claudecode` | Coding agent backend: `claudecode` or `opencode` |
| `--agent-model` | `OOMPA_AGENT_MODEL` | — | Model override for OpenCode (e.g. `google-vertex-anthropic/claude-opus-4-6@default`) |
| `--label` | `OOMPA_LABEL` | `good-for-ai` | Issue label to watch |
| `--clone-dir` | `OOMPA_CLONE_DIR` | `/tmp/oompa-work` | Working directory for clones and worktrees |
| `--poll-interval` | `OOMPA_POLL_INTERVAL` | `2m` | How often to poll GitHub |
| `--log-level` | `OOMPA_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `--log-file` | `OOMPA_LOG_FILE` | stderr | Write logs to a file instead of stderr |
| `--signed-off-by` | `OOMPA_SIGNED_OFF_BY` | auto-detected | `Signed-off-by` line for commits |
| `--reviewers` | `OOMPA_REVIEWERS` | all | Comma-separated allowlist of reviewers to respond to |
| `--fork` | `OOMPA_FORK` | — | Fork repo as `owner/repo` for pushing branches |
| `--watch-prs` | `OOMPA_WATCH_PRS` | — | Comma-separated PR numbers to monitor (bypasses issue discovery) |
| `--reactions` | `OOMPA_REACTIONS` | all | Comma-separated list: `reviews`, `ci`, `conflicts`, `rebase` |
| `--only-assigned` | `OOMPA_ONLY_ASSIGNED` | `false` | Only process issues assigned to the agent user |
| `--create-flaky-issues` | `OOMPA_CREATE_FLAKY_ISSUES` | `false` | Create issues for unrelated CI failures (opt-in) |
| `--flaky-label` | `OOMPA_FLAKY_LABEL` | `flaky-test` | Label to apply to flaky CI issues |
| `--triage-jobs` | `OOMPA_TRIAGE_JOBS` | — | Comma-separated CI job URLs to monitor for periodic job triage |
| `--max-workers` | `OOMPA_MAX_WORKERS` | `1` | Maximum parallel agent invocations |
| `--exit-on-new-version` | `OOMPA_EXIT_ON_NEW_VERSION` | — | Exit when a new release is available (`owner/repo`) |
| `--dry-run` | — | `false` | Log actions without executing them |
| `--one-shot` | — | `false` | Run one poll cycle and exit |
| — | `GITHUB_TOKEN` | — | GitHub personal access token (falls back to `gh auth token`) |
| `--github-app-id` | `GITHUB_APP_ID` | — | GitHub App ID |
| `--github-app-private-key` | `GITHUB_APP_PRIVATE_KEY_PATH` | — | Path to GitHub App private key PEM file |
| `--github-app-installation-id` | `GITHUB_APP_INSTALLATION_ID` | — | GitHub App installation ID |
| `--github-user` | `GITHUB_USER` | auto-detected | GitHub username (e.g. `myapp[bot]`) |
| `--git-author-name` | `GIT_AUTHOR_NAME` | auto-detected | Git commit author name |
| `--git-author-email` | `GIT_AUTHOR_EMAIL` | auto-detected | Git commit author email |

`GITHUB_TOKEN` is optional — if not set, oompa falls back to `gh auth token`. When all three `--github-app-*` flags are provided, the agent uses App auth instead.

## Running as a systemd service

Oompa can run as a systemd user service that automatically downloads the latest release binary on each (re)start. Use `RuntimeDirectory=` to give each unit its own isolated directory and `--exit-on-new-version` to trigger a restart when a new release is published.

### Example: issue resolver

```ini
# ~/.config/systemd/user/oompa-issue-resolver.service
[Unit]
Description=Oompa Issue Resolver - myorg/myrepo
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=%h/.config/oompa/env
Environment=PATH=/usr/local/bin:/usr/bin:/bin
RuntimeDirectory=oompa-resolver
ExecStartPre=/bin/bash -c 'gh release download --repo qinqon/oompa --pattern oompa-linux-amd64 --dir %t/oompa-resolver --clobber && chmod +x %t/oompa-resolver/oompa-linux-amd64'
ExecStart=%t/oompa-resolver/oompa-linux-amd64 --exit-on-new-version=qinqon/oompa --repo myorg/myrepo --poll-interval 2m --log-level info
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
```

### Example: PR babysitter

```ini
# ~/.config/systemd/user/oompa-pr-babysitter.service
[Unit]
Description=Oompa PR Babysitter - myorg/myrepo
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=%h/.config/oompa/env
Environment=PATH=/usr/local/bin:/usr/bin:/bin
RuntimeDirectory=oompa-babysitter
ExecStartPre=/bin/bash -c 'gh release download --repo qinqon/oompa --pattern oompa-linux-amd64 --dir %t/oompa-babysitter --clobber && chmod +x %t/oompa-babysitter/oompa-linux-amd64'
ExecStart=%t/oompa-babysitter/oompa-linux-amd64 --exit-on-new-version=qinqon/oompa --repo myorg/myrepo --watch-prs 123,456 --reactions ci,conflicts,rebase --fork myuser/myrepo --poll-interval 2m --log-level info
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
```

### Example: periodic CI triage (one-shot with timer)

For one-shot workflows like periodic CI triage, use a `Type=oneshot` service paired with a systemd timer instead of `Restart=always`.

```ini
# ~/.config/systemd/user/oompa-periodic-triage.service
[Unit]
Description=Oompa Periodic CI Triage - myorg/myrepo
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
EnvironmentFile=%h/.config/oompa/env
Environment=PATH=/usr/local/bin:/usr/bin:/bin
RuntimeDirectory=oompa-periodic-triage
ExecStartPre=/bin/bash -c 'gh release download --repo qinqon/oompa --pattern oompa-linux-amd64 --dir %t/oompa-periodic-triage --clobber && chmod +x %t/oompa-periodic-triage/oompa-linux-amd64'
ExecStart=%t/oompa-periodic-triage/oompa-linux-amd64 --repo myorg/myrepo --triage-jobs https://prow.example.com/view/gs/bucket/logs/periodic-e2e-job/ --create-flaky-issues --one-shot --log-level info
```

```ini
# ~/.config/systemd/user/oompa-periodic-triage.timer
[Unit]
Description=Run Oompa Periodic CI Triage daily at 9 AM

[Timer]
OnCalendar=*-*-* 09:00:00 Europe/Madrid
Persistent=true

[Install]
WantedBy=timers.target
```

Enable the timer (not the service):

```bash
systemctl --user enable --now oompa-periodic-triage.timer
```

- `Type=oneshot` lets the service run to completion and exit.
- `--one-shot` makes oompa run a single triage cycle and exit.
- `Persistent=true` ensures a missed run (e.g. machine was off) is caught up on next boot.
- The timer's `OnCalendar` supports IANA timezones (e.g. `Europe/Madrid`, `US/Eastern`).

### Environment file

Store provider credentials in `~/.config/oompa/env`:

```bash
# For Vertex AI (used by both OpenCode and Claude Code)
CLOUD_ML_REGION=us-east5
ANTHROPIC_VERTEX_PROJECT_ID=my-gcp-project
GOOGLE_APPLICATION_CREDENTIALS=/path/to/credentials.json
GOOGLE_CLOUD_PROJECT=my-gcp-project

# GITHUB_TOKEN is optional — oompa falls back to `gh auth token`
```

### How it works

- `ExecStartPre` downloads the latest release binary before each start.
- `--exit-on-new-version=qinqon/oompa` makes the agent exit when it detects a newer release during polling.
- `Restart=always` restarts the service on exit, which triggers `ExecStartPre` to download the new binary.
- `RuntimeDirectory=` gives each unit its own directory under `/run/user/<uid>/`, so multiple units don't interfere with each other.
- `%t` is the systemd specifier for the runtime directory root.
- `%h` is the systemd specifier for the user's home directory.

### Managing the services

```bash
# Enable and start
systemctl --user enable --now oompa-issue-resolver.service

# Check status
systemctl --user status oompa-issue-resolver.service

# View logs
journalctl --user -u oompa-issue-resolver.service -f

# Restart (re-downloads the binary)
systemctl --user restart oompa-issue-resolver.service
```

## State

The agent is stateless on disk. On every startup it rebuilds its state from GitHub by scanning labeled issues and matching PRs, so there is nothing to back up or migrate.

## Testing

```bash
go test ./...
```

All external interactions (GitHub API, Claude CLI, git) are behind interfaces with mock implementations, so tests run without any credentials or network access.

## Project layout

```
cmd/oompa/          CLI entry point
pkg/agent/          Core logic (loop, state, GitHub client, Claude runner, worktree, prompts)
specs/              Design specifications for each component
```

## Safety

- Claude only creates PRs — it never merges.
- No force-pushes.
- On failure, the issue is labeled `ai-failed` with a comment explaining the error. A human removes the label and re-adds `good-for-ai` to retry.
- Billing is controlled through GCP IAM on the Vertex AI project.

## Acknowledgments

Prompt engineering patterns in this project were inspired by:

- [openshift-eng/ai-helpers](https://github.com/openshift-eng/ai-helpers) (Apache License 2.0) — structured step-by-step investigation procedures, "be tenacious" root-cause tracing, review comment classification, concise reply formatting, and commit convention detection.
- [ambient-code/workflows](https://github.com/ambient-code/workflows) — "don't over-fix" guardrails, mandatory reply on every review thread, and self-review before push.
