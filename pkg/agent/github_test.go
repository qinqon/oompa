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
		json.NewEncoder(w).Encode(issues)
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
		json.NewEncoder(w).Encode(comments)
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
		json.NewEncoder(w).Encode(pr)
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
		json.NewEncoder(w).Encode(pr)
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
		json.NewEncoder(w).Encode(pr)
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
		json.NewDecoder(r.Body).Decode(&comment)
		receivedBody = comment.GetBody()
		json.NewEncoder(w).Encode(&comment)
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
		json.NewDecoder(r.Body).Decode(&receivedLabels)
		json.NewEncoder(w).Encode([]*github.Label{{Name: github.Ptr("ai-failed")}})
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
		json.NewEncoder(w).Encode(prs)
	})

	gh := setupTestClient(t, mux)
	prs, err := gh.ListPRsByHead(context.Background(), "owner", "repo", "ai/issue-42")
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

func TestGetAuthenticatedUser_WithNameAndEmail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/user", func(w http.ResponseWriter, r *http.Request) {
		user := &github.User{
			Login: github.Ptr("jdoe"),
			Name:  github.Ptr("Jane Doe"),
			Email: github.Ptr("jane@example.com"),
		}
		json.NewEncoder(w).Encode(user)
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
		json.NewEncoder(w).Encode(user)
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
