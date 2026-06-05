#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null                              # consume the prompt on stdin
echo "e2e fix" > FIX.md
git add FIX.md
git commit -q -m "e2e: implement issue"     # MUST produce a commit (issues.go:198 checks origin/main..HEAD non-empty)
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"done"}'
