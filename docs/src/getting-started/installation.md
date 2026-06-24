# Installation

## Prerequisites

- Go 1.26+
- A coding agent CLI on `PATH`: either [OpenCode](https://opencode.ai) (recommended) or [Claude Code](https://docs.anthropic.com/en/docs/claude-code)
- Provider credentials configured (e.g. `gcloud auth application-default login` for Vertex AI, or `ANTHROPIC_API_KEY` for direct API)
- GitHub authentication: either `gh auth login` (recommended), a personal access token (PAT) with repo scope, or a GitHub App
- `gh` CLI installed and configured as a git credential helper (`gh auth setup-git`)
- [compound-engineering-plugin](https://github.com/EveryInc/compound-engineering-plugin) for CI investigation, review handling, and commit creation

## Install the Coding Agent Plugin

**OpenCode:**

```bash
bunx @every-env/compound-plugin install compound-engineering --to opencode
```

**Claude Code:**

```
/plugin install compound-engineering
```

## Binary Download

Download the latest release binary from GitHub:

```bash
gh release download --repo qinqon/oompa --pattern 'oompa-linux-amd64'
chmod +x oompa-linux-amd64
sudo mv oompa-linux-amd64 /usr/local/bin/oompa
```

## Build From Source

```bash
git clone https://github.com/qinqon/oompa.git
cd oompa
go build -o oompa ./cmd/oompa
```

## Container Image

A container image is published to GitHub Container Registry on every push to `main`:

```bash
podman pull ghcr.io/qinqon/oompa:latest
```

The image includes Go, `gh` CLI, Claude Code CLI, and git -- everything needed to run oompa in a container environment. See [Kubernetes deployment](../operations/kubernetes.md) for production use.
