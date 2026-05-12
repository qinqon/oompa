package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/qinqon/oompa/pkg/agent"
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseConfig() (cfg agent.Config, exitOnNewVersion, configPath string) {
	cfg = agent.Config{}

	flag.StringVar(&configPath, "config", envOrDefault("OOMPA_CONFIG", ""), "Path to YAML config file for multi-project mode")

	var repoFlag string
	flag.StringVar(&repoFlag, "repo", envOrDefault("OOMPA_REPO", ""), "GitHub repo as owner/repo (e.g. ovn-kubernetes/ovn-kubernetes)")
	flag.StringVar(&cfg.Label, "label", envOrDefault("OOMPA_LABEL", "good-for-ai"), "Issue label to watch")
	flag.StringVar(&cfg.CloneDir, "clone-dir", envOrDefault("OOMPA_CLONE_DIR", "/tmp/oompa-work"), "Base directory for clones (owner/repo appended automatically)")
	flag.DurationVar(&cfg.PollInterval, "poll-interval", parseDuration(envOrDefault("OOMPA_POLL_INTERVAL", "2m")), "Poll frequency")
	flag.StringVar(&cfg.Agent, "agent", envOrDefault("OOMPA_AGENT", "claudecode"), "Coding agent backend: claudecode or opencode")
	flag.StringVar(&cfg.AgentModel, "agent-model", envOrDefault("OOMPA_AGENT_MODEL", ""), "Model override for OpenCode (ignored for Claude Code)")
	flag.StringVar(&cfg.LogLevel, "log-level", envOrDefault("OOMPA_LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Log what would be done without executing")
	flag.BoolVar(&cfg.OneShot, "one-shot", false, "Run one cycle and exit")
	flag.BoolVar(&cfg.SkipFix, "skip-fix", envOrDefault("OOMPA_SKIP_FIX", "") == "true", "Investigate CI failures and comment but do not fix or push code")
	flag.StringVar(&cfg.SignedOffBy, "signed-off-by", os.Getenv("OOMPA_SIGNED_OFF_BY"), "Signed-off-by value for commits (e.g. \"Name <email>\")")

	var reviewers string
	flag.StringVar(&reviewers, "reviewers", os.Getenv("OOMPA_REVIEWERS"), "Comma-separated whitelist of users/bots whose reviews to address (empty = all)")

	var watchPRs string
	flag.StringVar(&watchPRs, "watch-prs", os.Getenv("OOMPA_WATCH_PRS"), "Comma-separated PR numbers to monitor directly (bypasses issue discovery)")

	var reactions string
	flag.StringVar(&reactions, "reactions", os.Getenv("OOMPA_REACTIONS"), "Comma-separated list of reactions to run: reviews, ci, conflicts, rebase (empty = all)")

	var skipComments string
	flag.StringVar(&skipComments, "skip-comment", os.Getenv("OOMPA_SKIP_COMMENTS"), "Comma-separated list of comment categories to suppress: ci-unrelated, ci-infrastructure, ci-related, conflict, rebase, flaky, issue-in-progress")

	var skipChecks string
	flag.StringVar(&skipChecks, "skip-checks", os.Getenv("OOMPA_SKIP_CHECKS"), "Comma-separated list of CI check names to ignore entirely")

	var maxReviewNoOps int
	maxReviewNoOpsDefault := 3
	if v := os.Getenv("OOMPA_MAX_REVIEW_NOOPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			maxReviewNoOpsDefault = n
		}
	}
	flag.IntVar(&maxReviewNoOps, "max-review-noops", maxReviewNoOpsDefault, "Consecutive no-op review cycles before pausing review processing (0 = unlimited)")

	var maxPRSessionCost float64
	maxPRSessionCostDefault := 0.0
	if v := os.Getenv("OOMPA_MAX_PR_SESSION_COST"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			maxPRSessionCostDefault = f
		}
	}
	flag.Float64Var(&maxPRSessionCost, "max-pr-session-cost", maxPRSessionCostDefault, "Max cumulative agent cost per PR per session before pausing (0 = unlimited)")

	var triageJobs string
	flag.StringVar(&triageJobs, "triage-jobs", os.Getenv("OOMPA_TRIAGE_JOBS"), "Comma-separated CI job URLs to monitor for periodic job triage")
	triageLookback := time.Duration(0)
	if raw := os.Getenv("OOMPA_TRIAGE_LOOKBACK"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid OOMPA_TRIAGE_LOOKBACK %q: %v\n", raw, err)
			os.Exit(1)
		}
		triageLookback = d
	}
	flag.DurationVar(&cfg.TriageLookback, "triage-lookback", triageLookback,
		"Time window to check for failed triage runs (e.g. 24h, 12h)")

	var logFile string
	flag.StringVar(&logFile, "log-file", os.Getenv("OOMPA_LOG_FILE"), "Log file path (default: stderr)")

	flag.BoolVar(&cfg.CreateFlakyIssues, "create-flaky-issues", envOrDefault("OOMPA_CREATE_FLAKY_ISSUES", "") == "true", "Create issues for unrelated CI failures (opt-in)")
	flag.StringVar(&cfg.FlakyLabel, "flaky-label", envOrDefault("OOMPA_FLAKY_LABEL", "flaky-test"), "Label to apply to flaky CI issues")
	flag.BoolVar(&cfg.OnlyAssigned, "only-assigned", envOrDefault("OOMPA_ONLY_ASSIGNED", "") == "true", "Only process issues assigned to the agent user")

	cfg.SlackWebhookURL = os.Getenv("OOMPA_SLACK_WEBHOOK")

	var forkFlag string
	flag.StringVar(&forkFlag, "fork", envOrDefault("OOMPA_FORK", ""), "Fork repo as owner/repo for pushing (e.g. qinqon/ovn-kubernetes)")

	flag.StringVar(&exitOnNewVersion, "exit-on-new-version", os.Getenv("OOMPA_EXIT_ON_NEW_VERSION"), "Exit when a new version is available (format: owner/repo, e.g. qinqon/oompa)")

	// Identity flags (optional, auto-detected from auth when not set)
	flag.StringVar(&cfg.GitHubUser, "github-user", os.Getenv("GITHUB_USER"), "GitHub username (e.g. myapp[bot])")
	flag.StringVar(&cfg.GitAuthorName, "git-author-name", os.Getenv("GIT_AUTHOR_NAME"), "Git commit author name")
	flag.StringVar(&cfg.GitAuthorEmail, "git-author-email", os.Getenv("GIT_AUTHOR_EMAIL"), "Git commit author email")

	// GitHub App auth flags
	var ghAppID int64
	var ghAppPrivateKeyPath string
	var ghAppInstallationID int64
	if v := os.Getenv("GITHUB_APP_ID"); v != "" {
		ghAppID, _ = strconv.ParseInt(v, 10, 64)
	}
	flag.Int64Var(&ghAppID, "github-app-id", ghAppID, "GitHub App ID")
	flag.StringVar(&ghAppPrivateKeyPath, "github-app-private-key", os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"), "Path to GitHub App private key PEM file (or set GITHUB_APP_PRIVATE_KEY env var with PEM content)")
	if v := os.Getenv("GITHUB_APP_INSTALLATION_ID"); v != "" {
		ghAppInstallationID, _ = strconv.ParseInt(v, 10, 64)
	}
	flag.Int64Var(&ghAppInstallationID, "github-app-installation-id", ghAppInstallationID, "GitHub App installation ID")

	flag.Parse()

	// Parse --reviewers (needed in both single-repo and config-file mode)
	if reviewers != "" {
		for r := range strings.SplitSeq(reviewers, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				cfg.Reviewers = append(cfg.Reviewers, r)
			}
		}
	}

	// Assign safety guard config early — needed in both single-repo and config-file modes.
	cfg.MaxReviewNoOps = maxReviewNoOps
	cfg.MaxPRSessionCost = maxPRSessionCost

	// In config-file mode, --repo is not required
	if configPath != "" {
		cfg.CloneDir = strings.TrimSuffix(cfg.CloneDir, "/")
		cfg.LogFile = logFile
		cfg.GitHubAppID = ghAppID
		cfg.GitHubAppInstallationID = ghAppInstallationID
		if key := os.Getenv("GITHUB_APP_PRIVATE_KEY"); key != "" {
			cfg.GitHubAppPrivateKey = []byte(key)
		} else if ghAppPrivateKeyPath != "" {
			keyBytes, err := os.ReadFile(ghAppPrivateKeyPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to read private key file %s: %v\n", ghAppPrivateKeyPath, err)
				os.Exit(1)
			}
			cfg.GitHubAppPrivateKey = keyBytes
		}
		return cfg, exitOnNewVersion, configPath
	}

	// Single-repo mode: --repo is required
	if repoFlag == "" {
		fmt.Fprintln(os.Stderr, "--repo is required (format: owner/repo) or use --config for multi-project mode")
		os.Exit(1)
	}
	if parts := strings.SplitN(repoFlag, "/", 2); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		fmt.Fprintf(os.Stderr, "invalid --repo %q: must be owner/repo\n", repoFlag)
		os.Exit(1)
	} else {
		cfg.Owner = parts[0]
		cfg.Repo = parts[1]
	}

	// Parse --fork owner/repo
	if forkFlag != "" {
		if parts := strings.SplitN(forkFlag, "/", 2); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			fmt.Fprintf(os.Stderr, "invalid --fork %q: must be owner/repo\n", forkFlag)
			os.Exit(1)
		} else {
			cfg.ForkOwner = parts[0]
			cfg.ForkRepo = parts[1]
		}
	}

	// Structure clone dir as <base>/<owner>/<repo> so multiple projects can share --clone-dir
	cfg.CloneDir = filepath.Join(cfg.CloneDir, cfg.Owner, cfg.Repo)

	cfg.GitHubAppID = ghAppID
	cfg.GitHubAppInstallationID = ghAppInstallationID

	// Load private key: prefer inline env var, fall back to file path
	if key := os.Getenv("GITHUB_APP_PRIVATE_KEY"); key != "" {
		cfg.GitHubAppPrivateKey = []byte(key)
	} else if ghAppPrivateKeyPath != "" {
		keyBytes, err := os.ReadFile(ghAppPrivateKeyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to read private key file %s: %v\n", ghAppPrivateKeyPath, err)
			os.Exit(1)
		}
		cfg.GitHubAppPrivateKey = keyBytes
	}

	cfg.LogFile = logFile

	if watchPRs != "" {
		for s := range strings.SplitSeq(watchPRs, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "invalid PR number in --watch-prs: %q\n", s)
				os.Exit(1)
			}
			cfg.WatchPRs = append(cfg.WatchPRs, n)
		}
	}

	if reactions != "" {
		validReactions := map[string]bool{"reviews": true, "ci": true, "conflicts": true, "rebase": true}
		for r := range strings.SplitSeq(reactions, ",") {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			if !validReactions[r] {
				fmt.Fprintf(os.Stderr, "invalid reaction type %q: valid values are reviews, ci, conflicts, rebase\n", r)
				os.Exit(1)
			}
			cfg.Reactions = append(cfg.Reactions, r)
		}
	}

	if skipComments != "" {
		validComments := map[string]bool{
			"ci-unrelated":      true,
			"ci-infrastructure": true,
			"ci-related":        true,
			"conflict":          true,
			"rebase":            true,
			"flaky":             true,
			"issue-in-progress": true,
		}
		for c := range strings.SplitSeq(skipComments, ",") {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			if !validComments[c] {
				fmt.Fprintf(os.Stderr, "invalid comment category %q: valid values are ci-unrelated, ci-infrastructure, ci-related, conflict, rebase, flaky, issue-in-progress\n", c)
				os.Exit(1)
			}
			cfg.SkipComments = append(cfg.SkipComments, c)
		}
	}

	if skipChecks != "" {
		for c := range strings.SplitSeq(skipChecks, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
				cfg.SkipChecks = append(cfg.SkipChecks, c)
			}
		}
	}

	if triageJobs != "" {
		for url := range strings.SplitSeq(triageJobs, ",") {
			url = strings.TrimSpace(url)
			if url != "" {
				cfg.TriageJobs = append(cfg.TriageJobs, url)
			}
		}
	}

	return cfg, exitOnNewVersion, "" //nolint:nakedret // named results used for gocritic
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 2 * time.Minute
	}
	return d
}

func setupLogger(level, logFile string) (logger *slog.Logger, cleanup func()) {
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

	w := os.Stderr
	cleanup = func() {}

	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open log file %s: %v\n", logFile, err)
			os.Exit(1)
		}
		w = f
		cleanup = func() { f.Close() }
	}

	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: logLevel})), cleanup
}

func getCommitSHA() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

func shouldExitForNewVersion(ctx context.Context, ghClient *agent.GoGitHubClient, owner, repo, currentSHA string, logger *slog.Logger) bool {
	latestSHA, err := ghClient.GetLatestReleaseSHA(ctx, owner, repo)
	if err != nil {
		logger.Warn("failed to check for new version", "error", err)
		return false
	}

	if latestSHA != currentSHA {
		logger.Info("new version detected, exiting for update", "current", currentSHA, "latest", latestSHA)
		return true
	}

	return false
}

// setupGCPCredentials writes inline GCP credentials to a temp file if provided.
func setupGCPCredentials(logger *slog.Logger) func() {
	gcpJSON := os.Getenv("GCP_CREDENTIALS_JSON")
	if gcpJSON == "" {
		return func() {}
	}
	f, err := os.CreateTemp("", "gcp-credentials-*.json")
	if err != nil {
		logger.Error("failed to create temp file for GCP credentials", "error", err)
		os.Exit(1) //nolint:gocritic // exitAfterDefer: intentional early exit in CLI startup
	}
	if _, err := f.Write([]byte(gcpJSON)); err != nil {
		f.Close()
		logger.Error("failed to write GCP credentials", "error", err)
		os.Exit(1)
	}
	f.Close()
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", f.Name())
	return func() { os.Remove(f.Name()) }
}

// setupAuth configures GitHub authentication and returns the client, token func, and updated config.
func setupAuth(cfg *agent.Config, logger *slog.Logger) (ghClient *agent.GoGitHubClient, tokenFunc func(context.Context) (string, error), useAppAuth bool) {
	useAppAuth = cfg.GitHubAppID != 0 && len(cfg.GitHubAppPrivateKey) > 0 && cfg.GitHubAppInstallationID != 0

	if useAppAuth {
		appAuth, err := agent.NewGitHubAppAuth(cfg.GitHubAppID, cfg.GitHubAppInstallationID, cfg.GitHubAppPrivateKey)
		if err != nil {
			logger.Error("failed to set up GitHub App auth", "error", err)
			os.Exit(1)
		}

		ghClient = appAuth.Client
		tokenFunc = appAuth.TokenFunc
		cfg.GitHubUser = appAuth.Login
		if cfg.ForkOwner != "" {
			cfg.GitHubHeadOwner = cfg.ForkOwner
		} else {
			cfg.GitHubHeadOwner = cfg.Owner
		}
		cfg.GitAuthorName = appAuth.Name
		cfg.GitAuthorEmail = appAuth.Email
		if cfg.SignedOffBy == "" {
			cfg.SignedOffBy = fmt.Sprintf("%s <%s>", appAuth.Name, appAuth.Email)
		}

		token, err := appAuth.TokenFunc(context.Background())
		if err != nil {
			logger.Error("failed to get initial installation token", "error", err)
			os.Exit(1)
		}
		cfg.GitHubToken = token
		os.Setenv("GH_TOKEN", token)

		logger.Info("authenticated as GitHub App", "login", appAuth.Login, "signed-off-by", cfg.SignedOffBy)
	} else {
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			const ghTokenTimeout = 10 * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), ghTokenTimeout)
			defer cancel()
			ghToken, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
			if err != nil {
				logger.Warn("failed to get token from gh auth", "error", err)
			} else {
				token = strings.TrimSpace(string(ghToken))
			}
		}
		if token == "" {
			logger.Error("GITHUB_TOKEN is required, or run 'gh auth login' (or configure GitHub App auth with --github-app-id, --github-app-private-key/GITHUB_APP_PRIVATE_KEY, --github-app-installation-id)")
			os.Exit(1) //nolint:gocritic // exitAfterDefer: intentional early exit in CLI startup
		}
		cfg.GitHubToken = token
		os.Setenv("GH_TOKEN", token)

		ghClient = agent.NewGoGitHubClient(token)
		if cfg.GitHubUser != "" && cfg.GitAuthorName != "" && cfg.GitAuthorEmail != "" {
			if cfg.ForkOwner != "" {
				cfg.GitHubHeadOwner = cfg.ForkOwner
			} else {
				cfg.GitHubHeadOwner = cfg.Owner
			}
			if cfg.SignedOffBy == "" {
				cfg.SignedOffBy = fmt.Sprintf("%s <%s>", cfg.GitAuthorName, cfg.GitAuthorEmail)
			}
			logger.Info("using provided identity", "login", cfg.GitHubUser, "signed-off-by", cfg.SignedOffBy)
		} else if login, name, email, err := ghClient.GetAuthenticatedUser(context.Background()); err == nil {
			cfg.GitHubUser = login
			cfg.GitHubHeadOwner = login
			cfg.GitAuthorName = name
			cfg.GitAuthorEmail = email
			if cfg.SignedOffBy == "" {
				cfg.SignedOffBy = fmt.Sprintf("%s <%s>", name, email)
			}
			logger.Info("authenticated as GitHub user", "login", login, "signed-off-by", cfg.SignedOffBy)
		} else {
			logger.Error("could not fetch GitHub user (set --github-user, --git-author-name, --git-author-email to skip auto-detection)", "error", err)
			os.Exit(1)
		}
	}

	return ghClient, tokenFunc, useAppAuth
}

// selectCodeAgent returns the appropriate CodeAgent implementation.
func selectCodeAgent(cfg agent.Config, logger *slog.Logger) agent.CodeAgent {
	switch cfg.Agent {
	case "claudecode":
		return &agent.ClaudeCodeAgent{}
	case "opencode":
		return &agent.OpenCodeAgent{Model: cfg.AgentModel}
	default:
		logger.Error("unsupported agent backend", "agent", cfg.Agent)
		os.Exit(1)
		return nil
	}
}

// buildAgentForConfig creates a fully wired Agent for a given config.
func buildAgentForConfig(cfg agent.Config, ghClient *agent.GoGitHubClient, tokenFunc func(context.Context) (string, error), useAppAuth bool, logger *slog.Logger) *agent.Agent {
	repoURL := fmt.Sprintf("https://github.com/%s/%s.git", cfg.Owner, cfg.Repo)
	forkURL := repoURL
	if cfg.ForkOwner != "" {
		forkRepoName := cfg.ForkRepo
		if forkRepoName == "" {
			forkRepoName = cfg.Repo
		}
		forkURL = fmt.Sprintf("https://github.com/%s/%s.git", cfg.ForkOwner, forkRepoName)
	} else if !useAppAuth {
		forkURL = fmt.Sprintf("https://github.com/%s/%s.git", cfg.GitHubUser, cfg.Repo)
	}
	runner := &agent.ExecRunner{}
	runner.Env = agent.BuildAgentEnv(cfg)

	wtm := agent.NewGitWorktreeManager(runner, cfg.CloneDir, repoURL, forkURL)
	if cfg.GitAuthorName != "" || cfg.GitAuthorEmail != "" {
		wtm.SetGitIdentity(cfg.GitAuthorName, cfg.GitAuthorEmail)
	}

	codeAgent := selectCodeAgent(cfg, logger)

	var agentGH agent.GitHubClient = ghClient
	if cfg.DryRun {
		agentGH = agent.NewDryRunGitHubClient(ghClient, logger)
	}

	state := agent.BuildStateFromGitHub(context.Background(), ghClient, cfg, cfg.CloneDir, logger)

	a := agent.NewAgent(agentGH, runner, wtm, state, cfg, logger, codeAgent)
	if tokenFunc != nil {
		a.SetTokenFunc(tokenFunc)
	}

	if cfg.SlackWebhookURL != "" {
		slack := agent.NewSlackReporter(cfg.SlackWebhookURL, cfg.Owner, cfg.Repo, logger)
		a.SetSlackReporter(slack)
		logger.Info("Slack reporting enabled")
	}

	return a
}

func main() {
	// Check for subcommands before flag parsing
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "status":
			runStatusCommand(os.Args[2:])
			return
		case "tui":
			runTUICommand(os.Args[2:])
			return
		}
	}

	cfg, exitOnNewVersion, configPath := parseConfig()

	logger, closeLog := setupLogger(cfg.LogLevel, cfg.LogFile)
	defer closeLog()

	cleanupGCP := setupGCPCredentials(logger)
	defer cleanupGCP()

	ghClient, tokenFunc, useAppAuth := setupAuth(&cfg, logger)

	commitSHA := getCommitSHA()
	cfg.Version = commitSHA

	// Start event server for observability
	socketPath := agent.DefaultSocketPath()
	eventServer := agent.NewSocketEventServer(socketPath, 1000, logger)
	eventCtx, eventCancel := context.WithCancel(context.Background())
	defer eventCancel()
	serverStarted := false
	if err := eventServer.Start(eventCtx); err != nil {
		logger.Warn("failed to start event server (observability disabled)", "error", err)
	} else {
		logger.Info("event server started", "socket", socketPath)
		serverStarted = true
		defer eventServer.Stop()
	}

	// Parse exit-on-new-version
	var exitOnNewVersionOwner, exitOnNewVersionRepo string
	if exitOnNewVersion != "" {
		if parts := strings.SplitN(exitOnNewVersion, "/", 2); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			exitOnNewVersionOwner = parts[0]
			exitOnNewVersionRepo = parts[1]
			if commitSHA == "" {
				logger.Warn("--exit-on-new-version set but no VCS revision found (dev build), version check disabled")
			} else {
				logger.Info("version check enabled", "repo", exitOnNewVersion, "current-sha", commitSHA)
			}
		} else {
			logger.Error("invalid --exit-on-new-version format (must be owner/repo)", "value", exitOnNewVersion)
			os.Exit(1)
		}
	}

	// Only pass event server to agents when it started successfully
	var emitter agent.EventEmitter
	if serverStarted {
		emitter = eventServer
	}

	if configPath != "" {
		// Multi-project mode
		runMultiProject(cfg, configPath, ghClient, tokenFunc, useAppAuth, exitOnNewVersionOwner, exitOnNewVersionRepo, commitSHA, logger, emitter)
	} else {
		// Single-repo mode (backward compatible)
		runSingleRepo(cfg, ghClient, tokenFunc, useAppAuth, exitOnNewVersionOwner, exitOnNewVersionRepo, commitSHA, logger, emitter)
	}
}

// runSingleRepo runs the original single-repo mode.
func runSingleRepo(cfg agent.Config, ghClient *agent.GoGitHubClient, tokenFunc func(context.Context) (string, error), useAppAuth bool, exitOwner, exitRepo, commitSHA string, logger *slog.Logger, emitter agent.EventEmitter) {
	// Validate agent backend
	if cfg.Agent != "claudecode" && cfg.Agent != "opencode" {
		fmt.Fprintf(os.Stderr, "invalid --agent %q: must be claudecode or opencode\n", cfg.Agent)
		os.Exit(1)
	}
	if cfg.AgentModel != "" && cfg.Agent != "opencode" {
		fmt.Fprintln(os.Stderr, "--agent-model can only be used with --agent opencode")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	logger.Info("starting oompa (single-repo mode)",
		"owner", cfg.Owner,
		"repo", cfg.Repo,
		"label", cfg.Label,
		"poll-interval", cfg.PollInterval,
		"dry-run", cfg.DryRun,
	)

	a := buildAgentForConfig(cfg, ghClient, tokenFunc, useAppAuth, logger)
	if emitter != nil {
		a.SetEmitter(emitter)
	}

	runLoop(ctx, a, logger)

	if cfg.OneShot {
		return
	}

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutting down")
			return
		case <-ticker.C:
			runLoop(ctx, a, logger)
			if exitOwner != "" && commitSHA != "" && shouldExitForNewVersion(ctx, ghClient, exitOwner, exitRepo, commitSHA, logger) {
				return
			}
		}
	}
}

// runMultiProject runs the multi-project orchestrator with per-role goroutines.
func runMultiProject(globalCfg agent.Config, configPath string, ghClient *agent.GoGitHubClient, tokenFunc func(context.Context) (string, error), useAppAuth bool, exitOwner, exitRepo, commitSHA string, logger *slog.Logger, emitter agent.EventEmitter) {
	fc, err := agent.LoadFileConfig(configPath)
	if err != nil {
		logger.Error("failed to load config file", "path", configPath, "error", err)
		os.Exit(1)
	}

	// Apply file-level overrides to global config
	if fc.LogLevel != "" {
		globalCfg.LogLevel = fc.LogLevel
		// Re-create logger with new level
		var closeLog func()
		logger, closeLog = setupLogger(globalCfg.LogLevel, globalCfg.LogFile)
		defer closeLog()
	}

	// Override exit-on-new-version from file config
	if fc.ExitOnNewVersion != "" && exitOwner == "" {
		if parts := strings.SplitN(fc.ExitOnNewVersion, "/", 2); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			exitOwner = parts[0]
			exitRepo = parts[1]
			if commitSHA == "" {
				logger.Warn("exit-on-new-version set but no VCS revision found (dev build), version check disabled")
			} else {
				logger.Info("version check enabled", "repo", fc.ExitOnNewVersion, "current-sha", commitSHA)
			}
		}
	}

	baseCloneDir := globalCfg.CloneDir
	entries := agent.BuildRoleEntries(fc, baseCloneDir, globalCfg)

	if len(entries) == 0 {
		logger.Error("no role entries generated from config file")
		os.Exit(1) //nolint:gocritic // exitAfterDefer: intentional early exit in CLI startup
	}

	logger.Info("starting oompa (multi-project mode)",
		"projects", len(fc.Projects),
		"goroutines", len(entries),
		"config", configPath,
	)

	// Two-signal graceful shutdown:
	// First signal: cancel context → goroutines finish current cycle → clean exit
	// Second signal: force exit immediately
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received signal, initiating graceful shutdown", "signal", sig)
		cancel()

		sig = <-sigCh
		logger.Error("received second signal, forcing exit", "signal", sig)
		os.Exit(1) //nolint:gocritic // exitAfterDefer: intentional force exit
	}()

	var wg sync.WaitGroup

	for _, entry := range entries {
		wg.Add(1)
		roleLogger := agent.NewRoleLogger(logger, entry)

		// Capture for goroutine
		entry := entry

		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					roleLogger.Error("panic recovered in role goroutine", "panic", r)
				}
			}()

			a := buildAgentForConfig(entry.Config, ghClient, tokenFunc, useAppAuth, roleLogger)
			if emitter != nil {
				a.SetEmitter(emitter)
			}

			roleLogger.Info("role goroutine started")

			if entry.Role == "triage" && entry.Schedule != "" {
				// Triage with scheduling: sleep until next run time
				runScheduledTriage(ctx, a, entry, roleLogger, ghClient, exitOwner, exitRepo, commitSHA, cancel)
			} else {
				// Standard poll loop
				runRoleLoop(ctx, a, entry, roleLogger, ghClient, exitOwner, exitRepo, commitSHA, cancel)
			}

			roleLogger.Info("role goroutine stopped")
		}()
	}

	wg.Wait()
	logger.Info("all role goroutines stopped, exiting")
}

// runRoleLoop runs the poll loop for a single role goroutine.
func runRoleLoop(ctx context.Context, a *agent.Agent, entry agent.RoleEntry, logger *slog.Logger, ghClient *agent.GoGitHubClient, exitOwner, exitRepo, commitSHA string, cancel context.CancelFunc) {
	runLoop(ctx, a, logger)

	if entry.Config.OneShot {
		return
	}

	ticker := time.NewTicker(entry.Config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runLoop(ctx, a, logger)
			if exitOwner != "" && commitSHA != "" && shouldExitForNewVersion(ctx, ghClient, exitOwner, exitRepo, commitSHA, logger) {
				cancel() // cancel the shared context → all goroutines stop
				return
			}
		}
	}
}

// runScheduledTriage runs triage on a schedule instead of a fixed poll interval.
func runScheduledTriage(ctx context.Context, a *agent.Agent, entry agent.RoleEntry, logger *slog.Logger, ghClient *agent.GoGitHubClient, exitOwner, exitRepo, commitSHA string, cancel context.CancelFunc) {
	for {
		next, err := agent.ParseSchedule(entry.Schedule, time.Now())
		if err != nil {
			logger.Error("failed to parse schedule, falling back to poll interval", "error", err)
			// Fall back to standard poll interval
			timer := time.NewTimer(entry.Config.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		} else {
			sleepDuration := time.Until(next)
			logger.Info("next triage run scheduled", "next", next.Format(time.RFC3339), "sleep", sleepDuration.Round(time.Second))
			timer := time.NewTimer(sleepDuration)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		// Run the triage
		runLoop(ctx, a, logger)

		if entry.Config.OneShot {
			return
		}

		// Check for new version after each triage run
		if exitOwner != "" && commitSHA != "" && shouldExitForNewVersion(ctx, ghClient, exitOwner, exitRepo, commitSHA, logger) {
			cancel() // cancel the shared context → all goroutines stop
			return
		}
	}
}

func runLoop(ctx context.Context, a *agent.Agent, logger *slog.Logger) {
	logger.Debug("starting poll cycle")
	a.EmitPollCycleStart()
	if err := a.RefreshToken(ctx); err != nil {
		logger.Error("failed to refresh GitHub token", "error", err)
	}
	a.CleanupDone(ctx)

	if a.HasWatchedPRs() {
		a.BootstrapWatchedPRs(ctx)
	} else {
		a.ProcessNewIssues(ctx)
	}

	if a.ShouldRunReaction("reviews") {
		a.ProcessReviewComments(ctx)
	}
	if a.ShouldRunReaction("conflicts") {
		a.ProcessConflicts(ctx)
	}
	if a.ShouldRunReaction("rebase") {
		a.ProcessRebase(ctx)
	}
	if a.ShouldRunReaction("ci") {
		a.ProcessCIFailures(ctx)
	}

	// ProcessTriageJobs is independent of other reactions and runs when triage jobs are configured
	a.ProcessTriageJobs(ctx)

	// Slack reporting: run report-only checks for reactions NOT in the active list,
	// then post consolidated findings to Slack.
	if a.SlackEnabled() {
		findings := a.RunReportOnlyChecks(ctx)
		a.ReportToSlack(ctx, findings)
	}

	a.EmitPollCycleEnd()
	logger.Debug("poll cycle complete")
}
