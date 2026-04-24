package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v84/github"
)

func setupTestClient(t *testing.T, mux *http.ServeMux) *GoGitHubClient {
	t.Helper()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := github.NewClient(nil).WithAuthToken("test-token")
	baseURL := server.URL + "/"
	var err error
	client, err = client.WithEnterpriseURLs(baseURL, baseURL)
	if err != nil {
		t.Fatalf("failed to set base URL: %v", err)
	}

	return &GoGitHubClient{client: client}
}

func TestListLabeledIssues(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		issues := []*github.Issue{
			{
				Number: github.Ptr(42),
				Title:  github.Ptr("Fix bug"),
				Body:   github.Ptr("Something is broken"),
				Labels: []*github.Label{{Name: github.Ptr("good-for-ai")}},
			},
		}
		_ = json.NewEncoder(w).Encode(issues)
	})

	gh := setupTestClient(t, mux)
	issues, err := gh.ListLabeledIssues(context.Background(), "owner", "repo", "good-for-ai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Number != 42 {
		t.Errorf("expected issue 42, got %d", issues[0].Number)
	}
	if issues[0].Title != "Fix bug" {
		t.Errorf("expected title 'Fix bug', got %q", issues[0].Title)
	}
}

func TestGetPRReviewComments_FiltersBySinceID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls/1/comments", func(w http.ResponseWriter, r *http.Request) {
		comments := []*github.PullRequestComment{
			{ID: github.Ptr(int64(10)), User: &github.User{Login: github.Ptr("alice")}, Body: github.Ptr("old comment")},
			{ID: github.Ptr(int64(20)), User: &github.User{Login: github.Ptr("bob")}, Body: github.Ptr("new comment"), Path: github.Ptr("main.go"), Line: github.Ptr(5)},
		}
		_ = json.NewEncoder(w).Encode(comments)
	})

	gh := setupTestClient(t, mux)
	comments, err := gh.GetPRReviewComments(context.Background(), "owner", "repo", 1, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment (filtered), got %d", len(comments))
	}
	if comments[0].ID != 20 {
		t.Errorf("expected comment ID 20, got %d", comments[0].ID)
	}
	if comments[0].Path != "main.go" {
		t.Errorf("expected path 'main.go', got %q", comments[0].Path)
	}
}

func TestGetPRState_Merged(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		pr := &github.PullRequest{State: github.Ptr("closed"), Merged: github.Ptr(true)}
		_ = json.NewEncoder(w).Encode(pr)
	})

	gh := setupTestClient(t, mux)
	state, err := gh.GetPRState(context.Background(), "owner", "repo", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "merged" {
		t.Errorf("expected 'merged', got %q", state)
	}
}

func TestGetPRState_Closed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		pr := &github.PullRequest{State: github.Ptr("closed"), Merged: github.Ptr(false)}
		_ = json.NewEncoder(w).Encode(pr)
	})

	gh := setupTestClient(t, mux)
	state, err := gh.GetPRState(context.Background(), "owner", "repo", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "closed" {
		t.Errorf("expected 'closed', got %q", state)
	}
}

func TestGetPRState_Open(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		pr := &github.PullRequest{State: github.Ptr("open"), Merged: github.Ptr(false)}
		_ = json.NewEncoder(w).Encode(pr)
	})

	gh := setupTestClient(t, mux)
	state, err := gh.GetPRState(context.Background(), "owner", "repo", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "open" {
		t.Errorf("expected 'open', got %q", state)
	}
}

func TestAddIssueComment(t *testing.T) {
	var receivedBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		var comment github.IssueComment
		_ = json.NewDecoder(r.Body).Decode(&comment)
		receivedBody = comment.GetBody()
		_ = json.NewEncoder(w).Encode(&comment)
	})

	gh := setupTestClient(t, mux)
	err := gh.AddIssueComment(context.Background(), "owner", "repo", 42, "test comment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedBody != "test comment" {
		t.Errorf("expected body 'test comment', got %q", receivedBody)
	}
}

func TestAddLabel(t *testing.T) {
	var receivedLabels []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues/42/labels", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedLabels)
		_ = json.NewEncoder(w).Encode([]*github.Label{{Name: github.Ptr("ai-failed")}})
	})

	gh := setupTestClient(t, mux)
	err := gh.AddLabel(context.Background(), "owner", "repo", 42, "ai-failed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(receivedLabels) != 1 || receivedLabels[0] != "ai-failed" {
		t.Errorf("expected [ai-failed], got %v", receivedLabels)
	}
}

func TestRemoveLabel(t *testing.T) {
	var called bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues/42/labels/good-for-ai", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	gh := setupTestClient(t, mux)
	err := gh.RemoveLabel(context.Background(), "owner", "repo", 42, "good-for-ai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected remove label endpoint to be called")
	}
}

func TestListPRsByHead(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		prs := []*github.PullRequest{
			{
				Number: github.Ptr(100),
				State:  github.Ptr("open"),
				Merged: github.Ptr(false),
				Head:   &github.PullRequestBranch{Ref: github.Ptr("ai/issue-42")},
			},
		}
		_ = json.NewEncoder(w).Encode(prs)
	})

	gh := setupTestClient(t, mux)
	prs, err := gh.ListPRsByHead(context.Background(), "owner", "repo", "qinqon", "ai/issue-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(prs))
	}
	if prs[0].Number != 100 {
		t.Errorf("expected PR 100, got %d", prs[0].Number)
	}
	if prs[0].Head != "ai/issue-42" {
		t.Errorf("expected head 'ai/issue-42', got %q", prs[0].Head)
	}
}

func TestHasLinkedPR_FindsOpenPR(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues/42/timeline", func(w http.ResponseWriter, r *http.Request) {
		events := []map[string]any{
			{
				"event": "labeled",
			},
			{
				"event": "cross-referenced",
				"source": map[string]any{
					"type": "issue",
					"issue": map[string]any{
						"number":             99,
						"state":              "open",
						"pull_request":       map[string]any{"url": "https://api.github.com/repos/owner/repo/pulls/99"},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(events)
	})

	gh := setupTestClient(t, mux)
	linked, err := gh.HasLinkedPR(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !linked {
		t.Error("expected HasLinkedPR to return true for open PR")
	}
}

func TestHasLinkedPR_IgnoresClosedPR(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues/42/timeline", func(w http.ResponseWriter, r *http.Request) {
		events := []map[string]any{
			{
				"event": "cross-referenced",
				"source": map[string]any{
					"type": "issue",
					"issue": map[string]any{
						"number":             99,
						"state":              "closed",
						"pull_request":       map[string]any{"url": "https://api.github.com/repos/owner/repo/pulls/99"},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(events)
	})

	gh := setupTestClient(t, mux)
	linked, err := gh.HasLinkedPR(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if linked {
		t.Error("expected HasLinkedPR to return false for closed PR")
	}
}

func TestHasLinkedPR_NoLinkedPRs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues/42/timeline", func(w http.ResponseWriter, r *http.Request) {
		events := []map[string]any{
			{"event": "labeled"},
			{"event": "assigned"},
		}
		_ = json.NewEncoder(w).Encode(events)
	})

	gh := setupTestClient(t, mux)
	linked, err := gh.HasLinkedPR(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if linked {
		t.Error("expected HasLinkedPR to return false when no linked PRs")
	}
}

func TestGetAuthenticatedUser_WithNameAndEmail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/user", func(w http.ResponseWriter, r *http.Request) {
		user := &github.User{
			Login: github.Ptr("jdoe"),
			Name:  github.Ptr("Jane Doe"),
			Email: github.Ptr("jane@example.com"),
		}
		_ = json.NewEncoder(w).Encode(user)
	})

	gh := setupTestClient(t, mux)
	login, name, email, err := gh.GetAuthenticatedUser(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if login != "jdoe" {
		t.Errorf("expected login 'jdoe', got %q", login)
	}
	if name != "Jane Doe" {
		t.Errorf("expected name 'Jane Doe', got %q", name)
	}
	if email != "jane@example.com" {
		t.Errorf("expected email 'jane@example.com', got %q", email)
	}
}

func TestGetAuthenticatedUser_FallbackToLogin(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/user", func(w http.ResponseWriter, r *http.Request) {
		user := &github.User{
			Login: github.Ptr("jdoe"),
		}
		_ = json.NewEncoder(w).Encode(user)
	})

	gh := setupTestClient(t, mux)
	login, name, email, err := gh.GetAuthenticatedUser(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if login != "jdoe" {
		t.Errorf("expected login 'jdoe', got %q", login)
	}
	if name != "jdoe" {
		t.Errorf("expected name 'jdoe' (login fallback), got %q", name)
	}
	if email != "jdoe@users.noreply.github.com" {
		t.Errorf("expected noreply email fallback, got %q", email)
	}
}

func TestCreateIssue(t *testing.T) {
	var receivedTitle, receivedBody string
	var receivedLabels []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		var req github.IssueRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		receivedTitle = req.GetTitle()
		receivedBody = req.GetBody()
		receivedLabels = *req.Labels
		issue := &github.Issue{
			Number: github.Ptr(123),
			Title:  req.Title,
			Body:   req.Body,
		}
		_ = json.NewEncoder(w).Encode(issue)
	})

	gh := setupTestClient(t, mux)
	issueNum, err := gh.CreateIssue(context.Background(), "owner", "repo", "Test Issue", "Test body", []string{"flaky-test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issueNum != 123 {
		t.Errorf("expected issue number 123, got %d", issueNum)
	}
	if receivedTitle != "Test Issue" {
		t.Errorf("expected title 'Test Issue', got %q", receivedTitle)
	}
	if receivedBody != "Test body" {
		t.Errorf("expected body 'Test body', got %q", receivedBody)
	}
	if len(receivedLabels) != 1 || receivedLabels[0] != "flaky-test" {
		t.Errorf("expected labels ['flaky-test'], got %v", receivedLabels)
	}
}

func TestGetLatestReleaseSHA(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		release := &github.RepositoryRelease{
			TargetCommitish: github.Ptr("abc123def456"),
			TagName:         github.Ptr("latest"),
		}
		_ = json.NewEncoder(w).Encode(release)
	})

	gh := setupTestClient(t, mux)
	sha, err := gh.GetLatestReleaseSHA(context.Background(), "owner", "repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sha != "abc123def456" {
		t.Errorf("expected SHA 'abc123def456', got %q", sha)
	}
}

func TestGetLatestReleaseSHA_NoRelease(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	gh := setupTestClient(t, mux)
	_, err := gh.GetLatestReleaseSHA(context.Background(), "owner", "repo")
	if err == nil {
		t.Fatal("expected error for no release, got nil")
	}
}

func TestSearchIssues(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/search/issues", func(w http.ResponseWriter, r *http.Request) {
		result := map[string]any{
			"total_count": 2,
			"items": []map[string]any{
				{
					"number": 42,
					"title":  "Flaky CI: integration-tests",
					"body":   "Test failure",
					"labels": []map[string]any{
						{"name": "flaky-test"},
					},
				},
				{
					"number": 43,
					"title":  "Flaky CI: e2e-tests",
					"body":   "Another failure",
					"labels": []map[string]any{
						{"name": "flaky-test"},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	gh := setupTestClient(t, mux)
	issues, err := gh.SearchIssues(context.Background(), "repo:owner/repo is:issue is:open label:flaky-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
	if issues[0].Number != 42 {
		t.Errorf("expected issue 42, got %d", issues[0].Number)
	}
	if issues[0].Title != "Flaky CI: integration-tests" {
		t.Errorf("expected title 'Flaky CI: integration-tests', got %q", issues[0].Title)
	}
	if len(issues[0].Labels) != 1 || issues[0].Labels[0] != "flaky-test" {
		t.Errorf("expected labels ['flaky-test'], got %v", issues[0].Labels)
	}
}

func TestSearchIssues_FiltersPullRequests(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/search/issues", func(w http.ResponseWriter, r *http.Request) {
		result := map[string]any{
			"total_count": 2,
			"items": []map[string]any{
				{
					"number":       42,
					"title":        "Flaky CI: integration-tests",
					"body":         "Test failure",
					"labels":       []map[string]any{{"name": "flaky-test"}},
				},
				{
					"number":       100,
					"title":        "Fix flaky test",
					"body":         "PR description",
					"pull_request": map[string]any{"url": "https://api.github.com/repos/owner/repo/pulls/100"},
					"labels":       []map[string]any{{"name": "flaky-test"}},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	gh := setupTestClient(t, mux)
	issues, err := gh.SearchIssues(context.Background(), "repo:owner/repo label:flaky-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should filter out PRs and only return issues
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue (PR filtered out), got %d", len(issues))
	}
	if issues[0].Number != 42 {
		t.Errorf("expected issue 42, got %d", issues[0].Number)
	}
}

func TestGetCheckRuns_UsesPerPage100(t *testing.T) {
	var receivedPerPage int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/commits/abc123/check-runs", func(w http.ResponseWriter, r *http.Request) {
		// Capture the per_page query parameter
		if pp := r.URL.Query().Get("per_page"); pp != "" {
			receivedPerPage = 100 // If per_page is set in URL, it should be 100
			if pp != "100" {
				t.Errorf("expected per_page=100, got %q", pp)
			}
		}

		result := map[string]any{
			"total_count": 2,
			"check_runs": []map[string]any{
				{
					"id":         int64(1001),
					"name":       "test-job",
					"status":     "completed",
					"conclusion": "success",
				},
				{
					"id":         int64(1002),
					"name":       "lint-job",
					"status":     "completed",
					"conclusion": "failure",
					"output": map[string]any{
						"text":    "Linting errors found",
						"summary": "3 errors",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	gh := setupTestClient(t, mux)
	runs, err := gh.GetCheckRuns(context.Background(), "owner", "repo", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 check runs, got %d", len(runs))
	}
	if runs[0].ID != 1001 {
		t.Errorf("expected check run ID 1001, got %d", runs[0].ID)
	}
	if runs[0].Conclusion != "success" {
		t.Errorf("expected conclusion 'success', got %q", runs[0].Conclusion)
	}
	if runs[1].ID != 1002 {
		t.Errorf("expected check run ID 1002, got %d", runs[1].ID)
	}
	if runs[1].Output != "Linting errors found" {
		t.Errorf("expected output 'Linting errors found', got %q", runs[1].Output)
	}
	if receivedPerPage != 100 {
		t.Error("expected per_page parameter to be set to 100")
	}
}
