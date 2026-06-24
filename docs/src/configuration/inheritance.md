# Inheritance

Oompa uses two-tier inheritance for configuration: **global** settings flow down to **project** level, which flow down to **role** level.

## Hierarchy

```
Global Settings
  └─ Project Settings (override global)
       └─ Role Settings (override project)
```

## How It Works

1. **Global settings** are defined at the top level of the YAML config file
2. **Project settings** override global settings for all roles in that project
3. **Role settings** override project settings for that specific role

## Example

```yaml
# Global: all projects use Opus by default
agent-model: google-vertex-anthropic/claude-opus-4-6@default

projects:
  - repo: myorg/expensive-repo
    # Project: override to Sonnet (cheaper) for all roles
    agent-model: google-vertex-anthropic/claude-sonnet-4-20250514
    reviewers: [alice, bob]

    prs:
      - watch: [100]
        reactions: [ci, conflicts]
        # Role: inherits agent-model from project (Sonnet)
        # Role: inherits reviewers from project ([alice, bob])

      - watch: [200]
        # Role: override reviewers for this specific PR group
        reviewers: [alice]

  - repo: myorg/critical-repo
    # Project: inherits agent-model from global (Opus)
    issues:
      - label: good-for-ai
        # Role: inherits agent-model from global (Opus)
```

## Inheritable Fields

| Field | Global | Project | Role |
|-------|--------|---------|------|
| `agent` | Y | - | - |
| `agent-model` | Y | Y | - |
| `poll-interval` | Y | - | - |
| `log-level` | Y | - | - |
| `reviewers` | - | Y | Y |
| `create-flaky-issues` | - | Y | Y |
| `flaky-label` | - | Y | Y |
| `rebase-interval` | - | Y | Y |
| `skip-comment` | - | - | Y |
| `reactions` | - | - | Y |

## Rebase Interval

The `rebase-interval` field controls the minimum time between rebases for a PR. It defaults to `4h` when not specified at either level.

```yaml
projects:
  - repo: myorg/myrepo
    rebase-interval: 24h    # project default: at most once per day
    prs:
      - watch: [100]
        reactions: [ci, conflicts, rebase]
        # inherits rebase-interval: 24h from project

      - watch: [200]
        rebase-interval: 12h  # override: twice per day for this group
```

Non-positive values (zero or negative) are rejected at config load time.
