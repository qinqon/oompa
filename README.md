# github-issue-resolver

A single long-running Go binary that automatically resolves GitHub issues using Claude AI. It polls for issues with a configurable label, invokes Claude Code in headless mode to implement fixes, and opens pull requests — no webhooks or per-issue goroutines required.

## How it works

1. **Poll** — watches for open issues tagged with a label (default `good-for-ai`).
2. **Implement** — creates a git worktree, runs Claude Code (`claude -p`) to produce a fix.
3. **Open PR** — pushes the branch and creates a pull request linked to the issue.
4. **Address reviews** — picks up reviewer comments, runs Claude again to iterate.
5. **Rebase conflicts** — detects PRs with merge conflicts, attempts an automatic rebase, and falls back to Claude if that fails.
6. **Handle CI failures** — detects CI failures and asks Claude to fix them.

Claude never merges; a human must approve and merge every PR.

## Loop flow

```mermaid
flowchart TD
    Start([Start]) --> Cleanup

    subgraph Cleanup [CleanupDone]
        C1[Iterate active issues] --> C2{PR merged\nor closed?}
        C2 -- yes --> C3[Remove worktree\nRemove from state]
        C2 -- no --> C4[Skip]
    end

    Cleanup --> NewIssues

    subgraph NewIssues [ProcessNewIssues]
        N1[List issues with label] --> N2{Already\ntracked?}
        N2 -- yes --> N3[Skip]
        N2 -- no --> N4["Post 'working on it' comment\nClone repo & create worktree\nBranch: ai/issue-N"]
        N4 --> N5[Run Claude to implement fix]
        N5 --> N6{Claude\nsucceeded?}
        N6 -- yes --> N7[Find PR created by Claude\nTrack as pr-open]
        N6 -- no --> N8["Add ai-failed label\nComment with error\nTrack as failed"]
    end

    NewIssues --> Reviews

    subgraph Reviews [ProcessReviewComments]
        R1[Iterate open PRs] --> R2[Fetch new review comments\nFilter by allowed reviewers]
        R2 --> R3{New\ncomments?}
        R3 -- no --> R4[Skip]
        R3 -- yes --> R5["React with :eyes: to each\nSync worktree"]
        R5 --> R6[Run Claude to address comments]
        R6 --> R7[Post fallback reply for\nunanswered comments]
    end

    Reviews --> Conflicts

    subgraph Conflicts [ProcessConflicts]
        CF1[Iterate open PRs] --> CF2{Merge state\ndirty?}
        CF2 -- no --> CF3[Skip]
        CF2 -- yes --> CF4[Sync worktree\nTry git rebase origin/main]
        CF4 --> CF5{Rebase\nsucceeded?}
        CF5 -- yes --> CF6[Push with --force-with-lease\nComment: resolved by rebase]
        CF5 -- no --> CF7[Abort rebase\nRun Claude to resolve]
        CF7 --> CF8{Claude pushed\nnew commits?}
        CF8 -- yes --> CF9[Comment: conflicts resolved]
        CF8 -- no --> CF10[Comment: human intervention needed]
    end

    Conflicts --> CI

    subgraph CI [ProcessCIFailures]
        CI1[Iterate open PRs] --> CI2{Fix attempts\n>= 3?}
        CI2 -- yes --> CI3[Comment: human intervention needed]
        CI2 -- no --> CI4[Get check runs for HEAD]
        CI4 --> CI5{Completed\nfailures?}
        CI5 -- no --> CI6[Skip]
        CI5 -- yes --> CI7[Fetch failing check logs\nSync worktree\nRun Claude to investigate]
        CI7 --> CI8{Claude says\nUNRELATED?}
        CI8 -- yes --> CI9[Comment: failure unrelated to PR]
        CI8 -- no --> CI10{Claude\npushed fix?}
        CI10 -- yes --> CI11[Comment: pushed a fix]
        CI10 -- no --> CI12[Comment: could not fix]
    end

    CI --> OneShot{--one-shot?}
    OneShot -- yes --> Stop([Stop])
    OneShot -- no --> Sleep["Sleep(poll-interval)"]
    Sleep --> Cleanup
```

## Prerequisites

- Go 1.25+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and on `PATH`
- Google Cloud Application Default Credentials configured (`gcloud auth application-default login` or a service account key)
- A GitHub personal access token with repo scope

## Build

```bash
go build -o ai-agent ./cmd/ai-agent
```

## Usage

```bash
export GITHUB_TOKEN="ghp_..."
export CLOUD_ML_REGION="us-east5"
export ANTHROPIC_VERTEX_PROJECT_ID="my-gcp-project"

./ai-agent --owner myorg --repo myrepo
```

### Flags and environment variables

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `--owner` | `AI_AGENT_OWNER` | `openperouter` | GitHub repo owner |
| `--repo` | `AI_AGENT_REPO` | `openperouter` | GitHub repo name |
| `--label` | `AI_AGENT_LABEL` | `good-for-ai` | Issue label to watch |
| `--clone-dir` | `AI_AGENT_CLONE_DIR` | `~/ai-agent-work` | Working directory for clones and worktrees |
| `--poll-interval` | `AI_AGENT_POLL_INTERVAL` | `2m` | How often to poll GitHub |
| `--log-level` | `AI_AGENT_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `--log-file` | `AI_AGENT_LOG_FILE` | stderr | Write logs to a file instead of stderr |
| `--signed-off-by` | `AI_AGENT_SIGNED_OFF_BY` | auto-detected | `Signed-off-by` line for commits |
| `--reviewers` | `AI_AGENT_REVIEWERS` | all | Comma-separated allowlist of reviewers to respond to |
| `--dry-run` | — | `false` | Log actions without executing them |
| `--one-shot` | — | `false` | Run one poll cycle and exit |
| — | `GITHUB_TOKEN` | *required* | GitHub personal access token |
| `--vertex-region` | `CLOUD_ML_REGION` | *required* | GCP Vertex AI region |
| `--vertex-project` | `ANTHROPIC_VERTEX_PROJECT_ID` | *required* | GCP project ID for Vertex AI |

## State

The agent is stateless on disk. On every startup it rebuilds its state from GitHub by scanning labeled issues and matching PRs, so there is nothing to back up or migrate.

## Testing

```bash
go test ./...
```

All external interactions (GitHub API, Claude CLI, git) are behind interfaces with mock implementations, so tests run without any credentials or network access.

## Project layout

```
cmd/ai-agent/       CLI entry point
pkg/agent/          Core logic (loop, state, GitHub client, Claude runner, worktree, prompts)
specs/              Design specifications for each component
```

## Safety

- Claude only creates PRs — it never merges.
- No force-pushes.
- On failure, the issue is labeled `ai-failed` with a comment explaining the error. A human removes the label and re-adds `good-for-ai` to retry.
- Billing is controlled through GCP IAM on the Vertex AI project.
