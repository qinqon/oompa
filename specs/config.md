# Configuration

## Config Struct

```go
type Config struct {
    Owner         string
    Repo          string
    Label         string
    CloneDir      string
    StatePath     string
    PollInterval  time.Duration
    VertexRegion  string
    VertexProject string
    LogLevel      string
    DryRun        bool
}
```

## Environment Variables and Flags

| Env Var | Flag | Default | Description |
|---------|------|---------|-------------|
| `AI_AGENT_OWNER` | `--owner` | `openperouter` | GitHub repo owner |
| `AI_AGENT_REPO` | `--repo` | `openperouter` | GitHub repo name |
| `AI_AGENT_LABEL` | `--label` | `good-for-ai` | Issue label to watch |
| `AI_AGENT_CLONE_DIR` | `--clone-dir` | `~/ai-agent-work` | Clone/worktree directory |
| `AI_AGENT_STATE_PATH` | `--state-path` | `~/.ai-agent-state.json` | State file |
| `AI_AGENT_POLL_INTERVAL` | `--poll-interval` | `2m` | Poll frequency |
| `GITHUB_TOKEN` | -- | (required) | GitHub PAT for go-github client |
| `CLOUD_ML_REGION` | `--vertex-region` | (required) | GCP Vertex AI region |
| `ANTHROPIC_VERTEX_PROJECT_ID` | `--vertex-project` | (required) | GCP project ID for Vertex |

Config from env vars (`AI_AGENT_*`) with flag overrides, read in `main()`.

## Required External Setup

- Google Cloud ADC configured (`gcloud auth application-default login` or service account key)
- `CLAUDE_CODE_USE_VERTEX=1` is set automatically by the agent when invoking Claude
- `GITHUB_TOKEN` is used by both the go-github client and passed to Claude for `gh pr create`
