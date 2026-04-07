# Error Handling and Safety

## Error Handling

- **Claude failure**: Add `ai-failed` label to issue, comment with error, skip. Human removes label + re-adds `good-for-ai` to retry.
- **GitHub API failure**: Log and skip, retry on next poll cycle.
- **Process restart**: State file ensures pickup where left off. Worktrees persist on disk.
- **Infinite loop prevention**: Bot's own comments filtered out. No CI-failure auto-retry.

## Safety

- Claude never merges -- only creates PRs
- No force-push in prompts
- `--dangerously-skip-permissions` is acceptable since this runs unattended on a trusted server
- Uses Vertex AI -- billing goes through your GCP project, controlled by GCP IAM and quotas
- Sequential processing avoids race conditions
