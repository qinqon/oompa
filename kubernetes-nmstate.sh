#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
IMAGE="${IMAGE:-ghcr.io/qinqon/github-issue-resolver:local}"

NMSTATE_AI=$(gh api /orgs/nmstate/installations --jq '.installations[] | select(.app_slug == "nmstate-ai") | {app_id, id}')
export GITHUB_APP_ID="${GITHUB_APP_ID:-$(echo "$NMSTATE_AI" | jq -r '.app_id')}"
export GITHUB_APP_INSTALLATION_ID="${GITHUB_APP_INSTALLATION_ID:-$(echo "$NMSTATE_AI" | jq -r '.id')}"
export GITHUB_APP_PRIVATE_KEY_PATH="${GITHUB_APP_PRIVATE_KEY_PATH:-$HOME/.secrets/nmstate-ai.2026-04-10.private-key.pem}"

echo "Building image..."
make -C "${SCRIPT_DIR}" image IMAGE="${IMAGE}"

LOG_FILE="/tmp/agent.kubernetes-nmstate.log"
echo "Starting agent (logging to ${LOG_FILE})..."
podman run --rm --userns=keep-id \
    -v "${GITHUB_APP_PRIVATE_KEY_PATH}:/secrets/app.pem:ro,Z" \
    -v ai-agent-kubernetes-nmstate:/work \
    -v "${GCP_SA_KEY_PATH:-$HOME/.secrets/nmstate-ai-agent-vertex.json}:/secrets/gcp-sa.json:ro,Z" \
    -e GOOGLE_APPLICATION_CREDENTIALS=/secrets/gcp-sa.json \
    -e GITHUB_APP_ID="${GITHUB_APP_ID}" \
    -e GITHUB_APP_INSTALLATION_ID="${GITHUB_APP_INSTALLATION_ID}" \
    -e GITHUB_APP_PRIVATE_KEY_PATH=/secrets/app.pem \
    -e CLOUD_ML_REGION="${CLOUD_ML_REGION:-us-east5}" \
    -e ANTHROPIC_VERTEX_PROJECT_ID="${ANTHROPIC_VERTEX_PROJECT_ID:-itpc-gcp-hcm-pe-eng-claude}" \
    "${IMAGE}" \
    --owner nmstate --repo kubernetes-nmstate \
    --fork-owner nmstate --fork-repo kubernetes-nmstate-ci \
    --clone-dir /work \
    --log-level debug --poll-interval 30s \
    --reviewers "mkowalski,emy,qinqon,gemini-code-assist[bot]" \
    2>&1 | tee "${LOG_FILE}"
