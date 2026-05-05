package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CIJobSource represents a CI job backend that can list recent runs and fetch logs.
type CIJobSource interface {
	// ListRecentRuns returns the most recent runs, sorted by timestamp descending.
	ListRecentRuns(ctx context.Context, limit int) ([]JobRun, error)

	// FetchLog returns the build log for a given run ID.
	FetchLog(ctx context.Context, runID string) (string, error)

	// JobName returns the human-readable job name.
	JobName() string
}

// ParseCIJobURL determines the CI backend from the URL and returns a CIJobSource.
func ParseCIJobURL(jobURL string, ghClient GitHubClient) (CIJobSource, error) {
	// GCS-backed CI: any URL containing /view/gs/{bucket}/{prefix}
	if strings.Contains(jobURL, "/view/gs/") {
		bucket, prefix := extractBucketPrefix(jobURL)
		if strings.HasPrefix(prefix, "pr-logs/directory/") {
			return newGCSDirectoryJobSource(bucket, prefix)
		}
		return parseGCSJobURL(jobURL)
	}

	// GCS-backed CI: direct GCS URL (https://storage.googleapis.com/...)
	if strings.Contains(jobURL, "storage.googleapis.com") {
		bucket, prefix := extractDirectGCSBucketPrefix(jobURL)
		if strings.HasPrefix(prefix, "pr-logs/directory/") {
			return newGCSDirectoryJobSource(bucket, prefix)
		}
		return parseDirectGCSURL(jobURL)
	}

	// GitHub Actions: github.com/{owner}/{repo}/actions/workflows/{workflow}
	if strings.Contains(jobURL, "github.com/") && strings.Contains(jobURL, "/actions/workflows/") {
		return parseGitHubActionsURL(jobURL, ghClient)
	}

	return nil, fmt.Errorf("unrecognized CI job URL format: %s", jobURL)
}

// extractBucketPrefix extracts bucket and prefix from a /view/gs/ URL.
func extractBucketPrefix(jobURL string) (bucket, prefix string) {
	_, after, ok := strings.Cut(jobURL, "/view/gs/")
	if !ok {
		return "", ""
	}
	parts := strings.SplitN(after, "/", 2)
	if len(parts) < 2 {
		return parts[0], ""
	}
	prefix = parts[1]
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return parts[0], prefix
}

// extractDirectGCSBucketPrefix extracts bucket and prefix from a storage.googleapis.com URL.
func extractDirectGCSBucketPrefix(jobURL string) (bucket, prefix string) {
	parsedURL, err := url.Parse(jobURL)
	if err != nil {
		return "", ""
	}
	path := strings.TrimPrefix(parsedURL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		return parts[0], ""
	}
	prefix = parts[1]
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return parts[0], prefix
}

// parseGCSJobURL parses a Prow-style GCS URL like:
// https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/logs/periodic-knmstate-e2e-handler-k8s-latest/
func parseGCSJobURL(jobURL string) (CIJobSource, error) {
	// Extract /view/gs/{bucket}/{prefix} from the URL
	_, after, ok := strings.Cut(jobURL, "/view/gs/")
	if !ok {
		return nil, fmt.Errorf("URL must contain /view/gs/: %s", jobURL)
	}

	// Everything after /view/gs/
	rest := after
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid GCS URL format (expected /view/gs/{bucket}/{prefix}): %s", jobURL)
	}

	bucket := parts[0]
	prefix := parts[1]

	// Ensure prefix ends with /
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	// Extract job name from prefix (last non-empty path component)
	jobName := strings.TrimSuffix(prefix, "/")
	if idx := strings.LastIndex(jobName, "/"); idx >= 0 {
		jobName = jobName[idx+1:]
	}

	return &GCSJobSource{
		bucket:  bucket,
		prefix:  prefix,
		jobName: jobName,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// parseDirectGCSURL parses a direct GCS URL like:
// https://storage.googleapis.com/kubevirt-prow/logs/periodic-knmstate-e2e-handler-k8s-latest/
func parseDirectGCSURL(jobURL string) (CIJobSource, error) {
	parsedURL, err := url.Parse(jobURL)
	if err != nil {
		return nil, fmt.Errorf("parsing GCS URL: %w", err)
	}

	// Path format: /{bucket}/{prefix}
	path := strings.TrimPrefix(parsedURL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid GCS URL path (expected /{bucket}/{prefix}): %s", jobURL)
	}

	bucket := parts[0]
	prefix := parts[1]

	// Ensure prefix ends with /
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	// Extract job name from prefix
	jobName := strings.TrimSuffix(prefix, "/")
	if idx := strings.LastIndex(jobName, "/"); idx >= 0 {
		jobName = jobName[idx+1:]
	}

	return &GCSJobSource{
		bucket:  bucket,
		prefix:  prefix,
		jobName: jobName,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// parseGitHubActionsURL parses a GitHub Actions workflow URL like:
// https://github.com/nmstate/kubernetes-nmstate/actions/workflows/nightly.yml
func parseGitHubActionsURL(jobURL string, ghClient GitHubClient) (CIJobSource, error) {
	// Regex: github.com/{owner}/{repo}/actions/workflows/{workflow}
	re := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/actions/workflows/([^/?#]+)`)
	matches := re.FindStringSubmatch(jobURL)
	if matches == nil {
		return nil, fmt.Errorf("invalid GitHub Actions URL format: %s", jobURL)
	}

	owner := matches[1]
	repo := matches[2]
	workflow := matches[3]

	return &GitHubActionsJobSource{
		owner:    owner,
		repo:     repo,
		workflow: workflow,
		jobName:  fmt.Sprintf("%s/%s/%s", owner, repo, workflow),
		gh:       ghClient,
	}, nil
}

// GCSJobSource implements CIJobSource for GCS-backed CI (Prow, Spyglass, etc.)
type GCSJobSource struct {
	bucket  string
	prefix  string
	jobName string
	client  *http.Client
}

func (g *GCSJobSource) JobName() string {
	return g.jobName
}

// gcsListResponse is the JSON response from the GCS list API.
type gcsListResponse struct {
	Prefixes []string `json:"prefixes"`
}

// gcsFinishedJSON is the structure of finished.json files.
type gcsFinishedJSON struct {
	Passed    *bool  `json:"passed"`
	Result    string `json:"result"`
	Timestamp int64  `json:"timestamp"`
}

// gcsStartedJSON is the structure of started.json files.
type gcsStartedJSON struct {
	Timestamp int64 `json:"timestamp"`
}

func (g *GCSJobSource) ListRecentRuns(ctx context.Context, limit int) ([]JobRun, error) {
	// List build directories using GCS JSON API
	listURL := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b/%s/o?prefix=%s&delimiter=/",
		url.PathEscape(g.bucket), url.PathEscape(g.prefix))

	req, err := http.NewRequestWithContext(ctx, "GET", listURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("creating list request: %w", err)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing GCS builds: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GCS list failed (status %d): %s", resp.StatusCode, string(body))
	}

	var listResp gcsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("decoding GCS list response: %w", err)
	}

	// Extract build IDs from prefixes
	var runs []JobRun
	for _, prefix := range listResp.Prefixes {
		// prefix format: {g.prefix}{buildID}/
		buildID := strings.TrimPrefix(prefix, g.prefix)
		buildID = strings.TrimSuffix(buildID, "/")

		if buildID == "" {
			continue
		}

		// Fetch finished.json to get status and timestamp
		status, timestamp, err := g.fetchFinishedJSON(ctx, buildID)
		if err != nil {
			// Skip builds without finished.json (still running or incomplete)
			continue
		}

		runs = append(runs, JobRun{
			ID:        buildID,
			JobName:   g.jobName,
			Status:    status,
			Timestamp: timestamp,
			LogURL:    fmt.Sprintf("https://storage.googleapis.com/%s/%s%s/build-log.txt", g.bucket, g.prefix, buildID),
		})
	}

	// Sort by timestamp descending
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].Timestamp.After(runs[j].Timestamp)
	})

	// Return up to limit
	if len(runs) > limit {
		runs = runs[:limit]
	}

	return runs, nil
}

func (g *GCSJobSource) fetchFinishedJSON(ctx context.Context, buildID string) (string, time.Time, error) {
	finishedURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s%s/finished.json", g.bucket, g.prefix, buildID)

	req, err := http.NewRequestWithContext(ctx, "GET", finishedURL, http.NoBody)
	if err != nil {
		return "", time.Time{}, err
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	var finished gcsFinishedJSON
	if err := json.NewDecoder(resp.Body).Decode(&finished); err != nil {
		return "", time.Time{}, err
	}

	// Determine status from finished.json
	status := "failure"
	if finished.Passed != nil && *finished.Passed {
		status = "success"
	} else if strings.ToUpper(finished.Result) == "SUCCESS" {
		status = "success"
	}

	// Use timestamp from finished.json if available
	timestamp := time.Time{}
	if finished.Timestamp > 0 {
		timestamp = time.Unix(finished.Timestamp, 0)
	} else {
		// Fallback: try to fetch started.json
		startedURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s%s/started.json", g.bucket, g.prefix, buildID)
		startedReq, err := http.NewRequestWithContext(ctx, "GET", startedURL, http.NoBody)
		if err == nil {
			startedResp, err := g.client.Do(startedReq)
			if err == nil {
				defer startedResp.Body.Close()
				if startedResp.StatusCode == http.StatusOK {
					var started gcsStartedJSON
					if json.NewDecoder(startedResp.Body).Decode(&started) == nil && started.Timestamp > 0 {
						timestamp = time.Unix(started.Timestamp, 0)
					}
				}
			}
		}
	}

	return status, timestamp, nil
}

func (g *GCSJobSource) FetchLog(ctx context.Context, runID string) (string, error) {
	logURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s%s/build-log.txt", g.bucket, g.prefix, runID)

	req, err := http.NewRequestWithContext(ctx, "GET", logURL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("creating log request: %w", err)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching log: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("log fetch failed (status %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading log: %w", err)
	}

	return string(body), nil
}

// gcsRef holds a resolved GCS bucket and prefix for a symlink-based build directory.
type gcsRef struct {
	bucket string
	prefix string // e.g. "pr-logs/pull/org_repo/PR/job/buildID/"
}

// GCSDirectoryJobSource implements CIJobSource for Prow presubmit jobs listed
// via the pr-logs/directory/ symlink index. Each entry is a .txt object whose
// x-goog-meta-link metadata points to the actual build directory.
type GCSDirectoryJobSource struct {
	bucket       string
	prefix       string // e.g. "pr-logs/directory/pull-job-name/"
	jobName      string
	client       *http.Client
	resolvedRuns map[string]gcsRef // buildID → resolved bucket+prefix
}

// newGCSDirectoryJobSource creates a GCSDirectoryJobSource from bucket and prefix.
func newGCSDirectoryJobSource(bucket, prefix string) (*GCSDirectoryJobSource, error) {
	if bucket == "" || prefix == "" {
		return nil, fmt.Errorf("invalid GCS directory URL: bucket=%q prefix=%q", bucket, prefix)
	}

	// Extract job name from prefix (last non-empty path component)
	jobName := strings.TrimSuffix(prefix, "/")
	if idx := strings.LastIndex(jobName, "/"); idx >= 0 {
		jobName = jobName[idx+1:]
	}

	return &GCSDirectoryJobSource{
		bucket:       bucket,
		prefix:       prefix,
		jobName:      jobName,
		client:       &http.Client{Timeout: 30 * time.Second},
		resolvedRuns: make(map[string]gcsRef),
	}, nil
}

func (g *GCSDirectoryJobSource) JobName() string {
	return g.jobName
}

// extractBuildIDNumeric extracts the numeric build ID from a directory item name.
// Given "pr-logs/directory/pull-job/12345.txt", it returns 12345.
// Returns -1 if the name doesn't end with ".txt" or doesn't contain a valid numeric build ID.
func extractBuildIDNumeric(name, prefix string) int64 {
	trimmed := strings.TrimPrefix(name, prefix)
	if !strings.HasSuffix(trimmed, ".txt") {
		return -1
	}
	trimmed = strings.TrimSuffix(trimmed, ".txt")
	id, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return -1
	}
	return id
}

// gcsDirectoryListResponse is the JSON response from the GCS list API
// when listing objects (not prefixes) in a directory index.
type gcsDirectoryListResponse struct {
	Items         []gcsDirectoryItem `json:"items"`
	NextPageToken string             `json:"nextPageToken"`
}

// gcsDirectoryItem represents an object in the GCS list response.
type gcsDirectoryItem struct {
	Name     string            `json:"name"`
	Metadata map[string]string `json:"metadata"`
}

func (g *GCSDirectoryJobSource) ListRecentRuns(ctx context.Context, limit int) ([]JobRun, error) {
	// Clear resolved runs for fresh listing
	g.resolvedRuns = make(map[string]gcsRef)

	var allItems []gcsDirectoryItem
	pageToken := ""

	// Phase 1: Paginate through all items in the directory index (metadata only, no per-item HTTP calls)
	for {
		listURL := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b/%s/o?prefix=%s",
			url.PathEscape(g.bucket), url.PathEscape(g.prefix))
		if pageToken != "" {
			listURL += "&pageToken=" + url.QueryEscape(pageToken)
		}

		req, err := http.NewRequestWithContext(ctx, "GET", listURL, http.NoBody)
		if err != nil {
			return nil, fmt.Errorf("creating list request: %w", err)
		}

		resp, err := g.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("listing GCS directory: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("GCS directory list failed (status %d): %s", resp.StatusCode, string(body))
		}

		var listResp gcsDirectoryListResponse
		if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decoding GCS directory list response: %w", err)
		}
		resp.Body.Close()

		allItems = append(allItems, listResp.Items...)

		if listResp.NextPageToken == "" {
			break
		}
		pageToken = listResp.NextPageToken
	}

	// Phase 2: Filter out non-build items to prevent them from consuming
	// the fetch limit. Items without a valid numeric build ID (e.g. .log
	// or .bak files) are excluded. Cache parsed IDs alongside items so
	// the sort avoids redundant string parsing.
	type itemWithID struct {
		item gcsDirectoryItem
		id   int64
	}
	var filtered []itemWithID
	for _, item := range allItems {
		if id := extractBuildIDNumeric(item.Name, g.prefix); id >= 0 {
			filtered = append(filtered, itemWithID{item: item, id: id})
		}
	}

	// Sort by build ID descending (numeric, monotonically increasing)
	// so we process the most recent builds first.
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].id > filtered[j].id })

	// Phase 3: Only fetch finished.json for the most recent items.
	// Use a safety multiplier to account for items that may be skipped
	// (missing metadata, still running, etc.)
	fetchLimit := min(max(limit*3, 50), len(filtered))

	// Phase 4: Process only the most recent items
	var runs []JobRun
	for _, fi := range filtered[:fetchLimit] {
		item := fi.item

		// Extract build ID from object name: strip prefix and .txt suffix
		name := strings.TrimPrefix(item.Name, g.prefix)
		buildID := strings.TrimSuffix(name, ".txt")

		// Resolve target build path from metadata.
		// GCS JSON API exposes custom metadata with bare keys (e.g. "link"),
		// while the x-goog-meta- prefix is used in XML API / HTTP headers.
		link := item.Metadata["link"]
		if link == "" {
			link = item.Metadata["x-goog-meta-link"]
		}
		if link == "" {
			continue
		}

		ref, err := parseGCSURI(link)
		if err != nil {
			continue
		}

		g.resolvedRuns[buildID] = ref

		// Fetch finished.json from the resolved path
		status, timestamp, err := g.fetchFinishedJSON(ctx, ref)
		if err != nil {
			// Skip builds without finished.json (still running or incomplete)
			continue
		}

		runs = append(runs, JobRun{
			ID:        buildID,
			JobName:   g.jobName,
			Status:    status,
			Timestamp: timestamp,
			LogURL:    fmt.Sprintf("https://storage.googleapis.com/%s/%sbuild-log.txt", ref.bucket, ref.prefix),
		})
	}

	// Sort by timestamp descending
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].Timestamp.After(runs[j].Timestamp)
	})

	// Return up to limit
	if len(runs) > limit {
		runs = runs[:limit]
	}

	return runs, nil
}

// parseGCSURI parses a gs:// URI into a gcsRef.
func parseGCSURI(uri string) (gcsRef, error) {
	if !strings.HasPrefix(uri, "gs://") {
		return gcsRef{}, fmt.Errorf("not a gs:// URI: %s", uri)
	}
	rest := strings.TrimPrefix(uri, "gs://")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		return gcsRef{}, fmt.Errorf("invalid gs:// URI (no path): %s", uri)
	}
	prefix := parts[1]
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return gcsRef{
		bucket: parts[0],
		prefix: prefix,
	}, nil
}

func (g *GCSDirectoryJobSource) fetchFinishedJSON(ctx context.Context, ref gcsRef) (string, time.Time, error) {
	finishedURL := fmt.Sprintf("https://storage.googleapis.com/%s/%sfinished.json", ref.bucket, ref.prefix)

	req, err := http.NewRequestWithContext(ctx, "GET", finishedURL, http.NoBody)
	if err != nil {
		return "", time.Time{}, err
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	var finished gcsFinishedJSON
	if err := json.NewDecoder(resp.Body).Decode(&finished); err != nil {
		return "", time.Time{}, err
	}

	// Determine status from finished.json
	status := "failure"
	if finished.Passed != nil && *finished.Passed {
		status = "success"
	} else if strings.ToUpper(finished.Result) == "SUCCESS" {
		status = "success"
	}

	// Use timestamp from finished.json if available
	timestamp := time.Time{}
	if finished.Timestamp > 0 {
		timestamp = time.Unix(finished.Timestamp, 0)
	} else {
		// Fallback: try to fetch started.json
		startedURL := fmt.Sprintf("https://storage.googleapis.com/%s/%sstarted.json", ref.bucket, ref.prefix)
		startedReq, err := http.NewRequestWithContext(ctx, "GET", startedURL, http.NoBody)
		if err == nil {
			startedResp, err := g.client.Do(startedReq)
			if err == nil {
				defer startedResp.Body.Close()
				if startedResp.StatusCode == http.StatusOK {
					var started gcsStartedJSON
					if json.NewDecoder(startedResp.Body).Decode(&started) == nil && started.Timestamp > 0 {
						timestamp = time.Unix(started.Timestamp, 0)
					}
				}
			}
		}
	}

	return status, timestamp, nil
}

func (g *GCSDirectoryJobSource) FetchLog(ctx context.Context, runID string) (string, error) {
	ref, ok := g.resolvedRuns[runID]
	if !ok {
		return "", fmt.Errorf("unknown run ID %q (not resolved during ListRecentRuns)", runID)
	}

	logURL := fmt.Sprintf("https://storage.googleapis.com/%s/%sbuild-log.txt", ref.bucket, ref.prefix)

	req, err := http.NewRequestWithContext(ctx, "GET", logURL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("creating log request: %w", err)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching log: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("log fetch failed (status %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading log: %w", err)
	}

	return string(body), nil
}

// GitHubActionsJobSource implements CIJobSource for GitHub Actions workflows.
type GitHubActionsJobSource struct {
	owner    string
	repo     string
	workflow string
	jobName  string
	gh       GitHubClient
}

func (g *GitHubActionsJobSource) JobName() string {
	return g.jobName
}

func (g *GitHubActionsJobSource) ListRecentRuns(ctx context.Context, limit int) ([]JobRun, error) {
	// List recent workflow runs filtered by failure status
	runs, err := g.gh.ListWorkflowRuns(ctx, g.owner, g.repo, g.workflow, "failure", limit)
	if err != nil {
		return nil, fmt.Errorf("listing workflow runs: %w", err)
	}

	var jobRuns []JobRun
	for _, run := range runs {
		status := "pending"
		if run.Status == "completed" {
			if run.Conclusion == "success" {
				status = "success"
			} else {
				status = "failure"
			}
		}

		jobRuns = append(jobRuns, JobRun{
			ID:        fmt.Sprintf("%d", run.ID),
			JobName:   g.jobName,
			Status:    status,
			Timestamp: run.CreatedAt,
			LogURL:    run.HTMLURL,
		})
	}

	return jobRuns, nil
}

func (g *GitHubActionsJobSource) FetchLog(ctx context.Context, runID string) (string, error) {
	// Parse runID as int64
	var workflowRunID int64
	if _, err := fmt.Sscanf(runID, "%d", &workflowRunID); err != nil {
		return "", fmt.Errorf("invalid run ID: %s", runID)
	}

	// List all jobs for this workflow run
	jobs, err := g.gh.ListWorkflowJobs(ctx, g.owner, g.repo, workflowRunID)
	if err != nil {
		return "", fmt.Errorf("listing workflow jobs: %w", err)
	}

	// Fetch and concatenate logs from all jobs
	var allLogs strings.Builder
	for i, job := range jobs {
		if i > 0 {
			allLogs.WriteString("\n\n==========\n\n")
		}
		fmt.Fprintf(&allLogs, "Job: %s (ID: %d)\n", job.Name, job.ID)
		allLogs.WriteString("==========\n\n")

		log, err := g.gh.GetWorkflowJobLogs(ctx, g.owner, g.repo, job.ID)
		if err != nil {
			fmt.Fprintf(&allLogs, "Error fetching logs: %v\n", err)
			continue
		}
		allLogs.WriteString(log)
	}

	return allLogs.String(), nil
}
