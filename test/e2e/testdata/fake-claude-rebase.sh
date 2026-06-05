#!/usr/bin/env bash
set -euo pipefail

# Stream stdin directly to marker file (avoids echo flag interpretation).
cat > .oompa-stdin-marker

# For rebase scenarios: resolve conflicts by accepting the current branch version.
# This script is only called if automatic rebase fails with conflicts.
git add -A
if git rebase --continue 2>/dev/null; then
  echo "rebase-continued" > .oompa-rebase-marker
elif git commit --allow-empty -m "resolve conflicts" 2>/dev/null; then
  echo "commit-fallback" > .oompa-rebase-marker
else
  echo "all-failed" > .oompa-rebase-marker
fi

printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"done"}'
