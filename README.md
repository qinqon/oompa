# oompa

A single long-running Go binary that automatically resolves GitHub issues using Claude AI. It polls for issues with a configurable label, invokes Claude Code in headless mode to implement fixes, and opens pull requests — no webhooks or per-issue goroutines required.

## How it works

1. **Poll** — watches for open issues tagged with a label (default `good-for-ai`).
2. **Implement** — creates a git worktree, runs Claude Code (`claude -p`) to produce a fix.
3. **Open PR** — pushes the branch and creates a pull request linked to the issue.
4. **Address reviews** — picks up reviewer comments, runs Claude again to iterate.
5. **Rebase conflicts** — detects PRs with merge conflicts, attempts an automatic rebase, and falls back to Claude if that fails.
6. **Handle CI failures** — detects CI failures and asks Claude to fix them. Optionally creates issues for unrelated CI failures (flaky tests) when `--create-flaky-issues` is enabled.

Claude never merges; a human must approve and merge every PR.

## Prerequisites

- Go 1.25+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and on `PATH`
- Google Cloud Application Default Credentials configured (`gcloud auth application-default login` or a service account key)
- GitHub authentication: either a personal access token (PAT) with repo scope **or** a GitHub App (see below)
- `gh` CLI installed and configured as a git credential helper (`gh auth setup-git`)

## Build

```bash
go build -o oompa ./cmd/oompa
```

## Usage

### With a personal access token (PAT)

```bash
export GITHUB_TOKEN="ghp_..."
export CLOUD_ML_REGION="us-east5"
export ANTHROPIC_VERTEX_PROJECT_ID="my-gcp-project"

./oompa --owner myorg --repo myrepo
```

### With a GitHub App

```bash
export GITHUB_APP_ID="123456"
export GITHUB_APP_PRIVATE_KEY_PATH="/path/to/private-key.pem"
export GITHUB_APP_INSTALLATION_ID="78901234"
export CLOUD_ML_REGION="us-east5"
export ANTHROPIC_VERTEX_PROJECT_ID="my-gcp-project"

./oompa --owner myorg --repo myrepo
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
| `--owner` | `OOMPA_OWNER` | `openperouter` | GitHub repo owner |
| `--repo` | `OOMPA_REPO` | `openperouter` | GitHub repo name |
| `--label` | `OOMPA_LABEL` | `good-for-ai` | Issue label to watch |
| `--clone-dir` | `OOMPA_CLONE_DIR` | `~/oompa-work` | Working directory for clones and worktrees |
| `--poll-interval` | `OOMPA_POLL_INTERVAL` | `2m` | How often to poll GitHub |
| `--log-level` | `OOMPA_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `--log-file` | `OOMPA_LOG_FILE` | stderr | Write logs to a file instead of stderr |
| `--signed-off-by` | `OOMPA_SIGNED_OFF_BY` | auto-detected | `Signed-off-by` line for commits |
| `--reviewers` | `OOMPA_REVIEWERS` | all | Comma-separated allowlist of reviewers to respond to |
| `--create-flaky-issues` | `OOMPA_CREATE_FLAKY_ISSUES` | `false` | Create issues for unrelated CI failures (opt-in) |
| `--dry-run` | — | `false` | Log actions without executing them |
| `--one-shot` | — | `false` | Run one poll cycle and exit |
| — | `GITHUB_TOKEN` | *required (PAT)* | GitHub personal access token |
| `--github-app-id` | `GITHUB_APP_ID` | — | GitHub App ID |
| `--github-app-private-key` | `GITHUB_APP_PRIVATE_KEY_PATH` | — | Path to GitHub App private key PEM file |
| `--github-app-installation-id` | `GITHUB_APP_INSTALLATION_ID` | — | GitHub App installation ID |
| `--vertex-region` | `CLOUD_ML_REGION` | *required* | GCP Vertex AI region |
| `--vertex-project` | `ANTHROPIC_VERTEX_PROJECT_ID` | *required* | GCP project ID for Vertex AI |

`GITHUB_TOKEN` is required when not using GitHub App auth. When all three `--github-app-*` flags are provided, the agent uses App auth instead.

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
