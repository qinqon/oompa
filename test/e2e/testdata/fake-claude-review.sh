#!/usr/bin/env bash
set -euo pipefail

# Stream stdin directly to marker file (avoids echo flag interpretation).
cat > .oompa-stdin-marker

# For review scenarios: make a small change to simulate addressing feedback.
echo "review fix $(date +%s)" >> REVIEW-FIX.md
git add REVIEW-FIX.md
# Create a fixup commit that the review handler will autosquash
git commit -q -m "fixup! e2e: implement issue"

printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"done"}'
