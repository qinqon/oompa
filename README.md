# oompa

An autonomous AI-powered code maintenance agent that uses [OpenCode](https://opencode.ai) or [Claude Code](https://docs.anthropic.com/en/docs/claude-code) to implement fixes, address reviews, resolve merge conflicts, fix CI failures, and triage flaky tests -- all without human intervention beyond the final merge.

## What It Does

- **Resolve issues** -- picks up GitHub issues with a configurable label, implements fixes, and opens pull requests.
- **Address reviews** -- reads reviewer comments and iterates on the code until reviewers are satisfied.
- **Fix CI failures** -- detects failing checks, analyzes logs, and pushes fixes.
- **Resolve merge conflicts** -- attempts an automatic rebase and falls back to Claude when that fails.
- **Babysit PRs** -- monitors specific PRs for reviews, CI, conflicts, and rebase.
- **Triage periodic CI** -- analyzes nightly/scheduled job failures and creates issues with root-cause analysis.

Claude never merges; a human must approve and merge every PR.

## Prerequisites

- Go 1.26+
- A coding agent CLI on `PATH`: either [OpenCode](https://opencode.ai) (recommended) or [Claude Code](https://docs.anthropic.com/en/docs/claude-code)
- Provider credentials configured (e.g. `gcloud auth application-default login` for Vertex AI, or `ANTHROPIC_API_KEY` for direct API)
- GitHub authentication: either `gh auth login` (recommended), a personal access token (PAT) with repo scope, or a GitHub App (see below)
- `gh` CLI installed and configured as a git credential helper (`gh auth setup-git`)
- [compound-engineering-plugin](https://github.com/EveryInc/compound-engineering-plugin) for CI investigation, review handling, and commit creation:
  - OpenCode: `bunx @every-env/compound-plugin install compound-engineering --to opencode`
  - Claude Code: `/plugin install compound-engineering` from the marketplace

## Build

```bash
go build -o oompa ./cmd/oompa
```

## Usage

**OpenCode (recommended):**

```bash
gh auth login
gcloud auth application-default login
./oompa --agent opencode --agent-model google-vertex-anthropic/claude-opus-4-6@default --repo myorg/myrepo
```

**Claude Code:**

```bash
gh auth login
export CLAUDE_CODE_USE_VERTEX=1
export CLOUD_ML_REGION="us-east5"
export ANTHROPIC_VERTEX_PROJECT_ID="my-gcp-project"
./oompa --repo myorg/myrepo
```

**GitHub App:**

```bash
export GITHUB_APP_ID="123456"
export GITHUB_APP_PRIVATE_KEY_PATH="/path/to/private-key.pem"
export GITHUB_APP_INSTALLATION_ID="78901234"
./oompa --agent opencode --agent-model google-vertex-anthropic/claude-opus-4-6@default --repo myorg/myrepo
```

To set up a GitHub App: create one in your org settings (`https://github.com/organizations/<org>/settings/apps/new`), disable webhooks (oompa uses polling), grant repository permissions (Actions: read, Checks: read, Contents: read+write, Issues: read+write, Metadata: read, Pull requests: read+write), generate a private key, and install it on the target repository. Get the installation ID with `gh api /orgs/<org>/installations --jq '.installations[] | select(.app_slug | contains("<app-slug>")) | .id'`. The agent pushes branches directly to the upstream repository and authenticates as `<app-slug>[bot]`. Installation tokens are automatically refreshed before each poll cycle.

## Flags

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `--repo` | `OOMPA_REPO` | -- | GitHub repo as `owner/repo` (required) |
| `--agent` | `OOMPA_AGENT` | `claudecode` | Coding agent backend: `claudecode` or `opencode` |
| `--agent-model` | `OOMPA_AGENT_MODEL` | -- | Model override for OpenCode (e.g. `google-vertex-anthropic/claude-opus-4-6@default`) |
| `--label` | `OOMPA_LABEL` | `good-for-ai` | Issue label to watch |
| `--clone-dir` | `OOMPA_CLONE_DIR` | `/tmp/oompa-work` | Working directory for clones and worktrees |
| `--poll-interval` | `OOMPA_POLL_INTERVAL` | `2m` | How often to poll GitHub |
| `--log-level` | `OOMPA_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `--log-file` | `OOMPA_LOG_FILE` | stderr | Write logs to a file instead of stderr |
| `--signed-off-by` | `OOMPA_SIGNED_OFF_BY` | auto-detected | `Signed-off-by` line for commits |
| `--assisted-by` | `OOMPA_ASSISTED_BY` | auto-detected | `Assisted-by` trailer for AI-assisted commits (auto-detected from `--agent`) |
| `--reviewers` | `OOMPA_REVIEWERS` | all | Comma-separated allowlist of reviewers to respond to |
| `--fork` | `OOMPA_FORK` | -- | Fork repo as `owner/repo` for pushing branches |
| `--watch-prs` | `OOMPA_WATCH_PRS` | -- | Comma-separated PR numbers to monitor (bypasses issue discovery) |
| `--reactions` | `OOMPA_REACTIONS` | all | Comma-separated list: `reviews`, `ci`, `conflicts`, `rebase` |
| `--skip-comment` | `OOMPA_SKIP_COMMENTS` | none | Comma-separated comment categories to suppress: `ci-unrelated`, `ci-infrastructure`, `ci-related`, `conflict`, `rebase`, `flaky`, `issue-in-progress` |
| `--only-assigned` | `OOMPA_ONLY_ASSIGNED` | `false` | Only process issues assigned to the agent user |
| `--create-flaky-issues` | `OOMPA_CREATE_FLAKY_ISSUES` | `false` | Create issues for unrelated CI failures (opt-in) |
| `--flaky-label` | `OOMPA_FLAKY_LABEL` | `flaky-test` | Label to apply to flaky CI issues |
| `--triage-jobs` | `OOMPA_TRIAGE_JOBS` | -- | Comma-separated CI job URLs to monitor for periodic job triage |
| `--triage-lookback` | `OOMPA_TRIAGE_LOOKBACK` | `0` (latest only) | Time window to check for failed triage runs (e.g. `24h`, `12h`) |
| `--max-workers` | `OOMPA_MAX_WORKERS` | `1` | Maximum parallel agent invocations |
| `--exit-on-new-version` | `OOMPA_EXIT_ON_NEW_VERSION` | -- | Exit when a new release is available (`owner/repo`) |
| `--dry-run` | -- | `false` | Log actions without executing them |
| `--one-shot` | -- | `false` | Run one poll cycle and exit |
| -- | `GITHUB_TOKEN` | -- | GitHub personal access token (falls back to `gh auth token`) |
| `--github-app-id` | `GITHUB_APP_ID` | -- | GitHub App ID |
| `--github-app-private-key` | `GITHUB_APP_PRIVATE_KEY_PATH` | -- | Path to GitHub App private key PEM file |
| `--github-app-installation-id` | `GITHUB_APP_INSTALLATION_ID` | -- | GitHub App installation ID |
| `--github-user` | `GITHUB_USER` | auto-detected | GitHub username (e.g. `myapp[bot]`) |
| `--git-author-name` | `GIT_AUTHOR_NAME` | auto-detected | Git commit author name |
| `--git-author-email` | `GIT_AUTHOR_EMAIL` | auto-detected | Git commit author email |

`GITHUB_TOKEN` is optional -- if not set, oompa falls back to `gh auth token`. When all three `--github-app-*` flags are provided, the agent uses App auth instead.

## Running as a Systemd Service

Oompa can run as a systemd user service that downloads the latest release binary on each (re)start. Use `RuntimeDirectory=` to isolate each unit and `--exit-on-new-version` to trigger a restart when a new release is published.

Store provider credentials in `~/.config/oompa/env`:

```bash
# For Vertex AI (used by both OpenCode and Claude Code)
CLOUD_ML_REGION=us-east5
ANTHROPIC_VERTEX_PROJECT_ID=my-gcp-project
GOOGLE_APPLICATION_CREDENTIALS=/path/to/credentials.json
GOOGLE_CLOUD_PROJECT=my-gcp-project

# GITHUB_TOKEN is optional — oompa falls back to `gh auth token`
```

**Issue resolver** (`~/.config/systemd/user/oompa-issue-resolver.service`):

```ini
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

**PR babysitter** (`~/.config/systemd/user/oompa-pr-babysitter.service`):

```ini
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

**Periodic CI triage** (one-shot with timer):

For one-shot workflows, use a `Type=oneshot` service paired with a systemd timer.

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

Enable the timer (not the service): `systemctl --user enable --now oompa-periodic-triage.timer`

How it works: `ExecStartPre` downloads the latest release binary before each start. `--exit-on-new-version` makes the agent exit when it detects a newer release during polling. `Restart=always` restarts the service on exit, triggering `ExecStartPre` to download the new binary. `RuntimeDirectory=` gives each unit its own directory under `/run/user/<uid>/`. `Persistent=true` on timers ensures missed runs are caught up on next boot. The timer's `OnCalendar` supports IANA timezones (e.g. `Europe/Madrid`, `US/Eastern`).

Manage services with: `systemctl --user enable --now <service>`, `systemctl --user status <service>`, `journalctl --user -u <service> -f`, `systemctl --user restart <service>`.

## Architecture

The agent is stateless on disk -- on every startup it rebuilds state from GitHub by scanning labeled issues and matching PRs. All external interactions (GitHub API, Claude CLI, git) are behind interfaces with mock implementations, so tests run without credentials or network access.

```text
cmd/oompa/          CLI entry point
pkg/agent/          Core logic (loop, state, GitHub client, Claude runner, worktree, prompts)
specs/              Design specifications for each component
```

Claude only creates PRs -- it never merges. No force-pushes. On failure, the issue is labeled `ai-failed` with a comment explaining the error; a human removes the label and re-adds `good-for-ai` to retry. Billing is controlled through GCP IAM on the Vertex AI project.

## Acknowledgments

Prompt engineering patterns in this project were inspired by [openshift-eng/ai-helpers](https://github.com/openshift-eng/ai-helpers) (Apache License 2.0), [ambient-code/workflows](https://github.com/ambient-code/workflows), and [EveryInc/compound-engineering-plugin](https://github.com/EveryInc/compound-engineering-plugin) (MIT License).
