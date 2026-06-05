#!/usr/bin/env bash
set -euo pipefail

# Stream stdin directly to marker file (avoids echo flag interpretation).
cat > .oompa-stdin-marker

# For CI scenarios: just output classification without making changes.
# The CI handler looks for RELATED/UNRELATED/INFRASTRUCTURE keywords.
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"UNRELATED\nERROR_SUMMARY: Flaky network timeout\nROOT_CAUSE: Test depends on external service\nFAILING_TEST: TestNetworkTimeout\nRECOMMENDATION: Add retry logic"}'
