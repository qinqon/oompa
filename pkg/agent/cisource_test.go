package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseGCSJobURL(t *testing.T) {
	testCases := []struct {
		name       string
		url        string
		wantBucket string
		wantPrefix string
		wantErr    bool
	}{
		{
			name:       "valid GCS URL",
			url:        "https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/logs/periodic-knmstate-e2e-handler-k8s-latest/",
			wantBucket: "kubevirt-prow",
			wantPrefix: "logs/periodic-knmstate-e2e-handler-k8s-latest/",
			wantErr:    false,
		},
		{
			name:       "GCS URL without trailing slash",
			url:        "https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/logs/periodic-knmstate-e2e-handler-k8s-latest",
			wantBucket: "kubevirt-prow",
			wantPrefix: "logs/periodic-knmstate-e2e-handler-k8s-latest/",
			wantErr:    false,
		},
		{
			name:    "invalid GCS URL - no bucket",
			url:     "https://prow.ci.kubevirt.io/view/gs/",
			wantErr: true,
		},
		{
			name:    "invalid GCS URL - missing /view/gs/",
			url:     "https://prow.ci.kubevirt.io/kubevirt-prow/logs/job/",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			source, err := parseGCSJobURL(tc.url)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Verify the source was created successfully - use type assertion
			gcsSource, ok := source.(*GCSJobSource)
			if !ok {
				t.Fatalf("expected *GCSJobSource, got %T", source)
			}
			if gcsSource.bucket != tc.wantBucket {
				t.Errorf("bucket = %q, want %q", gcsSource.bucket, tc.wantBucket)
			}
			if gcsSource.prefix != tc.wantPrefix {
				t.Errorf("prefix = %q, want %q", gcsSource.prefix, tc.wantPrefix)
			}
		})
	}
}

func TestParseGitHubActionsURL(t *testing.T) {
	mockGH := &mockGitHubClient{}

	testCases := []struct {
		name         string
		url          string
		wantOwner    string
		wantRepo     string
		wantWorkflow string
		wantErr      bool
	}{
		{
			name:         "valid GitHub Actions URL",
			url:          "https://github.com/nmstate/kubernetes-nmstate/actions/workflows/nightly.yml",
			wantOwner:    "nmstate",
			wantRepo:     "kubernetes-nmstate",
			wantWorkflow: "nightly.yml",
			wantErr:      false,
		},
		{
			name:    "invalid GitHub Actions URL - missing workflow",
			url:     "https://github.com/nmstate/kubernetes-nmstate/actions/workflows/",
			wantErr: true,
		},
		{
			name:    "invalid GitHub Actions URL - not actions path",
			url:     "https://github.com/nmstate/kubernetes-nmstate/pulls",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			source, err := parseGitHubActionsURL(tc.url, mockGH)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Verify the source was created successfully - use type assertion
			ghSource, ok := source.(*GitHubActionsJobSource)
			if !ok {
				t.Fatalf("expected *GitHubActionsJobSource, got %T", source)
			}
			if ghSource.owner != tc.wantOwner {
				t.Errorf("owner = %q, want %q", ghSource.owner, tc.wantOwner)
			}
			if ghSource.repo != tc.wantRepo {
				t.Errorf("repo = %q, want %q", ghSource.repo, tc.wantRepo)
			}
			if ghSource.workflow != tc.wantWorkflow {
				t.Errorf("workflow = %q, want %q", ghSource.workflow, tc.wantWorkflow)
			}
		})
	}
}

func TestParseGCSDirectoryURL(t *testing.T) {
	testCases := []struct {
		name        string
		url         string
		wantBucket  string
		wantPrefix  string
		wantJobName string
		wantErr     bool
	}{
		{
			name:        "valid pr-logs/directory URL via /view/gs/",
			url:         "https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/pr-logs/directory/pull-cluster-network-addons-operator-unit-test/",
			wantBucket:  "kubevirt-prow",
			wantPrefix:  "pr-logs/directory/pull-cluster-network-addons-operator-unit-test/",
			wantJobName: "pull-cluster-network-addons-operator-unit-test",
			wantErr:     false,
		},
		{
			name:        "valid pr-logs/directory URL without trailing slash",
			url:         "https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/pr-logs/directory/pull-job-name",
			wantBucket:  "kubevirt-prow",
			wantPrefix:  "pr-logs/directory/pull-job-name/",
			wantJobName: "pull-job-name",
			wantErr:     false,
		},
		{
			name:        "valid pr-logs/directory URL via storage.googleapis.com",
			url:         "https://storage.googleapis.com/kubevirt-prow/pr-logs/directory/pull-job-name/",
			wantBucket:  "kubevirt-prow",
			wantPrefix:  "pr-logs/directory/pull-job-name/",
			wantJobName: "pull-job-name",
			wantErr:     false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			source, err := ParseCIJobURL(tc.url, nil)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			dirSource, ok := source.(*GCSDirectoryJobSource)
			if !ok {
				t.Fatalf("expected *GCSDirectoryJobSource, got %T", source)
			}
			if dirSource.bucket != tc.wantBucket {
				t.Errorf("bucket = %q, want %q", dirSource.bucket, tc.wantBucket)
			}
			if dirSource.prefix != tc.wantPrefix {
				t.Errorf("prefix = %q, want %q", dirSource.prefix, tc.wantPrefix)
			}
			if dirSource.jobName != tc.wantJobName {
				t.Errorf("jobName = %q, want %q", dirSource.jobName, tc.wantJobName)
			}
		})
	}
}

func TestParseGCSDirectoryURL_DetectedByParseCIJobURL(t *testing.T) {
	testCases := []struct {
		name string
		url  string
	}{
		{
			name: "pr-logs/directory via /view/gs/ routes to GCSDirectoryJobSource",
			url:  "https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/pr-logs/directory/pull-job-name/",
		},
		{
			name: "pr-logs/directory via storage.googleapis.com routes to GCSDirectoryJobSource",
			url:  "https://storage.googleapis.com/kubevirt-prow/pr-logs/directory/pull-job-name/",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			source, err := ParseCIJobURL(tc.url, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, ok := source.(*GCSDirectoryJobSource); !ok {
				t.Errorf("expected *GCSDirectoryJobSource, got %T", source)
			}
		})
	}

	// Non-directory GCS URLs should still route to GCSJobSource
	nonDirURL := "https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/logs/periodic-job/"
	source, err := ParseCIJobURL(nonDirURL, nil)
	if err != nil {
		t.Fatalf("unexpected error for non-directory URL: %v", err)
	}
	if _, ok := source.(*GCSJobSource); !ok {
		t.Errorf("expected *GCSJobSource for non-directory URL, got %T", source)
	}
}

func TestGCSDirectoryJobSource_ListRecentRuns(t *testing.T) {
	passed := true
	finishedJSON := gcsFinishedJSON{
		Passed:    &passed,
		Result:    "SUCCESS",
		Timestamp: 1700000000,
	}
	finishedBytes, _ := json.Marshal(finishedJSON)

	failedFinishedJSON := gcsFinishedJSON{
		Passed:    func() *bool { b := false; return &b }(),
		Result:    "FAILURE",
		Timestamp: 1699999000,
	}
	failedFinishedBytes, _ := json.Marshal(failedFinishedJSON)

	// Mock server that serves GCS API responses
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		// GCS list API
		case "/storage/v1/b/test-bucket/o":
			resp := gcsDirectoryListResponse{
				Items: []gcsDirectoryItem{
					{
						Name: "pr-logs/directory/pull-job/111.txt",
						Metadata: map[string]string{
							"link": "gs://test-bucket/pr-logs/pull/org_repo/10/pull-job/111",
						},
					},
					{
						Name: "pr-logs/directory/pull-job/222.txt",
						Metadata: map[string]string{
							"link": "gs://test-bucket/pr-logs/pull/org_repo/20/pull-job/222",
						},
					},
					{
						Name: "pr-logs/directory/pull-job/333.txt",
						// Missing metadata — should be skipped
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		// finished.json for build 111 (success, newer)
		case "/test-bucket/pr-logs/pull/org_repo/10/pull-job/111/finished.json":
			_, _ = w.Write(finishedBytes)

		// finished.json for build 222 (failure, older)
		case "/test-bucket/pr-logs/pull/org_repo/20/pull-job/222/finished.json":
			_, _ = w.Write(failedFinishedBytes)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := &GCSDirectoryJobSource{
		bucket:       "test-bucket",
		prefix:       "pr-logs/directory/pull-job/",
		jobName:      "pull-job",
		client:       server.Client(),
		resolvedRuns: make(map[string]gcsRef),
	}

	// Override the GCS URLs to point to our test server
	// We do this by replacing the source's client with a transport that rewrites URLs
	source.client = &http.Client{
		Transport: &rewriteTransport{base: server.URL},
	}

	runs, err := source.ListRecentRuns(context.Background(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 2 runs (333 skipped due to missing metadata)
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}

	// Sorted by timestamp descending: build 111 (1700000000) before build 222 (1699999000)
	if runs[0].ID != "111" {
		t.Errorf("first run ID = %q, want %q", runs[0].ID, "111")
	}
	if runs[0].Status != "success" {
		t.Errorf("first run status = %q, want %q", runs[0].Status, "success")
	}
	if runs[1].ID != "222" {
		t.Errorf("second run ID = %q, want %q", runs[1].ID, "222")
	}
	if runs[1].Status != "failure" {
		t.Errorf("second run status = %q, want %q", runs[1].Status, "failure")
	}

	// Verify resolved runs are stored
	if _, ok := source.resolvedRuns["111"]; !ok {
		t.Error("expected resolved run for build 111")
	}
	if _, ok := source.resolvedRuns["222"]; !ok {
		t.Error("expected resolved run for build 222")
	}

	// Verify limit
	runs, err = source.ListRecentRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error with limit: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run with limit=1, got %d", len(runs))
	}
	if runs[0].ID != "111" {
		t.Errorf("limited run ID = %q, want %q (most recent)", runs[0].ID, "111")
	}
}

func TestGCSDirectoryJobSource_ListRecentRuns_OnlyFetchesRecentItems(t *testing.T) {
	// Create 200 items but only the most recent ones (by build ID) should have
	// their finished.json fetched. Old items should never be accessed.
	const totalItems = 200

	// Track which finished.json URLs were fetched
	fetchedBuildIDs := make(map[string]bool)

	passed := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/storage/v1/b/test-bucket/o":
			// Generate items with build IDs from 1 to totalItems
			var items []gcsDirectoryItem
			for i := 1; i <= totalItems; i++ {
				items = append(items, gcsDirectoryItem{
					Name: fmt.Sprintf("pr-logs/directory/pull-job/%d.txt", i),
					Metadata: map[string]string{
						"link": fmt.Sprintf("gs://test-bucket/pr-logs/pull/org_repo/10/pull-job/%d", i),
					},
				})
			}
			resp := gcsDirectoryListResponse{Items: items}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		default:
			// Handle finished.json requests
			// Extract build ID from path like /test-bucket/pr-logs/pull/org_repo/10/pull-job/{id}/finished.json
			path := r.URL.Path
			if path != "" && path[0] == '/' {
				path = path[1:]
			}
			parts := strings.Split(path, "/")
			if len(parts) >= 7 && parts[len(parts)-1] == "finished.json" {
				buildID := parts[len(parts)-2]
				fetchedBuildIDs[buildID] = true

				finished := gcsFinishedJSON{
					Passed:    &passed,
					Result:    "SUCCESS",
					Timestamp: 1700000000 + int64(len(buildID)), // slightly different timestamps
				}
				finishedBytes, _ := json.Marshal(finished)
				_, _ = w.Write(finishedBytes)
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := &GCSDirectoryJobSource{
		bucket:       "test-bucket",
		prefix:       "pr-logs/directory/pull-job/",
		jobName:      "pull-job",
		client:       &http.Client{Transport: &rewriteTransport{base: server.URL}},
		resolvedRuns: make(map[string]gcsRef),
	}

	runs, err := source.ListRecentRuns(context.Background(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runs) != 10 {
		t.Fatalf("expected 10 runs, got %d", len(runs))
	}

	// Verify that we fetched at most 50 finished.json files (not all 200)
	if len(fetchedBuildIDs) > 50 {
		t.Errorf("fetched finished.json for %d items, expected at most 50 (out of %d total)",
			len(fetchedBuildIDs), totalItems)
	}

	// Verify that old build IDs (low numbers) were NOT fetched
	for i := 1; i <= totalItems-50; i++ {
		buildID := fmt.Sprintf("%d", i)
		if fetchedBuildIDs[buildID] {
			t.Errorf("fetched finished.json for old build ID %s, expected it to be skipped", buildID)
			break // one error is enough to demonstrate the issue
		}
	}

	// Verify that recent build IDs (high numbers) WERE fetched
	for i := totalItems - 49; i <= totalItems; i++ {
		buildID := fmt.Sprintf("%d", i)
		if !fetchedBuildIDs[buildID] {
			t.Errorf("expected finished.json to be fetched for recent build ID %s", buildID)
			break
		}
	}
}

func TestExtractBuildIDNumeric(t *testing.T) {
	testCases := []struct {
		name   string
		input  string
		prefix string
		want   int64
	}{
		{
			name:   "valid numeric build ID",
			input:  "pr-logs/directory/pull-job/12345.txt",
			prefix: "pr-logs/directory/pull-job/",
			want:   12345,
		},
		{
			name:   "zero build ID",
			input:  "pr-logs/directory/pull-job/0.txt",
			prefix: "pr-logs/directory/pull-job/",
			want:   0,
		},
		{
			name:   "non-numeric build ID",
			input:  "pr-logs/directory/pull-job/abc.txt",
			prefix: "pr-logs/directory/pull-job/",
			want:   -1,
		},
		{
			name:   "empty after prefix strip",
			input:  "pr-logs/directory/pull-job/.txt",
			prefix: "pr-logs/directory/pull-job/",
			want:   -1,
		},
		{
			name:   "missing .txt suffix",
			input:  "pr-logs/directory/pull-job/12345.log",
			prefix: "pr-logs/directory/pull-job/",
			want:   -1,
		},
		{
			name:   "large build ID",
			input:  "pr-logs/directory/pull-job/1700000000.txt",
			prefix: "pr-logs/directory/pull-job/",
			want:   1700000000,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractBuildIDNumeric(tc.input, tc.prefix)
			if got != tc.want {
				t.Errorf("extractBuildIDNumeric(%q, %q) = %d, want %d", tc.input, tc.prefix, got, tc.want)
			}
		})
	}
}

func TestGCSDirectoryJobSource_FetchLog(t *testing.T) {
	expectedLog := "build log content here"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/test-bucket/pr-logs/pull/org_repo/10/pull-job/111/build-log.txt" {
			fmt.Fprint(w, expectedLog)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	source := &GCSDirectoryJobSource{
		bucket:  "test-bucket",
		prefix:  "pr-logs/directory/pull-job/",
		jobName: "pull-job",
		client: &http.Client{
			Transport: &rewriteTransport{base: server.URL},
		},
		resolvedRuns: map[string]gcsRef{
			"111": {
				bucket: "test-bucket",
				prefix: "pr-logs/pull/org_repo/10/pull-job/111/",
			},
		},
	}

	log, err := source.FetchLog(context.Background(), "111")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if log != expectedLog {
		t.Errorf("log = %q, want %q", log, expectedLog)
	}

	// Fetching unknown run ID should fail
	_, err = source.FetchLog(context.Background(), "999")
	if err == nil {
		t.Error("expected error for unknown run ID, got nil")
	}
}

func TestMatchLanePattern(t *testing.T) {
	tests := []struct {
		name      string
		jobName   string
		patterns  []string
		wantMatch bool
		wantLane  string
	}{
		{
			name:      "wildcard match prefix",
			jobName:   "e2e (kv-live-migration, noHA, local, dualstack, noSnatGW, 1br, ic-single-node-zones, 3, enable-network-segmentation)",
			patterns:  []string{"e2e (kv-live-migration, noHA, local,*"},
			wantMatch: true,
			wantLane:  "e2e (kv-live-migration, noHA, local, dualstack, noSnatGW, 1br, ic-single-node-zones, 3, enable-network-segmentation)",
		},
		{
			name:      "exact match",
			jobName:   "build",
			patterns:  []string{"build"},
			wantMatch: true,
			wantLane:  "build",
		},
		{
			name:      "no match",
			jobName:   "e2e (kv-live-migration, noHA, shared, dualstack)",
			patterns:  []string{"e2e (kv-live-migration, noHA, local,*"},
			wantMatch: false,
		},
		{
			name:      "multiple patterns second matches",
			jobName:   "e2e (kv-live-migration, noHA, shared, dualstack)",
			patterns:  []string{"e2e (kv-live-migration, noHA, local,*", "e2e (kv-live-migration, noHA, shared,*"},
			wantMatch: true,
			wantLane:  "e2e (kv-live-migration, noHA, shared, dualstack)",
		},
		{
			name:      "empty patterns",
			jobName:   "anything",
			patterns:  []string{},
			wantMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matched, lane := matchLanePattern(tc.jobName, tc.patterns)
			if matched != tc.wantMatch {
				t.Errorf("matched = %v, want %v", matched, tc.wantMatch)
			}
			if matched && lane != tc.wantLane {
				t.Errorf("lane = %q, want %q", lane, tc.wantLane)
			}
		})
	}
}

func TestGitHubActionsJobSource_LaneLevel_ListRecentRuns(t *testing.T) {
	mock := &laneTestMockGH{
		workflowRuns: []WorkflowRun{
			{ID: 100, Status: "completed", Conclusion: "failure", HTMLURL: "https://github.com/o/r/actions/runs/100"},
			{ID: 101, Status: "completed", Conclusion: "success", HTMLURL: "https://github.com/o/r/actions/runs/101"},
			{ID: 102, Status: "completed", Conclusion: "failure", HTMLURL: "https://github.com/o/r/actions/runs/102"},
		},
		jobsByRun: map[int64][]WorkflowJob{
			100: {
				{ID: 1001, Name: "e2e (kv-live-migration, noHA, local, dualstack)", Conclusion: "failure"},
				{ID: 1002, Name: "e2e (kv-live-migration, noHA, shared, dualstack)", Conclusion: "success"},
				{ID: 1003, Name: "e2e (other-lane, noHA)", Conclusion: "failure"},
			},
			102: {
				{ID: 1021, Name: "e2e (kv-live-migration, noHA, local, dualstack)", Conclusion: "success"},
				{ID: 1022, Name: "e2e (kv-live-migration, noHA, shared, dualstack)", Conclusion: "failure"},
			},
		},
	}

	source := &GitHubActionsJobSource{
		owner:        "o",
		repo:         "r",
		workflow:     "test.yml",
		jobName:      "o/r/test.yml",
		gh:           mock,
		lanePatterns: []string{"e2e (kv-live-migration,*"},
		matchedJobs:  make(map[string]int64),
	}

	runs, err := source.ListRecentRuns(context.Background(), 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should find 2 failures across the runs:
	// - run 100: local lane failed (job 1001), shared lane passed, other lane failed but doesn't match pattern
	// - run 101: skipped (success)
	// - run 102: local lane passed, shared lane failed (job 1022)
	if len(runs) != 2 {
		t.Fatalf("expected 2 lane failures, got %d: %v", len(runs), runs)
	}

	// First failure: run 100, local lane
	if runs[0].Status != "failure" {
		t.Errorf("runs[0].Status = %q, want failure", runs[0].Status)
	}
	if !strings.Contains(runs[0].JobName, "kv-live-migration") {
		t.Errorf("runs[0].JobName = %q, expected kv-live-migration lane", runs[0].JobName)
	}

	// Verify FetchLog uses the matched job ID
	expectedRunID := runs[0].ID
	if _, ok := source.matchedJobs[expectedRunID]; !ok {
		t.Errorf("matchedJobs missing entry for run ID %q", expectedRunID)
	}
}

func TestGitHubActionsJobSource_LaneLevel_FetchLog(t *testing.T) {
	mock := &laneTestMockGH{
		jobLogs: map[int64]string{
			1001: "lane failure log content",
		},
	}

	source := &GitHubActionsJobSource{
		owner:        "o",
		repo:         "r",
		workflow:     "test.yml",
		jobName:      "o/r/test.yml",
		gh:           mock,
		lanePatterns: []string{"e2e*"},
		matchedJobs:  map[string]int64{"100:1001": 1001},
	}

	log, err := source.FetchLog(context.Background(), "100:1001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if log != "lane failure log content" {
		t.Errorf("log = %q, want %q", log, "lane failure log content")
	}

	// Unknown run ID should fail
	_, err = source.FetchLog(context.Background(), "999:0")
	if err == nil {
		t.Error("expected error for unknown lane run ID")
	}
}

func TestGitHubActionsJobSource_LaneLevel_SkipsSuccessRuns(t *testing.T) {
	listJobsCalled := false
	mock := &laneTestMockGH{
		workflowRuns: []WorkflowRun{
			{ID: 200, Status: "completed", Conclusion: "success"},
		},
		onListJobs: func() { listJobsCalled = true },
	}

	source := &GitHubActionsJobSource{
		owner:        "o",
		repo:         "r",
		workflow:     "test.yml",
		jobName:      "o/r/test.yml",
		gh:           mock,
		lanePatterns: []string{"e2e*"},
		matchedJobs:  make(map[string]int64),
	}

	runs, err := source.ListRecentRuns(context.Background(), 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
	if listJobsCalled {
		t.Error("ListWorkflowJobs should not be called for success runs")
	}
}

// laneTestMockGH is a minimal mock for testing lane-level GitHubActionsJobSource.
// It only implements the methods needed by the lane filtering logic.
type laneTestMockGH struct {
	workflowRuns []WorkflowRun
	jobsByRun    map[int64][]WorkflowJob
	jobLogs      map[int64]string
	onListJobs   func() // callback for tracking ListWorkflowJobs calls
}

func (m *laneTestMockGH) ListWorkflowRuns(_ context.Context, _, _, _, _ string, _ int, _ time.Time) ([]WorkflowRun, error) {
	return m.workflowRuns, nil
}

func (m *laneTestMockGH) ListWorkflowJobs(_ context.Context, _, _ string, runID int64) ([]WorkflowJob, error) {
	if m.onListJobs != nil {
		m.onListJobs()
	}
	if m.jobsByRun != nil {
		return m.jobsByRun[runID], nil
	}
	return nil, nil
}

func (m *laneTestMockGH) GetWorkflowJobLogs(_ context.Context, _, _ string, jobID int64) (string, error) {
	if m.jobLogs != nil {
		if log, ok := m.jobLogs[jobID]; ok {
			return log, nil
		}
	}
	return "", fmt.Errorf("no logs for job %d", jobID)
}

// Unused interface methods (satisfy GitHubClient)
func (m *laneTestMockGH) ListLabeledIssues(context.Context, string, string, string) ([]Issue, error) {
	return nil, nil
}
func (m *laneTestMockGH) GetPRReviewComments(context.Context, string, string, int, int64) ([]ReviewComment, error) {
	return nil, nil
}
func (m *laneTestMockGH) GetIssueComments(context.Context, string, string, int, int64) ([]ReviewComment, error) {
	return nil, nil
}
func (m *laneTestMockGH) GetPRState(context.Context, string, string, int) (string, error) {
	return "", nil
}
func (m *laneTestMockGH) AddIssueComment(context.Context, string, string, int, string) error {
	return nil
}
func (m *laneTestMockGH) AddLabel(context.Context, string, string, int, string) error {
	return nil
}
func (m *laneTestMockGH) RemoveLabel(context.Context, string, string, int, string) error {
	return nil
}
func (m *laneTestMockGH) ListPRsByHead(context.Context, string, string, string, string) ([]PR, error) {
	return nil, nil
}
func (m *laneTestMockGH) AddPRCommentReaction(context.Context, string, string, int64, string) error {
	return nil
}
func (m *laneTestMockGH) AddIssueCommentReaction(context.Context, string, string, int64, string) error {
	return nil
}
func (m *laneTestMockGH) GetCheckRuns(context.Context, string, string, string) ([]CheckRun, error) {
	return nil, nil
}
func (m *laneTestMockGH) GetCheckRunLog(context.Context, string, string, int64) (string, error) {
	return "", nil
}
func (m *laneTestMockGH) GetPRHeadSHA(context.Context, string, string, int) (string, error) {
	return "", nil
}
func (m *laneTestMockGH) HasPRCommentReaction(context.Context, string, string, int64, string, string) (bool, error) {
	return false, nil
}
func (m *laneTestMockGH) ReplyToPRComment(context.Context, string, string, int, int64, string) error {
	return nil
}
func (m *laneTestMockGH) AssignIssue(context.Context, string, string, int, string) error {
	return nil
}
func (m *laneTestMockGH) UnassignIssue(context.Context, string, string, int, string) error {
	return nil
}
func (m *laneTestMockGH) GetPRMergeable(context.Context, string, string, int) (string, error) {
	return "", nil
}
func (m *laneTestMockGH) GetPRReviews(context.Context, string, string, int, int64) ([]PRReview, error) {
	return nil, nil
}
func (m *laneTestMockGH) GetPRHeadCommitDate(context.Context, string, string, int) (time.Time, error) {
	return time.Time{}, nil
}
func (m *laneTestMockGH) CreatePR(context.Context, string, string, string, string, string, string) (int, error) {
	return 0, nil
}
func (m *laneTestMockGH) HasLinkedPR(context.Context, string, string, int) (bool, error) {
	return false, nil
}
func (m *laneTestMockGH) GetPR(context.Context, string, string, int) (PR, error) { return PR{}, nil }
func (m *laneTestMockGH) IsPRBehind(context.Context, string, string, int) (bool, error) {
	return false, nil
}
func (m *laneTestMockGH) CreateIssue(context.Context, string, string, string, string, []string) (int, error) {
	return 0, nil
}
func (m *laneTestMockGH) SearchIssues(context.Context, string) ([]Issue, error) { return nil, nil }
func (m *laneTestMockGH) GetCommitStatuses(context.Context, string, string, string) ([]CheckRun, error) {
	return nil, nil
}
func (m *laneTestMockGH) CountCommitsSince(context.Context, string, string, time.Time) (int, error) {
	return 0, nil
}

// rewriteTransport rewrites all request URLs to point to a test server.
type rewriteTransport struct {
	base string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the URL to point to the test server, keeping the path
	req.URL.Scheme = "http"
	req.URL.Host = t.base[len("http://"):]
	return http.DefaultTransport.RoundTrip(req)
}
