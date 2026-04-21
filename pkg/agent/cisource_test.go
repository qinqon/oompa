package agent

import (
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

