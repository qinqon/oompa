# Stage 1: Build the Go binary
FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /ai-agent ./cmd/ai-agent

# Stage 2: Runtime
FROM node:22-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Install gh CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
    | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
    > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# Copy the agent binary
COPY --from=builder /ai-agent /usr/local/bin/ai-agent

# Use the existing non-root node user (UID 1000)
# This matches the host UID when using --userns=keep-id

# Configure gh as git credential helper
RUN gh auth setup-git 2>/dev/null || true

# Work directory for clones and worktrees
RUN mkdir -p /work && chown node:node /work
VOLUME /work

USER node
RUN git config --global --add safe.directory '*' \
    && git config --global credential.helper '!gh auth token' \
    && git config --global init.defaultBranch main
ENV GIT_TERMINAL_PROMPT=0
ENTRYPOINT ["ai-agent"]
CMD ["--clone-dir", "/work"]
