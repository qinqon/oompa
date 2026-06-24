# Config File

For managing multiple repositories or running different roles per project, oompa supports a YAML configuration file.

## Usage

```bash
oompa --config config.yaml
```

## Format

The config file defines global settings and a list of projects. Each project can have multiple roles (issue resolvers, PR watchers, triage jobs).

```yaml
# Global settings (apply to all projects unless overridden)
agent: opencode
agent-model: google-vertex-anthropic/claude-opus-4-6@default
poll-interval: 2m
log-level: debug
exit-on-new-version: qinqon/oompa

projects:
  - repo: myorg/myrepo
    # Project-level settings override global
    agent-model: google-vertex-anthropic/claude-sonnet-4-20250514

    # Issue resolver role
    issues:
      - label: good-for-ai
        only-assigned: true

    # PR babysitter role
    prs:
      - watch: [123, 456]
        reactions: [ci, conflicts, rebase]

    # Periodic triage role
    triage:
      - jobs:
          - https://prow.example.com/nightly
        schedule: "09:00 Europe/Madrid"
        lookback: 24h
        create-flaky-issues: true
        flaky-label: ci-flake
```

## Project Fields

| Field | Type | Description |
|-------|------|-------------|
| `repo` | string | GitHub repo as `owner/repo` (required) |
| `fork` | string | Fork repo as `owner/repo` for pushing branches |
| `agent-model` | string | Model override for this project |
| `reviewers` | list | Allowlist of reviewers to respond to |
| `create-flaky-issues` | bool | Create issues for unrelated CI failures |
| `flaky-label` | string | Label for flaky CI issues |
| `rebase-interval` | duration | Minimum time between rebases (default: `4h`) |
| `issues` | list | Issue resolver role configurations |
| `prs` | list | PR babysitter role configurations |
| `triage` | list | Periodic triage role configurations |

## Issue Role Fields

| Field | Type | Description |
|-------|------|-------------|
| `label` | string | Issue label to watch |
| `only-assigned` | bool | Only process issues assigned to the agent |
| `reviewers` | list | Overrides project-level reviewers |

## PR Role Fields

| Field | Type | Description |
|-------|------|-------------|
| `watch` | list | PR numbers to monitor |
| `reactions` | list | Reactions to run: `reviews`, `ci`, `conflicts`, `rebase` |
| `skip-comment` | list | Comment categories to suppress |
| `reviewers` | list | Overrides project-level reviewers |
| `rebase-interval` | duration | Overrides project-level rebase interval |

## Triage Role Fields

| Field | Type | Description |
|-------|------|-------------|
| `jobs` | list | CI job URLs to monitor |
| `workflow` | string | GHA workflow file name (e.g. `test.yml`) |
| `lanes` | list | Glob patterns for matrix job filtering |
| `schedule` | string | Schedule as `HH:MM Timezone` |
| `lookback` | duration | Time window for failed runs |
| `create-flaky-issues` | bool | Create issues for flaky failures |
| `flaky-label` | string | Label for flaky issues |

A triage entry must have either `jobs` or (`workflow` + `lanes`), not both.

## Multi-Project Execution

Each project/role runs as an independent goroutine with its own Agent and structured logger. This means a single oompa process can manage multiple repositories simultaneously.

See [Inheritance](inheritance.md) for how settings cascade and [Examples](examples.md) for common patterns.
