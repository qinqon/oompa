package e2e

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// FakeIssue represents an issue in the fake GitHub server.
type FakeIssue struct {
	Number    int              `json:"number"`
	Title     string           `json:"title"`
	Body      string           `json:"body"`
	State     string           `json:"state"`
	Labels    []map[string]any `json:"labels"`
	Assignees []map[string]any `json:"assignees"`
}

// FakeComment represents an issue comment in the fake GitHub server.
type FakeComment struct {
	ID   int64          `json:"id"`
	Body string         `json:"body"`
	User map[string]any `json:"user"`
}

// FakePR represents a pull request in the fake GitHub server.
type FakePR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	Head   string `json:"head"` // head ref name for matching
	Base   string `json:"base"` // base ref name
}

// fakePRJSON is the JSON representation returned by the API.
type fakePRJSON struct {
	Number int            `json:"number"`
	Title  string         `json:"title"`
	Body   string         `json:"body"`
	State  string         `json:"state"`
	Merged bool           `json:"merged"`
	Head   map[string]any `json:"head"`
	Base   map[string]any `json:"base"`
}

// CreatePRCall records the arguments of a CreatePR API call.
type CreatePRCall struct {
	Title string
	Body  string
	Head  string
	Base  string
}

// AssignCall records the arguments of an assign/unassign API call.
type AssignCall struct {
	IssueNumber int
	Assignees   []string
}

// FakeGitHub is a stateful in-memory GitHub API mock backed by httptest.
type FakeGitHub struct {
	mu     sync.Mutex
	t      *testing.T
	server *httptest.Server

	// State
	issues        map[int]*FakeIssue     // issue number -> issue
	comments      map[int][]*FakeComment // issue number -> comments
	prs           map[int]*FakePR        // PR number -> PR
	nextCommentID int64
	nextPRNumber  int

	// Recorders for assertions
	CreatePRCalls []CreatePRCall
	AssignCalls   []AssignCall
	UnassignCalls []AssignCall
	CommentCalls  []FakeComment // all comments posted via API
}

// NewFakeGitHub creates a new stateful fake GitHub server.
func NewFakeGitHub(t *testing.T, owner, repo string) *FakeGitHub {
	fg := &FakeGitHub{
		t:             t,
		issues:        make(map[int]*FakeIssue),
		comments:      make(map[int][]*FakeComment),
		prs:           make(map[int]*FakePR),
		nextCommentID: 1,
		nextPRNumber:  100,
	}

	prefix := fmt.Sprintf("/api/v3/repos/%s/%s", owner, repo)
	mux := http.NewServeMux()

	// GET /repos/{o}/{r}/issues - list issues with label filtering
	mux.HandleFunc(prefix+"/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			// Create issue (not needed for smoke test, but handle gracefully)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 999})
			return
		}
		fg.mu.Lock()
		defer fg.mu.Unlock()

		labelFilter := r.URL.Query().Get("labels")
		var result []*FakeIssue
		for _, issue := range fg.issues {
			if issue.State != "open" {
				continue
			}
			if labelFilter != "" {
				hasLabel := false
				for _, l := range issue.Labels {
					if name, ok := l["name"].(string); ok && name == labelFilter {
						hasLabel = true
						break
					}
				}
				if !hasLabel {
					continue
				}
			}
			result = append(result, issue)
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	// GET/POST /repos/{o}/{r}/issues/{n}/comments - list/create issue comments
	mux.HandleFunc(prefix+"/issues/", func(w http.ResponseWriter, r *http.Request) {
		// Parse the path to extract issue number and sub-resource
		path := strings.TrimPrefix(r.URL.Path, prefix+"/issues/")
		parts := strings.SplitN(path, "/", 2)
		issueNum, err := strconv.Atoi(parts[0])
		if err != nil {
			http.Error(w, "bad issue number", http.StatusBadRequest)
			return
		}

		if len(parts) < 2 {
			// GET /repos/{o}/{r}/issues/{n} - get single issue (not needed but safe)
			fg.mu.Lock()
			defer fg.mu.Unlock()
			if issue, ok := fg.issues[issueNum]; ok {
				_ = json.NewEncoder(w).Encode(issue)
			} else {
				http.NotFound(w, r)
			}
			return
		}

		subResource := parts[1]

		switch subResource {
		case "comments":
			fg.handleIssueComments(w, r, issueNum)
		case "assignees":
			fg.handleAssignees(w, r, issueNum)
		case "timeline":
			fg.handleTimeline(w, r)
		case "labels":
			fg.handleLabels(w, r, issueNum)
		default:
			// Check for labels/{name} pattern
			if strings.HasPrefix(subResource, "labels/") {
				fg.handleLabels(w, r, issueNum)
			} else {
				log.Printf("[FakeGitHub] unhandled sub-resource: %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}
	})

	// GET/POST /repos/{o}/{r}/pulls - list/create PRs
	mux.HandleFunc(prefix+"/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			fg.handleCreatePR(w, r)
			return
		}
		fg.handleListPRs(w, r)
	})

	// Handle /repos/{o}/{r}/pulls/{n} for individual PR operations
	mux.HandleFunc(prefix+"/pulls/", func(w http.ResponseWriter, r *http.Request) {
		// GET /repos/{o}/{r}/pulls/{n} - get single PR
		path := strings.TrimPrefix(r.URL.Path, prefix+"/pulls/")
		parts := strings.SplitN(path, "/", 2)
		prNum, err := strconv.Atoi(parts[0])
		if err != nil {
			http.Error(w, "bad PR number", http.StatusBadRequest)
			return
		}
		fg.mu.Lock()
		defer fg.mu.Unlock()
		if pr, ok := fg.prs[prNum]; ok {
			_ = json.NewEncoder(w).Encode(fg.prToJSON(pr))
		} else {
			http.NotFound(w, r)
		}
	})

	// Default 404 handler for unmatched routes under /api/v3/
	mux.HandleFunc("/api/v3/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[FakeGitHub] UNHANDLED: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})

	// Catch-all for non-API routes
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[FakeGitHub] UNHANDLED (root): %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})

	fg.server = httptest.NewServer(mux)
	t.Cleanup(fg.server.Close)

	return fg
}

// URL returns the base URL of the fake server (with trailing slash for go-github).
func (fg *FakeGitHub) URL() string {
	return fg.server.URL + "/"
}

// SeedIssue adds an issue to the fake server's state.
func (fg *FakeGitHub) SeedIssue(issue FakeIssue) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	issue.State = "open"
	fg.issues[issue.Number] = &issue
}

func (fg *FakeGitHub) handleIssueComments(w http.ResponseWriter, r *http.Request, issueNum int) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	// Return 404 if the issue doesn't exist, matching real GitHub API behavior.
	if _, ok := fg.issues[issueNum]; !ok {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case "GET":
		comments := fg.comments[issueNum]
		if comments == nil {
			comments = []*FakeComment{}
		}
		_ = json.NewEncoder(w).Encode(comments)
	case "POST":
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		comment := &FakeComment{
			ID:   fg.nextCommentID,
			Body: body.Body,
			User: map[string]any{"login": "oompa-bot"},
		}
		fg.nextCommentID++
		fg.comments[issueNum] = append(fg.comments[issueNum], comment)
		fg.CommentCalls = append(fg.CommentCalls, *comment)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(comment)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (fg *FakeGitHub) handleAssignees(w http.ResponseWriter, r *http.Request, issueNum int) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	var body struct {
		Assignees []string `json:"assignees"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	issue, ok := fg.issues[issueNum]
	if !ok {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case "POST":
		// Add assignees
		for _, a := range body.Assignees {
			issue.Assignees = append(issue.Assignees, map[string]any{"login": a})
		}
		fg.AssignCalls = append(fg.AssignCalls, AssignCall{
			IssueNumber: issueNum,
			Assignees:   body.Assignees,
		})
		_ = json.NewEncoder(w).Encode(issue)
	case "DELETE":
		// Remove assignees
		var remaining []map[string]any
		for _, a := range issue.Assignees {
			login, _ := a["login"].(string)
			shouldRemove := slices.Contains(body.Assignees, login)
			if !shouldRemove {
				remaining = append(remaining, a)
			}
		}
		issue.Assignees = remaining
		fg.UnassignCalls = append(fg.UnassignCalls, AssignCall{
			IssueNumber: issueNum,
			Assignees:   body.Assignees,
		})
		_ = json.NewEncoder(w).Encode(issue)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (fg *FakeGitHub) handleTimeline(w http.ResponseWriter, _ *http.Request) {
	// Return empty timeline events (no linked PRs)
	_ = json.NewEncoder(w).Encode([]map[string]any{})
}

func (fg *FakeGitHub) handleLabels(w http.ResponseWriter, r *http.Request, issueNum int) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	// Return an empty JSON array for all label operations to satisfy go-github's
	// response handling consistently across methods.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode([]map[string]any{})
	_ = issueNum // Suppress unused warning — labels are acknowledged but not stored yet
}

func (fg *FakeGitHub) handleListPRs(w http.ResponseWriter, r *http.Request) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	headFilter := r.URL.Query().Get("head")
	var result []fakePRJSON
	for _, pr := range fg.prs {
		if headFilter != "" {
			// headFilter can be "owner:branch" or just "branch"
			branchName := headFilter
			if _, after, ok := strings.Cut(headFilter, ":"); ok {
				branchName = after
			}
			if pr.Head != branchName {
				continue
			}
		}
		result = append(result, fg.prToJSON(pr))
	}
	if result == nil {
		result = []fakePRJSON{}
	}
	_ = json.NewEncoder(w).Encode(result)
}

func (fg *FakeGitHub) handleCreatePR(w http.ResponseWriter, r *http.Request) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	var body struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Head  string `json:"head"`
		Base  string `json:"base"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	prNumber := fg.nextPRNumber
	fg.nextPRNumber++

	pr := &FakePR{
		Number: prNumber,
		Title:  body.Title,
		Body:   body.Body,
		State:  "open",
		Head:   body.Head,
		Base:   body.Base,
	}
	fg.prs[prNumber] = pr

	fg.CreatePRCalls = append(fg.CreatePRCalls, CreatePRCall{
		Title: body.Title,
		Body:  body.Body,
		Head:  body.Head,
		Base:  body.Base,
	})

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fg.prToJSON(pr))
}

// prToJSON converts a FakePR to the JSON structure go-github expects.
func (fg *FakeGitHub) prToJSON(pr *FakePR) fakePRJSON {
	return fakePRJSON{
		Number: pr.Number,
		Title:  pr.Title,
		Body:   pr.Body,
		State:  pr.State,
		Merged: false,
		Head: map[string]any{
			"ref": pr.Head,
			"sha": "fake-sha-" + strconv.Itoa(pr.Number),
		},
		Base: map[string]any{
			"ref": pr.Base,
		},
	}
}
