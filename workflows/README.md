# Ambient Code Workflows

These workflows are designed for the [Ambient Code platform](https://github.com/ambient-code/platform).
Each subdirectory is a self-contained workflow selectable in the Ambient GUI.

## Workflows

| Workflow | Description | Key Flags |
|----------|-------------|-----------|
| `issue-resolver` | Watches for labeled issues, implements fixes, creates PRs | `--label` |
| `pr-babysitter` | Monitors specific PRs: reviews + CI + conflicts | `--watch-prs` |
| `ci-fixer` | Monitors specific PRs: CI failures only | `--watch-prs`, `--reactions ci` |
| `review-responder` | Monitors specific PRs: review comments only | `--watch-prs`, `--reactions reviews` |

## Required Session Configuration

All workflows require these environment variables in the Ambient session:

| Variable | Description |
|----------|-------------|
| `AI_AGENT_REPO` | Target repo as `owner/repo` |
| `GITHUB_TOKEN` | GitHub PAT with repo scope (or use GitHub App vars below) |
| `CLOUD_ML_REGION` | GCP Vertex AI region (e.g. `us-east5`) |
| `ANTHROPIC_VERTEX_PROJECT_ID` | GCP project ID for Vertex AI |

PR-based workflows (`pr-babysitter`, `ci-fixer`, `review-responder`) also need:

| Variable | Description |
|----------|-------------|
| `AI_AGENT_WATCH_PRS` | Comma-separated PR numbers (e.g. `42,53,71`) |

### GitHub App Authentication (alternative to PAT)

| Variable | Description |
|----------|-------------|
| `GITHUB_APP_ID` | GitHub App ID |
| `GITHUB_APP_PRIVATE_KEY` | PEM content |
| `GITHUB_APP_INSTALLATION_ID` | Installation ID |

## How It Works

1. Select a workflow subdirectory in the Ambient GUI.
2. Configure the required environment variables in session settings.
3. The Ambient platform starts a Claude Code session with the workflow directory as CWD.
4. Claude Code reads `.ambient/ambient.json` and executes `../run-ai-agent.sh` with the appropriate flags.
5. The runner script downloads the latest `ai-agent` binary from GitHub releases and runs it in a restart loop with auto-update support.
