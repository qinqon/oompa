NMSTATE_AI=$(gh api /orgs/nmstate/installations --jq '.installations[] | select(.app_slug == "nmstate-ai") | {app_id, id}')
export GITHUB_APP_ID="${GITHUB_APP_ID:-$(echo "$NMSTATE_AI" | jq -r '.app_id')}"
export GITHUB_APP_INSTALLATION_ID="${GITHUB_APP_INSTALLATION_ID:-$(echo "$NMSTATE_AI" | jq -r '.id')}"
export GITHUB_APP_PRIVATE_KEY_PATH="${GITHUB_APP_PRIVATE_KEY_PATH:-$HOME/.secrets/nmstate-ai.2026-04-10.private-key.pem}"

go run ./cmd/ai-agent/ --owner nmstate --repo kubernetes-nmstate --clone-dir "$HOME/ai-agent-work/kubernetes-nmstate" --log-level debug --poll-interval 30s --reviewers "mkowalski,emy,qinqon,gemini-code-assist" --log-file /tmp/agent.kubernetes-nmstate.log
