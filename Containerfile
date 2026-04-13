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
    make \
    gcc \
    libc6-dev \
    && rm -rf /var/lib/apt/lists/*

# Install Go (matches kubernetes-nmstate requirements)
ARG GO_VERSION=1.25.0
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"

# Install gh CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
    | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
    > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# Install ai-helpers plugins (code-review, git, jira, etc.)
RUN git clone --depth 1 https://github.com/openshift-eng/ai-helpers /opt/ai-helpers

# Copy the agent binary
COPY --from=builder /ai-agent /usr/local/bin/ai-agent

# Use the existing non-root node user (UID 1000)
# This matches the host UID when using --userns=keep-id

# Configure gh as git credential helper
RUN gh auth setup-git 2>/dev/null || true

# Create a home directory writable by any UID (OpenShift assigns random UIDs in the root group)
RUN mkdir -p /home/agent && chmod 775 /home/agent && chown 1000:0 /home/agent
ENV HOME=/home/agent

# Work directory for clones and worktrees
RUN mkdir -p /work && chmod 777 /work
VOLUME /work

# Set git config system-wide so it works with any UID (OpenShift assigns random UIDs)
RUN git config --system --add safe.directory '*' \
    && git config --system credential.helper '!gh auth token' \
    && git config --system init.defaultBranch main
ENV GIT_TERMINAL_PROMPT=0
ENTRYPOINT ["ai-agent"]
CMD ["--clone-dir", "/work"]
