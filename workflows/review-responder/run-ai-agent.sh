#!/bin/bash

set -euo pipefail

RELEASE_REPO="qinqon/github-issue-resolver"

BINARY_NAME="ai-agent-linux-amd64"

INSTALL_DIR="${AI_AGENT_INSTALL_DIR:-/tmp/bin}"

BINARY_PATH="${INSTALL_DIR}/${BINARY_NAME}"

mkdir -p "${INSTALL_DIR}"

while true; do

    echo "Downloading latest ai-agent binary..."

    # Download to a temp file first to avoid "text file busy" when overwriting a running binary
    gh release download \
        --repo "${RELEASE_REPO}" \
        --pattern "${BINARY_NAME}" \
        --dir "${INSTALL_DIR}/tmp-download" \
        --clobber

    mv "${INSTALL_DIR}/tmp-download/${BINARY_NAME}" "${BINARY_PATH}"

    chmod +x "${BINARY_PATH}"

    echo "Starting ai-agent..."

    "${BINARY_PATH}" --exit-on-new-version="${RELEASE_REPO}" "$@" || true

    echo "Agent exited, restarting in 5 seconds..."

    sleep 5

done
