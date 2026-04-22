#!/bin/bash
set -euo pipefail

NAMESPACE="oompa"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Validate required files
GITHUB_APP_KEY="${GITHUB_APP_KEY:-}"
GCP_CREDENTIALS="${GCP_CREDENTIALS:-}"
VERTEX_PROJECT="${VERTEX_PROJECT:-}"

if [ -z "${GITHUB_APP_KEY}" ]; then
    echo "Error: GITHUB_APP_KEY must point to the GitHub App private key PEM file"
    echo "Usage: GITHUB_APP_KEY=/path/to/app.pem GCP_CREDENTIALS=/path/to/creds.json VERTEX_PROJECT=my-project $0"
    exit 1
fi

if [ -z "${GCP_CREDENTIALS}" ]; then
    echo "Error: GCP_CREDENTIALS must point to the GCP service account key JSON file"
    exit 1
fi

if [ -z "${VERTEX_PROJECT}" ]; then
    echo "Error: VERTEX_PROJECT must be set to the GCP project ID"
    exit 1
fi

echo "Creating namespace..."
kubectl apply -f "${SCRIPT_DIR}/namespace.yaml"

echo "Creating secrets..."
kubectl create secret generic github-app-key \
    --namespace "${NAMESPACE}" \
    --from-file=app.pem="${GITHUB_APP_KEY}" \
    --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic gcp-credentials \
    --namespace "${NAMESPACE}" \
    --from-file=credentials.json="${GCP_CREDENTIALS}" \
    --dry-run=client -o yaml | kubectl apply -f -

echo "Deploying agent..."
sed "s|value: \"\"  # TODO: set your GCP project ID|value: \"${VERTEX_PROJECT}\"|" \
    "${SCRIPT_DIR}/deployment.yaml" | kubectl apply -f -

echo "Waiting for rollout..."
kubectl rollout status deployment/oompa --namespace "${NAMESPACE}" --timeout=120s

echo ""
echo "Deployed. View logs with:"
echo "  kubectl logs -f deployment/oompa -n ${NAMESPACE}"
