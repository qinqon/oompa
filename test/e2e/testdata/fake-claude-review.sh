#!/usr/bin/env bash
set -euo pipefail

# Stream stdin directly to a temp file (preserves trailing newlines, handles large inputs).
cat > .oompa-stdin-received

# If this is a change summary prompt (LLM summarization), just return
# a summary result without modifying any files or the stdin marker.
if grep -q "Summarize the following code diff" .oompa-stdin-received; then
    rm -f .oompa-stdin-received
    printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"- Addressed review feedback"}'
    exit 0
fi

# Move to marker file for review prompt verification by tests.
mv .oompa-stdin-received .oompa-stdin-marker

# For review scenarios: make a small change to simulate addressing feedback.
echo "review fix $(date +%s)" >> REVIEW-FIX.md
git add REVIEW-FIX.md
# Create a fixup commit that the review handler will autosquash
git commit -q -m "fixup! e2e: implement issue"

printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"done"}'
