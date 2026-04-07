package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/qinqon/github-issue-resolver/pkg/agent"
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseConfig() agent.Config {
	cfg := agent.Config{}

	flag.StringVar(&cfg.Owner, "owner", envOrDefault("AI_AGENT_OWNER", "openperouter"), "GitHub repo owner")
	flag.StringVar(&cfg.Repo, "repo", envOrDefault("AI_AGENT_REPO", "openperouter"), "GitHub repo name")
	flag.StringVar(&cfg.Label, "label", envOrDefault("AI_AGENT_LABEL", "good-for-ai"), "Issue label to watch")
	flag.StringVar(&cfg.CloneDir, "clone-dir", envOrDefault("AI_AGENT_CLONE_DIR", os.ExpandEnv("$HOME/ai-agent-work")), "Clone/worktree directory")
	flag.StringVar(&cfg.StatePath, "state-path", envOrDefault("AI_AGENT_STATE_PATH", os.ExpandEnv("$HOME/.ai-agent-state.json")), "State file path")
	flag.DurationVar(&cfg.PollInterval, "poll-interval", parseDuration(envOrDefault("AI_AGENT_POLL_INTERVAL", "2m")), "Poll frequency")
	flag.StringVar(&cfg.VertexRegion, "vertex-region", os.Getenv("CLOUD_ML_REGION"), "GCP Vertex AI region")
	flag.StringVar(&cfg.VertexProject, "vertex-project", os.Getenv("ANTHROPIC_VERTEX_PROJECT_ID"), "GCP project ID for Vertex")
	flag.StringVar(&cfg.LogLevel, "log-level", envOrDefault("AI_AGENT_LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Log what would be done without executing")
	flag.StringVar(&cfg.SignedOffBy, "signed-off-by", os.Getenv("AI_AGENT_SIGNED_OFF_BY"), "Signed-off-by value for commits (e.g. \"Name <email>\")")

	flag.Parse()
	return cfg
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 2 * time.Minute
	}
	return d
}

func setupLogger(level string) *slog.Logger {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
}

func main() {
	cfg := parseConfig()
	logger := setupLogger(cfg.LogLevel)

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		logger.Error("GITHUB_TOKEN is required")
		os.Exit(1)
	}

	if cfg.VertexRegion == "" || cfg.VertexProject == "" {
		logger.Error("CLOUD_ML_REGION and ANTHROPIC_VERTEX_PROJECT_ID are required")
		os.Exit(1)
	}

	// Default signed-off-by to the authenticated GitHub user
	if cfg.SignedOffBy == "" {
		ghClient := agent.NewGoGitHubClient(token)
		if name, email, err := ghClient.GetAuthenticatedUser(context.Background()); err == nil {
			cfg.SignedOffBy = fmt.Sprintf("%s <%s>", name, email)
			logger.Info("using GitHub user for signed-off-by", "signed-off-by", cfg.SignedOffBy)
		} else {
			logger.Warn("could not fetch GitHub user for signed-off-by", "error", err)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	repoURL := fmt.Sprintf("https://github.com/%s/%s.git", cfg.Owner, cfg.Repo)
	runner := &agent.ExecRunner{}

	a := agent.NewAgent(
		agent.NewGoGitHubClient(token),
		runner,
		agent.NewGitWorktreeManager(runner, cfg.CloneDir, repoURL),
		agent.LoadState(cfg.StatePath),
		cfg,
		logger,
	)

	logger.Info("starting ai-agent",
		"owner", cfg.Owner,
		"repo", cfg.Repo,
		"label", cfg.Label,
		"poll-interval", cfg.PollInterval,
		"dry-run", cfg.DryRun,
	)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	// Run once immediately, then on tick
	runLoop(ctx, a, logger)

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutting down")
			return
		case <-ticker.C:
			runLoop(ctx, a, logger)
		}
	}
}

func runLoop(ctx context.Context, a *agent.Agent, logger *slog.Logger) {
	logger.Debug("starting poll cycle")
	a.CleanupDone(ctx)
	a.ProcessNewIssues(ctx)
	a.ProcessReviewComments(ctx)
	logger.Debug("poll cycle complete")
}
