# Periodic Triage

The periodic triage role analyzes failures from scheduled CI jobs (nightly builds, periodic e2e tests) and creates issues with root-cause analysis.

## How It Works

1. Oompa monitors specified CI job URLs or GitHub Actions workflows
2. On each triage cycle, it fetches recent failed runs within the lookback window
3. For each failure, the coding agent analyzes logs and classifies the root cause
4. Issues are created with the analysis, deduplicating against existing issues

## Configuration

**CLI (one-shot):**

```bash
./oompa \
  --repo myorg/myrepo \
  --triage-jobs https://prow.example.com/view/gs/bucket/logs/periodic-e2e-job/ \
  --triage-lookback 24h \
  --create-flaky-issues \
  --one-shot
```

**YAML config file (Prow jobs):**

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

**YAML config file (GitHub Actions lane-level):**

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

## Triage Modes

### Prow Job URLs

Specify full URLs to Prow job pages. Oompa fetches and analyzes all failed runs within the lookback window.

### GitHub Actions Workflows (Lane-Level)

For GitHub Actions, use `workflow` + `lanes` to monitor specific matrix lanes within a high-volume workflow:

- `workflow`: GHA workflow file name relative to the repo (e.g., `test.yml`)
- `lanes`: Glob patterns matched against job names (supports trailing `*` wildcard)

This avoids triaging all 42+ jobs in a matrix workflow when you only care about specific lanes.

**Behavior:**
- Runs with `conclusion=success` are skipped (no API call needed)
- Only matching lanes with `conclusion=failure` are reported
- Logs are fetched per-lane, not per-workflow
- State dedup keys are per-lane (e.g., `runID:laneName`)

## Validation

A triage entry must have either `jobs` or (`workflow` + `lanes`), not both and not neither. `workflow` without `lanes` is invalid. `lanes` without `workflow` is invalid.

## Scheduling

For scheduled runs, use a systemd timer or cron job with `--one-shot`:

```bash
./oompa --repo myorg/myrepo --triage-jobs ... --one-shot
```

See [Systemd](../operations/systemd.md) for a complete timer setup.
