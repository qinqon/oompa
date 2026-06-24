# Configuration Examples

## Single Repository (CLI Only)

No config file needed -- just use CLI flags:

```bash
./oompa \
  --agent opencode \
  --agent-model google-vertex-anthropic/claude-opus-4-6@default \
  --repo myorg/myrepo \
  --poll-interval 2m \
  --log-level info
```

## Multi-Project Config File

Manage multiple repositories from a single oompa process:

```yaml
agent: opencode
agent-model: google-vertex-anthropic/claude-opus-4-6@default
poll-interval: 2m
log-level: debug
exit-on-new-version: qinqon/oompa

projects:
  - repo: myorg/frontend
    issues:
      - label: good-for-ai
        reviewers: [alice, bob]

  - repo: myorg/backend
    fork: myuser/backend
    issues:
      - label: good-for-ai
        only-assigned: true
    prs:
      - watch: [42, 55]
        reactions: [ci, conflicts, rebase]
```

## PR Babysitter With Fork

Monitor PRs in a repo where you push to a fork:

```bash
./oompa \
  --repo upstream/repo \
  --fork myuser/repo \
  --watch-prs 123,456 \
  --reactions ci,conflicts,rebase \
  --poll-interval 2m
```

Or in YAML:

```yaml
projects:
  - repo: upstream/repo
    fork: myuser/repo
    prs:
      - watch: [123, 456]
        reactions: [ci, conflicts, rebase]
```

## Cost-Optimized Setup

Use a cheaper model for report-only roles, and the full model for issue resolution:

```yaml
agent: opencode
agent-model: google-vertex-anthropic/claude-opus-4-6@default

projects:
  - repo: myorg/myrepo
    # Sonnet for PR babysitting (cheaper)
    agent-model: google-vertex-anthropic/claude-sonnet-4-20250514
    prs:
      - watch: [100, 200]
        reactions: [ci, conflicts, rebase]

  - repo: myorg/myrepo
    # Opus for issue resolution (full power)
    issues:
      - label: good-for-ai
```

## Periodic CI Triage

### Prow Jobs

```yaml
projects:
  - repo: myorg/myrepo
    create-flaky-issues: true
    flaky-label: kind/ci-flake
    triage:
      - jobs:
          - https://prow.example.com/view/gs/bucket/logs/periodic-e2e-job/
        schedule: "09:00 Europe/Madrid"
        lookback: 24h
```

### GitHub Actions Workflows (Lane-Level)

Filter specific matrix lanes within a workflow:

```yaml
projects:
  - repo: myorg/myrepo
    flaky-label: kind/ci-flake
    triage:
      - workflow: test.yml
        lanes:
          - "e2e (live-migration, noHA, local,*"
          - "e2e (live-migration, noHA, shared,*"
        schedule: "09:00 Europe/Madrid"
        lookback: 24h
```

## Suppressing Comment Categories

Reduce noise by suppressing specific comment types:

```yaml
projects:
  - repo: myorg/myrepo
    prs:
      - watch: [123]
        reactions: [ci, conflicts, rebase]
        skip-comment: [ci-unrelated, ci-infrastructure]
```

Available categories: `ci-unrelated`, `ci-infrastructure`, `ci-related`, `conflict`, `rebase`, `flaky`, `issue-in-progress`.

## GitHub App Authentication

```bash
export GITHUB_APP_ID="123456"
export GITHUB_APP_PRIVATE_KEY_PATH="/path/to/private-key.pem"
export GITHUB_APP_INSTALLATION_ID="78901234"

./oompa \
  --agent opencode \
  --agent-model google-vertex-anthropic/claude-opus-4-6@default \
  --repo myorg/myrepo
```
