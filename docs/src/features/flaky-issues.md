# Flaky Issues

When oompa detects CI failures unrelated to a PR's changes, it can create GitHub issues to track these flaky tests.

## How It Works

1. During CI investigation, the agent classifies a failure as **unrelated** to the PR
2. If `--create-flaky-issues` is enabled, oompa creates a GitHub issue with:
   - The failing test name and error details
   - Root-cause analysis from the coding agent
   - A link to the CI run
3. Issues are deduplicated -- if an issue already exists for the same failure, a new one is not created

## Configuration

**CLI:**

```bash
./oompa \
  --repo myorg/myrepo \
  --create-flaky-issues \
  --flaky-label kind/ci-flake
```

**YAML config file:**

```yaml
projects:
  - repo: myorg/myrepo
    create-flaky-issues: true
    flaky-label: kind/ci-flake
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--create-flaky-issues` | `false` | Enable flaky issue creation (opt-in) |
| `--flaky-label` | `flaky-test` | Label to apply to created issues |

## Deduplication

Oompa checks existing open issues with the flaky label before creating a new one. If a matching issue already exists (same test name or failure signature), the existing issue is updated rather than creating a duplicate.

## Periodic Triage Integration

Flaky issue creation also works with the [periodic triage](../roles/periodic-triage.md) role, where it tracks failures from scheduled CI jobs rather than PR-triggered CI.
