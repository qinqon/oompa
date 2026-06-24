# Monitoring

Oompa uses Go's `slog` for structured logging, making it easy to parse and filter logs in production.

## Log Levels

Set the log level with `--log-level`:

| Level | Description |
|-------|-------------|
| `debug` | Detailed operational info (API calls, git commands, agent invocations) |
| `info` | Standard operational events (issues processed, PRs created, reactions triggered) |
| `warn` | Non-critical issues (skipped items, rate limits approaching) |
| `error` | Failures requiring attention (API errors, agent failures) |

## Log Output

By default, logs go to stderr. Use `--log-file` to write to a file:

```bash
./oompa --repo myorg/myrepo --log-file /var/log/oompa/agent.log
```

## Structured Log Fields

Logs include structured fields for filtering:

```
time=2025-01-15T10:30:00Z level=INFO msg="processing issue" repo=myorg/myrepo issue=42 branch=ai/issue-42
time=2025-01-15T10:31:00Z level=INFO msg="PR created" repo=myorg/myrepo issue=42 pr=100
time=2025-01-15T10:32:00Z level=ERROR msg="agent failed" repo=myorg/myrepo issue=43 error="exit code 1"
```

## Filtering Logs

With `jq` (when using JSON output):

```bash
journalctl --user -u oompa-issue-resolver -o cat | jq 'select(.level == "ERROR")'
```

With `grep` (when using text output):

```bash
journalctl --user -u oompa-issue-resolver | grep "level=ERROR"
```

## Health Monitoring

### Systemd

Check service health:

```bash
systemctl --user status oompa-issue-resolver
journalctl --user -u oompa-issue-resolver --since "1 hour ago"
```

### Kubernetes

Monitor the pod:

```bash
kubectl logs -f deployment/oompa -n oompa
kubectl get pods -n oompa -w
```

## Auto-Update

Use `--exit-on-new-version=qinqon/oompa` to make oompa exit when a new release is available. Combined with systemd's `Restart=always`, this provides automatic binary updates:

1. Oompa detects a new release during polling
2. It exits cleanly
3. Systemd restarts the service
4. `ExecStartPre` downloads the new binary

## Multi-Project Logging

When running multiple projects from a YAML config file, each project/role runs as an independent goroutine with its own structured logger. Log entries include the `repo` field to distinguish between projects.
