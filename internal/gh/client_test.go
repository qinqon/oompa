package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v88/github"
)

func setupTestClient(t *testing.T, mux *http.ServeMux) *RESTClient {
	t.Helper()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	baseURL := server.URL + "/"
	client, err := github.NewClient(
		github.WithAuthToken("test-token"),
		github.WithEnterpriseURLs(baseURL, baseURL),
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	return &RESTClient{client: client}
}

func TestListLabeledIssues(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		issues := []*github.Issue{
			{
				Number: new(42),
				Title:  new("Fix bug"),
				Body:   new("Something is broken"),
				Labels: []*github.Label{{Name: new("good-for-ai")}},
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
			{ID: new(int64(10)), User: &github.User{Login: new("alice")}, Body: new("old comment")},
			{ID: new(int64(20)), User: &github.User{Login: new("bob")}, Body: new("new comment"), Path: new("main.go"), Line: new(5)},
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

func TestGetPRReviewComments_Paginates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls/1/comments", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Fatalf("expected per_page=100, got %q", got)
		}
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			// Page 1: return comments and indicate there's a next page
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			comments := []*github.PullRequestComment{
				{ID: new(int64(10)), User: &github.User{Login: new("alice")}, Body: new("page 1 comment"), Path: new("file1.go"), Line: new(1)},
			}
			_ = json.NewEncoder(w).Encode(comments)
		} else {
			// Page 2: return more comments, no next page
			comments := []*github.PullRequestComment{
				{ID: new(int64(20)), User: &github.User{Login: new("bob")}, Body: new("page 2 comment"), Path: new("file2.go"), Line: new(5)},
			}
			_ = json.NewEncoder(w).Encode(comments)
		}
	})

	gh := setupTestClient(t, mux)
	comments, err := gh.GetPRReviewComments(context.Background(), "owner", "repo", 1, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments across pages, got %d", len(comments))
	}
	if comments[0].ID != 10 {
		t.Errorf("expected first comment ID 10, got %d", comments[0].ID)
	}
	if comments[0].Body != "page 1 comment" {
		t.Errorf("expected first comment body 'page 1 comment', got %q", comments[0].Body)
	}
	if comments[1].ID != 20 {
		t.Errorf("expected second comment ID 20, got %d", comments[1].ID)
	}
	if comments[1].Body != "page 2 comment" {
		t.Errorf("expected second comment body 'page 2 comment', got %q", comments[1].Body)
	}
}

func TestGetPRReviewComments_SinceIDFilterAcrossPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls/1/comments", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			comments := []*github.PullRequestComment{
				{ID: new(int64(5)), User: &github.User{Login: new("alice")}, Body: new("old comment")},
				{ID: new(int64(10)), User: &github.User{Login: new("bob")}, Body: new("boundary comment")},
			}
			_ = json.NewEncoder(w).Encode(comments)
		} else {
			comments := []*github.PullRequestComment{
				{ID: new(int64(15)), User: &github.User{Login: new("charlie")}, Body: new("new comment page 2"), Path: new("main.go"), Line: new(42)},
			}
			_ = json.NewEncoder(w).Encode(comments)
		}
	})

	gh := setupTestClient(t, mux)
	// sinceID=10 should filter out IDs 5 and 10, keeping only ID 15 from page 2
	comments, err := gh.GetPRReviewComments(context.Background(), "owner", "repo", 1, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment after filtering, got %d", len(comments))
	}
	if comments[0].ID != 15 {
		t.Errorf("expected comment ID 15, got %d", comments[0].ID)
	}
	if comments[0].Path != "main.go" {
		t.Errorf("expected path 'main.go', got %q", comments[0].Path)
	}
	if comments[0].Line != 42 {
		t.Errorf("expected line 42, got %d", comments[0].Line)
	}
}

func TestGetPRState_Merged(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		pr := &github.PullRequest{State: new("closed"), Merged: new(true)}
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
		pr := &github.PullRequest{State: new("closed"), Merged: new(false)}
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
		pr := &github.PullRequest{State: new("open"), Merged: new(false)}
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
		_ = json.NewEncoder(w).Encode([]*github.Label{{Name: new("ai-failed")}})
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

func TestListPRsByHead(t *testing.T) {
	var closedRequests int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "open" {
			closedRequests++
			_ = json.NewEncoder(w).Encode([]*github.PullRequest{})
			return
		}
		prs := []*github.PullRequest{
			{
				Number: new(100),
				State:  new("open"),
				Merged: new(false),
				Head:   &github.PullRequestBranch{Ref: new("ai/issue-42")},
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
	if closedRequests != 0 {
		t.Errorf("open match should short-circuit the closed scan, got %d closed requests", closedRequests)
	}
}

func TestListPRsByHead_PaginatesOpenPRs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("per_page"); got != "100" {
			t.Errorf("expected per_page=100, got %q", got)
		}
		if q.Get("state") != "open" {
			_ = json.NewEncoder(w).Encode([]*github.PullRequest{})
			return
		}
		if page := q.Get("page"); page == "" || page == "1" {
			// Page 1: only non-matching PRs (simulates GitHub ignoring the head filter)
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			prs := []*github.PullRequest{
				{
					Number: new(1),
					State:  new("open"),
					Head:   &github.PullRequestBranch{Ref: new("some/other-branch")},
				},
			}
			_ = json.NewEncoder(w).Encode(prs)
			return
		}
		// Page 2: the matching PR
		prs := []*github.PullRequest{
			{
				Number: new(100),
				State:  new("open"),
				Head:   &github.PullRequestBranch{Ref: new("ai/issue-42")},
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
		t.Fatalf("expected 1 PR found on page 2, got %d", len(prs))
	}
	if prs[0].Number != 100 {
		t.Errorf("expected PR 100, got %d", prs[0].Number)
	}
}

func TestListPRsByHead_FindsClosedPRAcrossPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != "closed" {
			_ = json.NewEncoder(w).Encode([]*github.PullRequest{})
			return
		}
		if page := q.Get("page"); page == "" || page == "1" {
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			prs := []*github.PullRequest{
				{
					Number: new(1),
					State:  new("closed"),
					Head:   &github.PullRequestBranch{Ref: new("some/other-branch")},
				},
			}
			_ = json.NewEncoder(w).Encode(prs)
			return
		}
		// Page 2: the matching merged PR. The list endpoint only exposes
		// merged_at, never the merged boolean.
		prs := []*github.PullRequest{
			{
				Number:   new(100),
				State:    new("closed"),
				MergedAt: &github.Timestamp{Time: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
				Head:     &github.PullRequestBranch{Ref: new("ai/issue-42")},
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
		t.Fatalf("expected 1 PR found on closed page 2, got %d", len(prs))
	}
	if prs[0].Number != 100 {
		t.Errorf("expected PR 100, got %d", prs[0].Number)
	}
	if !prs[0].Merged {
		t.Error("expected Merged=true derived from merged_at")
	}
}

func TestListPRsByHead_CapsClosedPRScan(t *testing.T) {
	var closedRequests int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != "closed" {
			_ = json.NewEncoder(w).Encode([]*github.PullRequest{})
			return
		}
		// Recently merged PRs must sort near the front even when created long
		// ago, or the page cap would hide them.
		if got := q.Get("sort"); got != "updated" {
			t.Errorf("expected sort=updated, got %q", got)
		}
		if got := q.Get("direction"); got != "desc" {
			t.Errorf("expected direction=desc, got %q", got)
		}
		closedRequests++
		// Endless pages of non-matching closed PRs
		w.Header().Set("Link", `<`+r.URL.Path+`?page=`+fmt.Sprint(closedRequests+1)+`>; rel="next"`)
		prs := []*github.PullRequest{
			{
				Number: new(closedRequests),
				State:  new("closed"),
				Head:   &github.PullRequestBranch{Ref: new("some/other-branch")},
			},
		}
		_ = json.NewEncoder(w).Encode(prs)
	})

	gh := setupTestClient(t, mux)
	// Empty headOwner: no fallback retry, so the closed scan runs exactly once.
	prs, err := gh.ListPRsByHead(context.Background(), "owner", "repo", "", "ai/issue-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prs) != 0 {
		t.Fatalf("expected no matching PRs, got %d", len(prs))
	}
	if closedRequests != maxClosedPRPages {
		t.Errorf("expected closed scan to stop at %d pages, got %d", maxClosedPRPages, closedRequests)
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
						"number":       99,
						"state":        "open",
						"pull_request": map[string]any{"url": "https://api.github.com/repos/owner/repo/pulls/99"},
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
						"number":       99,
						"state":        "closed",
						"pull_request": map[string]any{"url": "https://api.github.com/repos/owner/repo/pulls/99"},
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

func TestHasLinkedPR_PaginatesTimeline(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues/42/timeline", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("expected per_page=100, got %q", got)
		}
		if page := r.URL.Query().Get("page"); page == "" || page == "1" {
			// Page 1: only non-PR activity (labels, comments, ...)
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			events := []map[string]any{
				{"event": "labeled"},
				{"event": "commented"},
				{"event": "assigned"},
			}
			_ = json.NewEncoder(w).Encode(events)
			return
		}
		// Page 2: the cross-referenced open PR
		events := []map[string]any{
			{
				"event": "cross-referenced",
				"source": map[string]any{
					"type": "issue",
					"issue": map[string]any{
						"number":       99,
						"state":        "open",
						"pull_request": map[string]any{"url": "https://api.github.com/repos/owner/repo/pulls/99"},
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
		t.Error("expected HasLinkedPR to find the cross-referenced PR on page 2")
	}
}

func TestGetAuthenticatedUser_WithNameAndEmail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/user", func(w http.ResponseWriter, r *http.Request) {
		user := &github.User{
			Login: new("jdoe"),
			Name:  new("Jane Doe"),
			Email: new("jane@example.com"),
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
			Login: new("jdoe"),
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
			Number: new(123),
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
			TargetCommitish: new("abc123def456"),
			TagName:         new("latest"),
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
					"number": 42,
					"title":  "Flaky CI: integration-tests",
					"body":   "Test failure",
					"labels": []map[string]any{{"name": "flaky-test"}},
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

func TestGetCommitStatuses_ReturnsFailures(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/commits/abc123/status", func(w http.ResponseWriter, r *http.Request) {
		result := map[string]any{
			"state": "failure",
			"statuses": []map[string]any{
				{
					"context":     "pull-kubernetes-nmstate-unit-test",
					"state":       "failure",
					"description": "Build failed.",
					"target_url":  "https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/logs/pull-kubernetes-nmstate-unit-test/1234",
				},
				{
					"context":    "pull-kubernetes-nmstate-e2e",
					"state":      "success",
					"target_url": "https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/logs/pull-kubernetes-nmstate-e2e/1235",
				},
				{
					"context":    "pull-kubernetes-nmstate-lint",
					"state":      "error",
					"target_url": "https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/logs/pull-kubernetes-nmstate-lint/1236",
				},
				{
					"context":    "pull-kubernetes-nmstate-build",
					"state":      "pending",
					"target_url": "https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/logs/pull-kubernetes-nmstate-build/1237",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	gh := setupTestClient(t, mux)
	runs, err := gh.GetCommitStatuses(context.Background(), "owner", "repo", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should only return failure and error states (not success or pending)
	if len(runs) != 2 {
		t.Fatalf("expected 2 failed statuses, got %d", len(runs))
	}
	if runs[0].Name != "pull-kubernetes-nmstate-unit-test" {
		t.Errorf("expected name 'pull-kubernetes-nmstate-unit-test', got %q", runs[0].Name)
	}
	if runs[0].Status != "completed" {
		t.Errorf("expected status 'completed', got %q", runs[0].Status)
	}
	if runs[0].Conclusion != "failure" {
		t.Errorf("expected conclusion 'failure', got %q", runs[0].Conclusion)
	}
	// Output should include description + target_url
	expectedOutput := "Build failed.\nhttps://prow.ci.kubevirt.io/view/gs/kubevirt-prow/logs/pull-kubernetes-nmstate-unit-test/1234"
	if runs[0].Output != expectedOutput {
		t.Errorf("expected output with description and URL, got %q", runs[0].Output)
	}
	if runs[1].Name != "pull-kubernetes-nmstate-lint" {
		t.Errorf("expected name 'pull-kubernetes-nmstate-lint', got %q", runs[1].Name)
	}
	// Second entry has no description, should only have target_url
	if runs[1].Output != "https://prow.ci.kubevirt.io/view/gs/kubevirt-prow/logs/pull-kubernetes-nmstate-lint/1236" {
		t.Errorf("expected output with URL only (no description), got %q", runs[1].Output)
	}
}

func TestGetCommitStatuses_NoFailures(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/commits/abc123/status", func(w http.ResponseWriter, r *http.Request) {
		result := map[string]any{
			"state": "success",
			"statuses": []map[string]any{
				{
					"context":    "ci/test",
					"state":      "success",
					"target_url": "https://example.com/logs",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	gh := setupTestClient(t, mux)
	runs, err := gh.GetCommitStatuses(context.Background(), "owner", "repo", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 failed statuses, got %d", len(runs))
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

func TestCountCommitsSince(t *testing.T) {
	mux := http.NewServeMux()
	var receivedSince string
	mux.HandleFunc("/api/v3/repos/owner/repo/commits", func(w http.ResponseWriter, r *http.Request) {
		receivedSince = r.URL.Query().Get("since")
		commits := []map[string]any{
			{"sha": "abc123", "commit": map[string]any{"message": "commit 1"}},
			{"sha": "def456", "commit": map[string]any{"message": "commit 2"}},
			{"sha": "ghi789", "commit": map[string]any{"message": "commit 3"}},
		}
		_ = json.NewEncoder(w).Encode(commits)
	})

	gh := setupTestClient(t, mux)
	since := time.Now().Add(-2 * time.Hour)
	count, err := gh.CountCommitsSince(context.Background(), "owner", "repo", since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 commits, got %d", count)
	}
	if receivedSince == "" {
		t.Error("expected 'since' query parameter to be set")
	}
}

func TestCountCommitsSince_ShortCircuitsAboveThreshold(t *testing.T) {
	// When the first page returns more commits than the quiet threshold,
	// CountCommitsSince should short-circuit and not fetch additional pages.
	var pagesRequested int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/commits", func(w http.ResponseWriter, r *http.Request) {
		pagesRequested++
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=2>; rel="next"`, r.URL.Path))
			commits := make([]map[string]any, 10)
			for i := range commits {
				commits[i] = map[string]any{
					"sha":    fmt.Sprintf("sha-page1-%d", i),
					"commit": map[string]any{"message": fmt.Sprintf("commit %d", i)},
				}
			}
			_ = json.NewEncoder(w).Encode(commits)
		} else {
			commits := []map[string]any{
				{"sha": "sha-page2-0", "commit": map[string]any{"message": "commit extra"}},
			}
			_ = json.NewEncoder(w).Encode(commits)
		}
	})

	gh := setupTestClient(t, mux)
	since := time.Now().Add(-2 * time.Hour)
	count, err := gh.CountCommitsSince(context.Background(), "owner", "repo", since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return 10 (first page) and short-circuit without fetching page 2
	if count != 10 {
		t.Errorf("expected 10 commits (short-circuited after first page), got %d", count)
	}
	if pagesRequested != 1 {
		t.Errorf("expected 1 page requested (short-circuit), got %d", pagesRequested)
	}
}

func TestCountCommitsSince_NoCommits(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/commits", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})

	gh := setupTestClient(t, mux)
	since := time.Now().Add(-2 * time.Hour)
	count, err := gh.CountCommitsSince(context.Background(), "owner", "repo", since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 commits, got %d", count)
	}
}

func TestCountCommitsSince_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/commits", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	gh := setupTestClient(t, mux)
	since := time.Now().Add(-2 * time.Hour)
	_, err := gh.CountCommitsSince(context.Background(), "owner", "repo", since)
	if err == nil {
		t.Fatal("expected error for API failure")
	}
}

func TestNewGoGitHubClient_EnvOverridesBaseURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		issues := []*github.Issue{
			{Number: new(1), Title: new("test")},
		}
		_ = json.NewEncoder(w).Encode(issues)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	t.Setenv("OOMPA_GITHUB_API_URL", server.URL+"/")
	gh, err := NewRESTClient("test-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	issues, err := gh.ListLabeledIssues(context.Background(), "owner", "repo", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Number != 1 {
		t.Errorf("expected issue 1, got %d", issues[0].Number)
	}
}

func TestAddIssueCommentReaction(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues/comments/42/reactions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      1,
			"content": "eyes",
		})
	})

	gh := setupTestClient(t, mux)
	err := gh.AddIssueCommentReaction(context.Background(), "owner", "repo", 42, "eyes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// The *_Paginates tests below cover the resp.NextPage loops added for issue
// #281: every list endpoint the agent reads must aggregate results across
// pages instead of silently dropping everything after the first.
func TestListLabeledIssues_Paginates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Fatalf("expected per_page=100, got %q", got)
		}
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode([]*github.Issue{
				{Number: new(1), Title: new("first"), Labels: []*github.Label{{Name: new("good-for-ai")}}},
			})
		} else {
			_ = json.NewEncoder(w).Encode([]*github.Issue{
				{Number: new(2), Title: new("second"), Labels: []*github.Label{{Name: new("good-for-ai")}}},
			})
		}
	})

	gh := setupTestClient(t, mux)
	issues, err := gh.ListLabeledIssues(context.Background(), "owner", "repo", "good-for-ai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 || issues[0].Number != 1 || issues[1].Number != 2 {
		t.Fatalf("expected issues [1 2] across pages, got %+v", issues)
	}
}

func TestGetCheckRuns_Paginates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/commits/abc123/check-runs", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode(&github.ListCheckRunsResults{
				Total:     new(int(2)),
				CheckRuns: []*github.CheckRun{{ID: new(int64(1)), Name: new("test-a"), Status: new("completed"), Conclusion: new("failure")}},
			})
		} else {
			_ = json.NewEncoder(w).Encode(&github.ListCheckRunsResults{
				Total:     new(int(2)),
				CheckRuns: []*github.CheckRun{{ID: new(int64(2)), Name: new("test-b"), Status: new("completed"), Conclusion: new("success")}},
			})
		}
	})

	gh := setupTestClient(t, mux)
	runs, err := gh.GetCheckRuns(context.Background(), "owner", "repo", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != 2 || runs[0].Name != "test-a" || runs[1].Name != "test-b" {
		t.Fatalf("expected check runs [test-a test-b] across pages, got %+v", runs)
	}
}

func TestGetPRReviews_Paginates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode([]*github.PullRequestReview{
				{ID: new(int64(10)), User: &github.User{Login: new("alice")}, State: new("CHANGES_REQUESTED"), Body: new("fix this")},
			})
		} else {
			_ = json.NewEncoder(w).Encode([]*github.PullRequestReview{
				{ID: new(int64(20)), User: &github.User{Login: new("bob")}, State: new("COMMENTED"), Body: new("and this")},
			})
		}
	})

	gh := setupTestClient(t, mux)
	reviews, err := gh.GetPRReviews(context.Background(), "owner", "repo", 7, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reviews) != 2 || reviews[0].ID != 10 || reviews[1].ID != 20 {
		t.Fatalf("expected reviews [10 20] across pages, got %+v", reviews)
	}
}

func TestListWorkflowJobs_Paginates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/actions/runs/99/jobs", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode(&github.Jobs{
				TotalCount: new(int(2)),
				Jobs:       []*github.WorkflowJob{{ID: new(int64(1)), Name: new("build"), Conclusion: new("failure")}},
			})
		} else {
			_ = json.NewEncoder(w).Encode(&github.Jobs{
				TotalCount: new(int(2)),
				Jobs:       []*github.WorkflowJob{{ID: new(int64(2)), Name: new("test"), Conclusion: new("success")}},
			})
		}
	})

	gh := setupTestClient(t, mux)
	jobs, err := gh.ListWorkflowJobs(context.Background(), "owner", "repo", 99)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jobs) != 2 || jobs[0].Name != "build" || jobs[1].Name != "test" {
		t.Fatalf("expected jobs [build test] across pages, got %+v", jobs)
	}
}

func TestHasPRCommentReaction_FindsReactionOnLaterPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/pulls/comments/55/reactions", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode([]*github.Reaction{
				{Content: new("+1"), User: &github.User{Login: new("alice")}},
			})
		} else {
			_ = json.NewEncoder(w).Encode([]*github.Reaction{
				{Content: new("eyes"), User: &github.User{Login: new("test-bot")}},
			})
		}
	})

	gh := setupTestClient(t, mux)
	found, err := gh.HasPRCommentReaction(context.Background(), "owner", "repo", 55, "eyes", "test-bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected reaction on page 2 to be found")
	}
}

// TestTailBuffer covers the bounded log reader added for issue #282: only
// the last maxBytes of a stream are retained regardless of input size or
// write chunking, with the truncation marker and rune-boundary handling of
// the former whole-string truncation preserved.
func TestTailBuffer(t *testing.T) {
	tests := []struct {
		name   string
		max    int
		writes []string
		want   string
	}{
		{
			name:   "under capacity returns input unchanged",
			max:    10,
			writes: []string{"hello"},
			want:   "hello",
		},
		{
			name:   "exactly at capacity returns input unchanged",
			max:    5,
			writes: []string{"hello"},
			want:   "hello",
		},
		{
			name:   "single oversized write keeps the tail",
			max:    4,
			writes: []string{"0123456789"},
			want:   "...(truncated)...\n6789",
		},
		{
			name:   "sliding window across many small writes",
			max:    6,
			writes: []string{"aa", "bb", "cc", "dd", "ee"},
			want:   "...(truncated)...\nccddee",
		},
		{
			name:   "write larger than remaining space slides partially",
			max:    8,
			writes: []string{"abcd", "efghij"},
			want:   "...(truncated)...\ncdefghij",
		},
		{
			name: "cut advances past a split multibyte rune",
			max:  4,
			// "xhéllo" ends in é(2 bytes) l l o — keeping the last 4 bytes
			// cuts é in half; the boundary walk drops the orphaned
			// continuation byte.
			writes: []string{"xhéllo"},
			want:   "...(truncated)...\nllo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb := newTailBuffer(tt.max)
			for _, w := range tt.writes {
				n, err := tb.Write([]byte(w))
				if err != nil || n != len(w) {
					t.Fatalf("Write(%q) = (%d, %v), want (%d, nil)", w, n, err, len(w))
				}
			}
			if got := tb.tailString(); got != tt.want {
				t.Errorf("tailString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetCheckRunLog_BoundsLargeLogs(t *testing.T) {
	// Serve a log much larger than maxCILogBytes in multiple chunks and
	// verify only the tail is returned, with the truncation marker.
	// The API endpoint answers 302 with a pre-signed blob-storage URL, as
	// GitHub does; the client extracts that URL via go-github and fetches
	// the content itself.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/actions/jobs/7/logs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/blob/logs", http.StatusFound)
	})
	mux.HandleFunc("/blob/logs", func(w http.ResponseWriter, r *http.Request) {
		for i := range 3 {
			line := strings.Repeat(fmt.Sprintf("chunk%d ", i), maxCILogBytes/7)
			_, _ = w.Write([]byte(line))
		}
		_, _ = w.Write([]byte("THE END"))
	})

	gh := setupTestClient(t, mux)
	log, err := gh.GetCheckRunLog(context.Background(), "owner", "repo", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(log, "...(truncated)...\n") {
		t.Errorf("expected truncation marker prefix, got %q", log[:min(40, len(log))])
	}
	if !strings.HasSuffix(log, "THE END") {
		t.Errorf("expected log to end with the stream tail, got %q", log[max(0, len(log)-40):])
	}
	if len(log) > maxCILogBytes+len("...(truncated)...\n") {
		t.Errorf("log length %d exceeds bound %d", len(log), maxCILogBytes+len("...(truncated)...\n"))
	}
}

func TestGetCheckRunLog_RejectsExpiredStorageURL(t *testing.T) {
	// A pre-signed storage URL that has expired answers 403 with an XML
	// error document — that must surface as an error, not as log content.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/owner/repo/actions/jobs/8/logs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/blob/expired", http.StatusFound)
	})
	mux.HandleFunc("/blob/expired", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>AuthenticationFailed</Code></Error>"))
	})

	gh := setupTestClient(t, mux)
	_, err := gh.GetCheckRunLog(context.Background(), "owner", "repo", 8)
	if err == nil {
		t.Fatal("expected error for expired storage URL")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected error to mention the status, got: %v", err)
	}
}
