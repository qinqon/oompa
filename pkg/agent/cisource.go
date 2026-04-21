package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CIJobSource provides access to CI job runs and logs.
type CIJobSource interface {
	// ListRecentRuns returns the most recent runs for this job.
	ListRecentRuns(ctx context.Context, limit int) ([]JobRun, error)

	// FetchLog retrieves the build log for the given run ID.
	FetchLog(ctx context.Context, runID string) (string, error)
}

// ParseJobURL parses a CI job URL and returns the appropriate CIJobSource.
// Returns an error if the URL format is not recognized.
func ParseJobURL(jobURL string, ghClient GitHubClient) (CIJobSource, error) {
	// GCS-backed CI: contains /view/gs/{bucket}/{prefix}
	if strings.Contains(jobURL, "/view/gs/") {
		return parseGCSJobURL(jobURL)
	}

	// GitHub Actions: contains github.com/.../actions/workflows/
	if strings.Contains(jobURL, "github.com") && strings.Contains(jobURL, "/actions/workflows/") {
		return parseGitHubActionsURL(jobURL, ghClient)
	}

	return nil, fmt.Errorf("unsupported CI job URL format: %s", jobURL)
}

// parseGCSJobURL extracts bucket and prefix from a GCS-backed CI URL.
// Example: https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/logs/periodic-knmstate-e2e-handler-k8s-latest/
//   -> bucket: kubevirt-prow
//   -> prefix: logs/periodic-knmstate-e2e-handler-k8s-latest/
func parseGCSJobURL(jobURL string) (*GCSJobSource, error) {
	parsed, err := url.Parse(jobURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Extract the path after /view/gs/
	path := parsed.Path
	if !strings.Contains(path, "/view/gs/") {
		return nil, fmt.Errorf("URL does not contain /view/gs/: %s", jobURL)
	}

	// Split on /view/gs/ and take the second part
	parts := strings.SplitN(path, "/view/gs/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid GCS URL format: %s", jobURL)
	}

	// The rest is bucket/prefix...
	// Split on first / to get bucket
	pathParts := strings.SplitN(parts[1], "/", 2)
	if len(pathParts) != 2 {
		return nil, fmt.Errorf("invalid GCS path format: %s", parts[1])
	}

	bucket := pathParts[0]
	prefix := pathParts[1]

	// Ensure prefix ends with /
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	return &GCSJobSource{
		Bucket: bucket,
		Prefix: prefix,
		JobURL: jobURL,
	}, nil
}

// parseGitHubActionsURL extracts owner, repo, and workflow from a GitHub Actions URL.
// Example: https://github.com/nmstate/kubernetes-nmstate/actions/workflows/nightly.yml
//   -> owner: nmstate, repo: kubernetes-nmstate, workflow: nightly.yml
func parseGitHubActionsURL(jobURL string, ghClient GitHubClient) (*GitHubActionsJobSource, error) {
	parsed, err := url.Parse(jobURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Path should be: /owner/repo/actions/workflows/workflow_file
	path := strings.TrimPrefix(parsed.Path, "/")
	parts := strings.Split(path, "/")

	if len(parts) < 5 || parts[2] != "actions" || parts[3] != "workflows" {
		return nil, fmt.Errorf("invalid GitHub Actions URL format: %s", jobURL)
	}

	owner := parts[0]
	repo := parts[1]
	workflow := parts[4]

	return &GitHubActionsJobSource{
		Owner:    owner,
		Repo:     repo,
		Workflow: workflow,
		Client:   ghClient,
		JobURL:   jobURL,
	}, nil
}

// GCSJobSource fetches CI job data from GCS-backed systems (Prow, etc.).
type GCSJobSource struct {
	Bucket string
	Prefix string
	JobURL string
}

// gcsListResponse represents the JSON response from GCS list API.
type gcsListResponse struct {
	Prefixes []string `json:"prefixes"`
}

// gcsFinishedJSON represents the finished.json file format.
type gcsFinishedJSON struct {
	Passed    bool   `json:"passed"`
	Result    string `json:"result"`    // "SUCCESS", "FAILURE", etc.
	Timestamp int64  `json:"timestamp"` // Unix timestamp in milliseconds
}

// gcsStartedJSON represents the started.json file format.
type gcsStartedJSON struct {
	Timestamp int64 `json:"timestamp"` // Unix timestamp in seconds
}

// ListRecentRuns lists recent builds from GCS.
func (g *GCSJobSource) ListRecentRuns(ctx context.Context, limit int) ([]JobRun, error) {
	// List build IDs using GCS JSON API
	listURL := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b/%s/o?prefix=%s&delimiter=/&maxResults=%d",
		url.PathEscape(g.Bucket),
		url.PathEscape(g.Prefix),
		limit+10) // Fetch extra to handle filtering

	req, err := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching build list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GCS API returned status %d: %s", resp.StatusCode, string(body))
	}

	var listResp gcsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("decoding GCS response: %w", err)
	}

	var runs []JobRun
	for _, prefix := range listResp.Prefixes {
		// Extract build ID from prefix (last path component before trailing /)
		buildID := strings.TrimSuffix(strings.TrimPrefix(prefix, g.Prefix), "/")
		if buildID == "" {
			continue
		}

		// Fetch finished.json to check status
		finishedURL := fmt.Sprintf("https://storage.googleapis.com/%s/%sfinished.json",
			g.Bucket, prefix)

		finished, err := g.fetchFinishedJSON(ctx, finishedURL)
		if err != nil {
			// Build might still be running, skip
			continue
		}

		status := "success"
		if !finished.Passed {
			status = "failure"
		}

		timestamp := time.Unix(finished.Timestamp/1000, 0)
		if finished.Timestamp == 0 {
			// Fallback: try started.json
			startedURL := fmt.Sprintf("https://storage.googleapis.com/%s/%sstarted.json",
				g.Bucket, prefix)
			if started, err := g.fetchStartedJSON(ctx, startedURL); err == nil && started.Timestamp > 0 {
				timestamp = time.Unix(started.Timestamp, 0)
			}
		}

		logURL := fmt.Sprintf("%s%s", strings.TrimSuffix(g.JobURL, "/"), buildID)

		runs = append(runs, JobRun{
			ID:        buildID,
			JobName:   g.Prefix,
			Status:    status,
			Timestamp: timestamp,
			LogURL:    logURL,
		})

		if len(runs) >= limit {
			break
		}
	}

	return runs, nil
}

// FetchLog retrieves the build log from GCS.
func (g *GCSJobSource) FetchLog(ctx context.Context, runID string) (string, error) {
	logURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s%s/build-log.txt",
		g.Bucket, g.Prefix, runID)

	req, err := http.NewRequestWithContext(ctx, "GET", logURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching log: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("log not found (status %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading log: %w", err)
	}

	return string(body), nil
}

// fetchFinishedJSON fetches and parses finished.json from GCS.
func (g *GCSJobSource) fetchFinishedJSON(ctx context.Context, url string) (*gcsFinishedJSON, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var finished gcsFinishedJSON
	if err := json.NewDecoder(resp.Body).Decode(&finished); err != nil {
		return nil, err
	}

	return &finished, nil
}

// fetchStartedJSON fetches and parses started.json from GCS.
func (g *GCSJobSource) fetchStartedJSON(ctx context.Context, url string) (*gcsStartedJSON, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var started gcsStartedJSON
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		return nil, err
	}

	return &started, nil
}

// GitHubActionsJobSource fetches CI job data from GitHub Actions.
type GitHubActionsJobSource struct {
	Owner    string
	Repo     string
	Workflow string
	Client   GitHubClient
	JobURL   string
}

// ListRecentRuns lists recent workflow runs from GitHub Actions.
func (g *GitHubActionsJobSource) ListRecentRuns(ctx context.Context, limit int) ([]JobRun, error) {
	// Use the GitHub client to list workflow runs
	// We need to add a method to GitHubClient for this
	runs, err := g.Client.ListWorkflowRuns(ctx, g.Owner, g.Repo, g.Workflow, "schedule", limit)
	if err != nil {
		return nil, fmt.Errorf("fetching workflow runs: %w", err)
	}

	var jobRuns []JobRun
	for _, run := range runs {
		status := "success"
		if run.Conclusion == "failure" {
			status = "failure"
		} else if run.Status != "completed" {
			status = "pending"
		}

		jobRuns = append(jobRuns, JobRun{
			ID:        fmt.Sprintf("%d", run.ID),
			JobName:   g.Workflow,
			Status:    status,
			Timestamp: run.CreatedAt,
			LogURL:    run.HTMLURL,
		})
	}

	return jobRuns, nil
}

// FetchLog retrieves logs for a GitHub Actions workflow run.
func (g *GitHubActionsJobSource) FetchLog(ctx context.Context, runID string) (string, error) {
	// Parse runID as int64
	var runIDInt int64
	if _, err := fmt.Sscanf(runID, "%d", &runIDInt); err != nil {
		return "", fmt.Errorf("invalid run ID: %s", runID)
	}

	// Get the workflow run jobs
	jobs, err := g.Client.ListWorkflowJobs(ctx, g.Owner, g.Repo, runIDInt)
	if err != nil {
		return "", fmt.Errorf("fetching workflow jobs: %w", err)
	}

	// Fetch logs for each job
	var logBuilder strings.Builder
	for _, job := range jobs {
		jobLog, err := g.Client.GetWorkflowJobLogs(ctx, g.Owner, g.Repo, job.ID)
		if err != nil {
			logBuilder.WriteString(fmt.Sprintf("\n--- Failed to fetch logs for job %s: %v ---\n", job.Name, err))
			continue
		}
		logBuilder.WriteString(fmt.Sprintf("\n--- Job: %s ---\n", job.Name))
		logBuilder.WriteString(jobLog)
		logBuilder.WriteString("\n")
	}

	return logBuilder.String(), nil
}
