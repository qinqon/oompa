# Authentication

Oompa requires authentication with both GitHub and an AI provider (Vertex AI or Anthropic API).

## GitHub Authentication

You have three options for GitHub authentication, listed from simplest to most robust.

### Option 1: `gh` CLI (Recommended for Development)

The simplest approach -- oompa falls back to `gh auth token` when no `GITHUB_TOKEN` is set:

```bash
gh auth login
gh auth setup-git
```

### Option 2: Personal Access Token

Set `GITHUB_TOKEN` with a token that has `repo` scope:

```bash
export GITHUB_TOKEN="ghp_..."
```

### Option 3: GitHub App (Recommended for Production)

GitHub Apps provide fine-grained permissions and don't consume a user's rate limit.

**Setup:**

1. Create a GitHub App in your org settings:
   `https://github.com/organizations/<org>/settings/apps/new`

2. Disable webhooks (oompa uses polling, not webhooks)

3. Grant repository permissions:
   - Actions: Read
   - Checks: Read
   - Contents: Read and Write
   - Issues: Read and Write
   - Metadata: Read
   - Pull requests: Read and Write

4. Generate a private key (PEM file)

5. Install the app on the target repository

6. Get the installation ID:
   ```bash
   gh api /orgs/<org>/installations \
     --jq '.installations[] | select(.app_slug | contains("<app-slug>")) | .id'
   ```

7. Configure oompa:
   ```bash
   export GITHUB_APP_ID="123456"
   export GITHUB_APP_PRIVATE_KEY_PATH="/path/to/private-key.pem"
   export GITHUB_APP_INSTALLATION_ID="78901234"
   ```

When all three `--github-app-*` flags are provided, the agent uses App auth instead of PAT auth. Installation tokens are automatically refreshed before each poll cycle.

The agent pushes branches directly to the upstream repository and authenticates as `<app-slug>[bot]`.

## AI Provider Authentication

### Vertex AI (Google Cloud)

```bash
gcloud auth application-default login
export CLOUD_ML_REGION="us-east5"
export ANTHROPIC_VERTEX_PROJECT_ID="my-gcp-project"
```

For service accounts (production), use a credentials JSON file:

```bash
export GOOGLE_APPLICATION_CREDENTIALS="/path/to/credentials.json"
export GOOGLE_CLOUD_PROJECT="my-gcp-project"
export CLOUD_ML_REGION="us-east5"
export ANTHROPIC_VERTEX_PROJECT_ID="my-gcp-project"
```

### Anthropic API (Direct)

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```
