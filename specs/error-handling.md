# Error Handling and Safety

## Design Intent

The error strategy prioritizes **human visibility over automatic recovery**. When Claude fails, the agent makes the failure visible on GitHub (label + comment) rather than silently retrying, because a failed AI attempt likely means the problem needs human judgment. Transient GitHub API errors are safe to retry automatically because the next poll cycle is idempotent.

## Error Handling

- **Claude failure**: Add `ai-failed` label to issue, comment with error, skip. Human removes label + re-adds `good-for-ai` to retry. Rationale: failed AI attempts are unlikely to succeed on immediate retry without human guidance.
- **Comment-only fix**: If Claude's commits change only comments or whitespace (no functional code), treat as a failed fix: add `ai-failed` label, do not push or create a PR. Rationale: a comment-only diff means the agent could not produce a real fix; opening a PR would waste reviewer time. Detection is conservative -- any non-comment changed line means the fix is treated as real, directive-style lines (shebangs, `//nolint`, `//go:`, build tags) count as functional, and `git diff` errors are logged and fail open (the fix proceeds).
- **GitHub API failure**: Log and skip, retry on next poll cycle. Rationale: API errors are typically transient; the polling loop provides natural retry.
- **Process restart**: State is rebuilt from GitHub on startup. Worktrees persist on disk. Rationale: GitHub is the source of truth, not local state files.
- **Infinite loop prevention**: Bot's own comments are filtered via `<!-- oompa-bot -->` markers. No CI-failure auto-retry beyond configured limits. Rationale: unbounded retries waste API credits and pollute PR history.

## Safety Invariants

- Claude never merges -- only creates PRs. This ensures a human reviews every change.
- No force-push in prompts. Branch history is preserved for auditability.
- `--dangerously-skip-permissions` is acceptable since this runs unattended on a trusted server.
- Uses Vertex AI -- billing goes through your GCP project, controlled by GCP IAM and quotas.
- Sequential processing avoids race conditions on shared git state.
