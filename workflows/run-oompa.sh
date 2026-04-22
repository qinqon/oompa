#!/bin/bash

set -euo pipefail

RELEASE_REPO="qinqon/oompa"

BINARY_NAME="oompa-linux-amd64"

INSTALL_DIR="${OOMPA_INSTALL_DIR:-/tmp/bin}"

BINARY_PATH="${INSTALL_DIR}/${BINARY_NAME}"

mkdir -p "${INSTALL_DIR}"

CHILD_PID=0

cleanup() {
    if [ "$CHILD_PID" -ne 0 ]; then
        kill "$CHILD_PID" 2>/dev/null || true
        wait "$CHILD_PID" 2>/dev/null || true
    fi
    exit 0
}

trap cleanup SIGTERM SIGINT

while true; do

    echo "Downloading latest oompa binary..."

    # Download to a temp file first to avoid "text file busy" when overwriting a running binary
    gh release download \
        --repo "${RELEASE_REPO}" \
        --pattern "${BINARY_NAME}" \
        --dir "${INSTALL_DIR}/tmp-download" \
        --clobber

    mv "${INSTALL_DIR}/tmp-download/${BINARY_NAME}" "${BINARY_PATH}"

    chmod +x "${BINARY_PATH}"

    echo "Starting oompa..."

    "${BINARY_PATH}" --exit-on-new-version="${RELEASE_REPO}" "$@" &
    CHILD_PID=$!
    wait "$CHILD_PID"
    EXIT_CODE=$?
    CHILD_PID=0

    # If killed by a signal (exit code > 128), propagate instead of restarting
    if [ "$EXIT_CODE" -gt 128 ]; then
        echo "Agent killed by signal (exit code $EXIT_CODE), exiting..."
        exit "$EXIT_CODE"
    fi

    echo "Agent exited (code $EXIT_CODE), restarting in 5 seconds..."

    sleep 5

done
