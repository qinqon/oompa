package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseGCSJobURL(t *testing.T) {
	testCases := []struct {
		name        string
		url         string
		wantBucket  string
		wantPrefix  string
		wantErr     bool
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

