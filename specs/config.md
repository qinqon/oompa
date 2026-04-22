# Configuration

## Config Struct

```go
type Config struct {
    Owner             string
    Repo              string
    Label             string
    CloneDir          string
    StatePath         string
    PollInterval      time.Duration
    VertexRegion      string
    VertexProject     string
    LogLevel          string
    DryRun            bool
    SignedOffBy       string
    Reviewers         []string // whitelist of users/bots whose reviews to address
    WatchPRs          []int    // PR numbers to monitor directly (bypasses issue discovery)
    Reactions         []string // which reactions to run: "reviews", "ci", "conflicts" (empty = all)
    CreateFlakyIssues bool     // when true, create issues for unrelated CI failures (opt-in)
}
```

## Environment Variables and Flags

| Env Var | Flag | Default | Description |
|---------|------|---------|-------------|
| `OOMPA_OWNER` | `--owner` | `openperouter` | GitHub repo owner |
| `OOMPA_REPO` | `--repo` | `openperouter` | GitHub repo name |
| `OOMPA_LABEL` | `--label` | `good-for-ai` | Issue label to watch |
| `OOMPA_CLONE_DIR` | `--clone-dir` | `~/oompa-work` | Clone/worktree directory |
| `OOMPA_STATE_PATH` | `--state-path` | `~/.oompa-state.json` | State file |
| `OOMPA_POLL_INTERVAL` | `--poll-interval` | `2m` | Poll frequency |
| `GITHUB_TOKEN` | -- | (required) | GitHub PAT for go-github client |
| `CLOUD_ML_REGION` | `--vertex-region` | (required) | GCP Vertex AI region |
| `ANTHROPIC_VERTEX_PROJECT_ID` | `--vertex-project` | (required) | GCP project ID for Vertex |
| `OOMPA_SIGNED_OFF_BY` | `--signed-off-by` | (GitHub user) | Signed-off-by for commits. Defaults to authenticated GitHub user's name and email |
| `OOMPA_REVIEWERS` | `--reviewers` | (empty = all) | Comma-separated whitelist of users/bots whose reviews to address |
| `OOMPA_WATCH_PRS` | `--watch-prs` | (empty) | Comma-separated PR numbers to monitor directly (bypasses issue discovery) |
| `OOMPA_REACTIONS` | `--reactions` | (empty = all) | Comma-separated list of reactions to run: `reviews`, `ci`, `conflicts` |
| `OOMPA_CREATE_FLAKY_ISSUES` | `--create-flaky-issues` | `false` | When true, create issues for unrelated CI failures (opt-in) |

Config from env vars (`OOMPA_*`) with flag overrides, read in `main()`.

## Required External Setup

- Google Cloud ADC configured (`gcloud auth application-default login` or service account key)
- `CLAUDE_CODE_USE_VERTEX=1` is set automatically by the agent when invoking Claude
- `GITHUB_TOKEN` is used by both the go-github client and passed to Claude for `gh pr create`
