#!/usr/bin/env bash
set -euo pipefail

# Stream stdin directly to a marker file so tests can assert prompt delivery.
# Using cat > file avoids echo's flag interpretation (-n, -e) and keeps memory
# usage bounded for large prompts.
cat > .oompa-stdin-marker

# If stdin was empty, exit with error — the prompt must be delivered via stdin.
if [ ! -s .oompa-stdin-marker ]; then
  printf '%s\n' '{"type":"result","subtype":"error","is_error":true,"result":"empty stdin"}'
  exit 1
fi

echo "e2e fix" > FIX.md
git add FIX.md
git commit -q -m "e2e: implement issue"     # MUST produce a commit (issues.go:198 checks origin/main..HEAD non-empty)
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"done"}'
