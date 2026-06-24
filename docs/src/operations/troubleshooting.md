# Troubleshooting

## Common Issues

### "GITHUB_TOKEN not set" or Authentication Failures

**Symptom:** Oompa fails to authenticate with GitHub.

**Solutions:**
1. Run `gh auth login` and `gh auth setup-git`
2. Or set `GITHUB_TOKEN` environment variable
3. Or configure all three GitHub App flags (`--github-app-id`, `--github-app-private-key`, `--github-app-installation-id`)

### Agent Fails to Run

**Symptom:** The coding agent (OpenCode or Claude Code) fails to start.

**Solutions:**
1. Verify the agent is on `PATH`: `which opencode` or `which claude`
2. Check provider credentials: `gcloud auth application-default print-access-token`
3. Verify the compound-engineering plugin is installed
4. Check `--agent-model` is valid for your provider

### Issue Labeled `ai-failed`

**Symptom:** An issue gets the `ai-failed` label with an error comment.

**Resolution:**
1. Read the error comment on the issue for details
2. Fix any blockers (unclear requirements, missing context)
3. Remove the `ai-failed` label
4. Re-add `good-for-ai` (or your configured label)

Oompa will pick up the issue on the next poll cycle.

### PR Stuck in Conflict Loop

**Symptom:** A PR keeps getting rebased but conflicts keep recurring.

**Solutions:**
1. Increase `rebase-interval` to reduce frequency
2. Check if the base branch has frequent conflicting changes
3. Consider manually resolving the conflicts

### Worktree Errors

**Symptom:** Git worktree operations fail.

**Solutions:**
1. Check disk space in `--clone-dir`
2. Clean up stale worktrees: `git worktree prune`
3. Verify the clone directory is writable

### Rate Limiting

**Symptom:** GitHub API errors with 403 status.

**Solutions:**
1. Increase `--poll-interval` to reduce API call frequency
2. Switch to GitHub App auth (higher rate limits than PAT)
3. Check `X-RateLimit-Remaining` in debug logs

### Self-Reply Loop

**Symptom:** Oompa keeps responding to its own comments.

**Prevention:** Bot-posted comments are tagged with `<!-- oompa-bot -->` markers. If you see self-replies, check that the marker is present in the comment template.

## Debugging

### Enable Debug Logging

```bash
./oompa --repo myorg/myrepo --log-level debug
```

### Dry Run

Test what oompa would do without making changes:

```bash
./oompa --repo myorg/myrepo --dry-run --one-shot
```

### Single Poll Cycle

Run one iteration and exit:

```bash
./oompa --repo myorg/myrepo --one-shot
```

### Check State

Oompa is stateless on disk -- it rebuilds state from GitHub on startup. To see what oompa tracks, enable debug logging and look for state-related messages:

```bash
./oompa --repo myorg/myrepo --log-level debug --one-shot 2>&1 | grep "state"
```

## Getting Help

If you encounter an issue not covered here, [open a GitHub issue](https://github.com/qinqon/oompa/issues/new) with:

1. The oompa version (`oompa --version` or commit hash)
2. Debug-level logs (with any tokens/secrets redacted)
3. The configuration you are using
4. Steps to reproduce the issue
