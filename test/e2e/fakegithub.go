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
	Number         int            `json:"number"`
	Title          string         `json:"title"`
	Body           string         `json:"body"`
	State          string         `json:"state"`
	Merged         bool           `json:"merged"`
	Head           map[string]any `json:"head"`
	Base           map[string]any `json:"base"`
	MergeableState string         `json:"mergeable_state,omitempty"`
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

// FakeReviewComment represents an inline review comment on a PR.
type FakeReviewComment struct {
	ID          int64          `json:"id"`
	InReplyToID int64          `json:"in_reply_to_id,omitempty"`
	Body        string         `json:"body"`
	Path        string         `json:"path"`
	Line        int            `json:"line"`
	User        map[string]any `json:"user"`
}

// FakeCheckRun represents a GitHub check run.
type FakeCheckRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Output     struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
		Text    string `json:"text"`
	} `json:"output"`
	HTMLURL     string `json:"html_url"`
	CompletedAt string `json:"completed_at,omitempty"`
}

// FakeReview represents a GitHub PR review.
type FakeReview struct {
	ID          int64          `json:"id"`
	User        map[string]any `json:"user"`
	State       string         `json:"state"`
	Body        string         `json:"body"`
	SubmittedAt string         `json:"submitted_at"`
}

// FakeGitHub is a stateful in-memory GitHub API mock backed by httptest.
type FakeGitHub struct {
	mu     sync.Mutex
	t      *testing.T
	server *httptest.Server

	// State
	issues          map[int]*FakeIssue           // issue number -> issue
	comments        map[int][]*FakeComment       // issue number -> comments
	prs             map[int]*FakePR              // PR number -> PR
	reviewComments  map[int][]*FakeReviewComment // PR number -> inline review comments
	checkRuns       map[string][]*FakeCheckRun   // SHA -> check runs
	reviews         map[int][]*FakeReview        // PR number -> reviews
	prHeadSHAs      map[int]string               // PR number -> HEAD SHA override
	prMergeStates   map[int]string               // PR number -> mergeable_state
	commitStatuses  map[string][]map[string]any  // SHA -> commit statuses
	reactions       map[int64][]map[string]any   // commentID -> reactions
	nextCommentID   int64
	nextPRNumber    int
	nextReviewComID int64
	nextIssueNumber int

	// Deferred items: only become visible after the first GET on the same resource.
	// This simulates comments arriving after state recovery (first GET) but before
	// ProcessReviewComments (second GET), which is the normal production scenario.
	deferredReviewComments map[int][]*FakeReviewComment // PR number -> deferred review comments
	deferredIssueComments  map[int][]*FakeComment       // issue number -> deferred issue comments
	reviewCommentsGets     map[int]int                  // PR number -> GET request count
	issueCommentsGets      map[int]int                  // issue number -> GET request count

	// Recorders for assertions
	CreatePRCalls    []CreatePRCall
	CreateIssueCalls []CreateIssueCall
	AssignCalls      []AssignCall
	UnassignCalls    []AssignCall
	CommentCalls     []FakeComment     // all comments posted via API
	ReviewReplyCalls []ReviewReplyCall // replies to PR review threads
	ReactionCalls    []ReactionCall    // reactions added
	SearchIssueCalls []string          // search queries
}

// CreateIssueCall records the arguments of a CreateIssue API call.
type CreateIssueCall struct {
	Title  string
	Body   string
	Labels []string
}

// ReviewReplyCall records a reply to a PR review comment.
type ReviewReplyCall struct {
	PRNumber  int
	CommentID int64
	Body      string
}

// ReactionCall records a reaction added.
type ReactionCall struct {
	CommentID int64
	Reaction  string
}

// NewFakeGitHub creates a new stateful fake GitHub server.
func NewFakeGitHub(t *testing.T, owner, repo string) *FakeGitHub {
	fg := &FakeGitHub{
		t:                      t,
		issues:                 make(map[int]*FakeIssue),
		comments:               make(map[int][]*FakeComment),
		prs:                    make(map[int]*FakePR),
		reviewComments:         make(map[int][]*FakeReviewComment),
		checkRuns:              make(map[string][]*FakeCheckRun),
		reviews:                make(map[int][]*FakeReview),
		prHeadSHAs:             make(map[int]string),
		prMergeStates:          make(map[int]string),
		commitStatuses:         make(map[string][]map[string]any),
		reactions:              make(map[int64][]map[string]any),
		nextCommentID:          1,
		nextPRNumber:           100,
		nextReviewComID:        1,
		nextIssueNumber:        900,
		deferredReviewComments: make(map[int][]*FakeReviewComment),
		deferredIssueComments:  make(map[int][]*FakeComment),
		reviewCommentsGets:     make(map[int]int),
		issueCommentsGets:      make(map[int]int),
	}

	prefix := fmt.Sprintf("/api/v3/repos/%s/%s", owner, repo)
	mux := http.NewServeMux()

	// GET /repos/{o}/{r}/issues - list issues with label filtering
	mux.HandleFunc(prefix+"/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			fg.handleCreateIssue(w, r)
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

	// Handle /repos/{o}/{r}/pulls/{n} and sub-resources
	mux.HandleFunc(prefix+"/pulls/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, prefix+"/pulls/")
		parts := strings.SplitN(path, "/", 2)
		prNum, err := strconv.Atoi(parts[0])
		if err != nil {
			http.Error(w, "bad PR number", http.StatusBadRequest)
			return
		}

		if len(parts) < 2 {
			// GET /repos/{o}/{r}/pulls/{n} - get single PR
			fg.mu.Lock()
			defer fg.mu.Unlock()
			if pr, ok := fg.prs[prNum]; ok {
				_ = json.NewEncoder(w).Encode(fg.prToJSON(pr))
			} else {
				http.NotFound(w, r)
			}
			return
		}

		subResource := parts[1]
		switch subResource {
		case "comments":
			fg.handlePRReviewComments(w, r, prNum)
		case "reviews":
			fg.handlePRReviews(w, r, prNum)
		case "commits":
			fg.handlePRCommits(w, r, prNum)
		default:
			log.Printf("[FakeGitHub] unhandled PR sub-resource: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	// GET /repos/{o}/{r}/commits - list commits (for CountCommitsSince)
	mux.HandleFunc(prefix+"/commits", func(w http.ResponseWriter, r *http.Request) {
		// Return empty list (no recent commits = quiet main branch)
		fg.mu.Lock()
		defer fg.mu.Unlock()
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})

	// GET /repos/{o}/{r}/commits/{sha}/check-runs or /commits/{sha}/status or /commits/{sha}
	mux.HandleFunc(prefix+"/commits/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, prefix+"/commits/")
		parts := strings.SplitN(path, "/", 2)
		sha := parts[0]
		if len(parts) >= 2 && parts[1] == "check-runs" {
			fg.handleCheckRuns(w, r, sha)
			return
		}
		if len(parts) >= 2 && parts[1] == "status" {
			fg.handleCommitStatuses(w, r, sha)
			return
		}
		// GET /repos/{o}/{r}/commits/{sha} - single commit
		fg.mu.Lock()
		defer fg.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha": sha,
			"commit": map[string]any{
				"committer": map[string]any{
					"date": "2025-01-01T00:00:00Z",
				},
			},
		})
	})

	// GET /repos/{o}/{r}/compare/{base}...{head}
	mux.HandleFunc(prefix+"/compare/", func(w http.ResponseWriter, r *http.Request) {
		fg.mu.Lock()
		defer fg.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ahead_by":  0,
			"behind_by": 0,
			"status":    "identical",
		})
	})

	// GET /api/v3/search/issues
	mux.HandleFunc("/api/v3/search/issues", func(w http.ResponseWriter, r *http.Request) {
		fg.mu.Lock()
		defer fg.mu.Unlock()
		query := r.URL.Query().Get("q")
		fg.SearchIssueCalls = append(fg.SearchIssueCalls, query)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": 0,
			"items":       []any{},
		})
	})

	// Handle reactions on PR comments: /repos/{o}/{r}/pulls/comments/{id}/reactions
	mux.HandleFunc(prefix+"/pulls/comments/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, prefix+"/pulls/comments/")
		parts := strings.SplitN(path, "/", 2)
		commentID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			http.Error(w, "bad comment ID", http.StatusBadRequest)
			return
		}
		// GET or POST .../reactions
		if len(parts) >= 2 && parts[1] == "reactions" {
			fg.mu.Lock()
			defer fg.mu.Unlock()
			if r.Method == "POST" {
				var body struct {
					Content string `json:"content"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "bad body", http.StatusBadRequest)
					return
				}
				fg.ReactionCalls = append(fg.ReactionCalls, ReactionCall{
					CommentID: commentID,
					Reaction:  body.Content,
				})
				fg.reactions[commentID] = append(fg.reactions[commentID], map[string]any{
					"id":      int64(len(fg.reactions[commentID]) + 1),
					"content": body.Content,
					"user":    map[string]any{"login": "oompa-bot"},
				})
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":      1,
					"content": body.Content,
				})
				return
			}
			// GET reactions - return persisted reactions for this comment
			_ = json.NewEncoder(w).Encode(fg.reactions[commentID])
			return
		}
		http.NotFound(w, r)
	})

	// Handle reactions on issue comments: /repos/{o}/{r}/issues/comments/{id}/reactions
	mux.HandleFunc(prefix+"/issues/comments/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, prefix+"/issues/comments/")
		parts := strings.SplitN(path, "/", 2)
		commentID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			http.Error(w, "bad comment ID", http.StatusBadRequest)
			return
		}
		if len(parts) >= 2 && parts[1] == "reactions" {
			fg.mu.Lock()
			defer fg.mu.Unlock()
			if r.Method == "POST" {
				var body struct {
					Content string `json:"content"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "bad body", http.StatusBadRequest)
					return
				}
				fg.ReactionCalls = append(fg.ReactionCalls, ReactionCall{
					CommentID: commentID,
					Reaction:  body.Content,
				})
				fg.reactions[commentID] = append(fg.reactions[commentID], map[string]any{
					"id":      int64(len(fg.reactions[commentID]) + 1),
					"content": body.Content,
					"user":    map[string]any{"login": "oompa-bot"},
				})
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":      1,
					"content": body.Content,
				})
				return
			}
			// GET reactions - return persisted reactions for this comment
			_ = json.NewEncoder(w).Encode(fg.reactions[commentID])
			return
		}
		http.NotFound(w, r)
	})

	// Handle check-runs/{id} for log fetching
	mux.HandleFunc(prefix+"/check-runs/", func(w http.ResponseWriter, r *http.Request) {
		// Return empty log for check run log requests
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(""))
	})

	// Handle actions/jobs/{id}/logs for GetCheckRunLog
	// go-github expects a 302 redirect to the actual log content.
	mux.HandleFunc(prefix+"/actions/jobs/", func(w http.ResponseWriter, r *http.Request) {
		// Return 302 redirect to a dummy log URL
		http.Redirect(w, r, fg.server.URL+"/fake-logs", http.StatusFound)
	})

	// Serve fake log content for redirected log requests
	mux.HandleFunc("/fake-logs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("(no logs available)"))
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

	// Return 404 if neither an issue nor a PR exists for this number.
	// In GitHub's API, PRs are also issues and share the same comment endpoint.
	if _, ok := fg.issues[issueNum]; !ok {
		if _, ok := fg.prs[issueNum]; !ok {
			http.NotFound(w, r)
			return
		}
	}

	switch r.Method {
	case "GET":
		// Promote deferred issue comments after the first GET (state recovery).
		fg.issueCommentsGets[issueNum]++
		if fg.issueCommentsGets[issueNum] > 1 {
			if deferred, ok := fg.deferredIssueComments[issueNum]; ok && len(deferred) > 0 {
				fg.comments[issueNum] = append(fg.comments[issueNum], deferred...)
				delete(fg.deferredIssueComments, issueNum)
			}
		}

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
	stateFilter := r.URL.Query().Get("state")
	var result []fakePRJSON
	for _, pr := range fg.prs {
		switch stateFilter {
		case "", "all":
			// no state filtering
		case "open":
			if pr.State != "open" {
				continue
			}
		default: // "closed" (merged PRs are closed too)
			if pr.State == "open" {
				continue
			}
		}
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

// SeedPR adds a PR to the fake server's state.
func (fg *FakeGitHub) SeedPR(pr FakePR) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	if pr.State == "" {
		pr.State = "open"
	}
	fg.prs[pr.Number] = &pr
}

// SeedReviewComment adds an inline review comment to a PR.
func (fg *FakeGitHub) SeedReviewComment(prNumber int, comment FakeReviewComment) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	if comment.ID == 0 {
		comment.ID = fg.nextReviewComID
		fg.nextReviewComID++
	} else if comment.ID >= fg.nextReviewComID {
		fg.nextReviewComID = comment.ID + 1
	}
	if comment.User == nil {
		comment.User = map[string]any{"login": "reviewer"}
	}
	fg.reviewComments[prNumber] = append(fg.reviewComments[prNumber], &comment)
}

// SeedReviewCommentDeferred adds an inline review comment that only becomes
// visible after the first GET on the PR's review comments endpoint. This
// simulates a comment arriving after state recovery but before the poll loop.
func (fg *FakeGitHub) SeedReviewCommentDeferred(prNumber int, comment FakeReviewComment) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	if comment.ID == 0 {
		comment.ID = fg.nextReviewComID
		fg.nextReviewComID++
	} else if comment.ID >= fg.nextReviewComID {
		fg.nextReviewComID = comment.ID + 1
	}
	if comment.User == nil {
		comment.User = map[string]any{"login": "reviewer"}
	}
	fg.deferredReviewComments[prNumber] = append(fg.deferredReviewComments[prNumber], &comment)
}

// SeedIssueCommentDeferred adds an issue comment that only becomes visible
// after the first GET on the issue's comments endpoint. This simulates a
// comment arriving after state recovery but before the poll loop.
func (fg *FakeGitHub) SeedIssueCommentDeferred(issueNumber int, comment FakeComment) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	if comment.ID == 0 {
		comment.ID = fg.nextCommentID
		fg.nextCommentID++
	} else if comment.ID >= fg.nextCommentID {
		fg.nextCommentID = comment.ID + 1
	}
	if comment.User == nil {
		comment.User = map[string]any{"login": "reviewer"}
	}
	fg.deferredIssueComments[issueNumber] = append(fg.deferredIssueComments[issueNumber], &comment)
}

// SeedCheckRun adds a check run for a commit SHA.
func (fg *FakeGitHub) SeedCheckRun(sha string, cr FakeCheckRun) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	fg.checkRuns[sha] = append(fg.checkRuns[sha], &cr)
}

// SeedReview adds a PR review.
func (fg *FakeGitHub) SeedReview(prNumber int, review FakeReview) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	fg.reviews[prNumber] = append(fg.reviews[prNumber], &review)
}

// SetPRHeadSHA overrides the HEAD SHA for a PR.
func (fg *FakeGitHub) SetPRHeadSHA(prNumber int, sha string) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	fg.prHeadSHAs[prNumber] = sha
}

// SetPRMergeState sets the mergeable_state for a PR.
func (fg *FakeGitHub) SetPRMergeState(prNumber int, state string) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	fg.prMergeStates[prNumber] = state
}

// SeedIssueComment adds a comment to an issue.
func (fg *FakeGitHub) SeedIssueComment(issueNumber int, comment FakeComment) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	if comment.ID == 0 {
		comment.ID = fg.nextCommentID
		fg.nextCommentID++
	} else if comment.ID >= fg.nextCommentID {
		fg.nextCommentID = comment.ID + 1
	}
	if comment.User == nil {
		comment.User = map[string]any{"login": "reviewer"}
	}
	fg.comments[issueNumber] = append(fg.comments[issueNumber], &comment)
}

func (fg *FakeGitHub) handlePRReviewComments(w http.ResponseWriter, r *http.Request, prNum int) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	switch r.Method {
	case "POST":
		// Reply to a review comment
		var body struct {
			Body      string `json:"body"`
			InReplyTo int64  `json:"in_reply_to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		reply := &FakeReviewComment{
			ID:          fg.nextReviewComID,
			InReplyToID: body.InReplyTo,
			Body:        body.Body,
			User:        map[string]any{"login": "oompa-bot"},
		}
		fg.nextReviewComID++
		fg.reviewComments[prNum] = append(fg.reviewComments[prNum], reply)
		fg.ReviewReplyCalls = append(fg.ReviewReplyCalls, ReviewReplyCall{
			PRNumber:  prNum,
			CommentID: body.InReplyTo,
			Body:      body.Body,
		})
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(reply)
	case "GET":
		// Promote deferred review comments after the first GET (state recovery).
		// Page 1 counts as the initial GET; subsequent page requests within the
		// same logical listing are not counted again.
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		if page < 1 {
			page = 1
		}
		if page == 1 {
			fg.reviewCommentsGets[prNum]++
			if fg.reviewCommentsGets[prNum] > 1 {
				if deferred, ok := fg.deferredReviewComments[prNum]; ok && len(deferred) > 0 {
					fg.reviewComments[prNum] = append(fg.reviewComments[prNum], deferred...)
					delete(fg.deferredReviewComments, prNum)
				}
			}
		}

		// Return review comments with pagination support.
		comments := fg.reviewComments[prNum]
		if comments == nil {
			comments = []*FakeReviewComment{}
		}
		perPage, _ := strconv.Atoi(q.Get("per_page"))
		if perPage <= 0 {
			perPage = 30
		}
		start := min((page-1)*perPage, len(comments))
		end := min(start+perPage, len(comments))
		// Set Link header for next page if more results exist
		if end < len(comments) {
			nextURL := fmt.Sprintf("<%s?page=%d&per_page=%d>; rel=\"next\"",
				r.URL.Path, page+1, perPage)
			w.Header().Set("Link", nextURL)
		}
		_ = json.NewEncoder(w).Encode(comments[start:end])
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (fg *FakeGitHub) handlePRReviews(w http.ResponseWriter, r *http.Request, prNum int) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	switch r.Method {
	case "POST":
		// Submit a review (not needed for e2e, but return success)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	case "GET":
		reviews := fg.reviews[prNum]
		if reviews == nil {
			reviews = []*FakeReview{}
		}
		_ = json.NewEncoder(w).Encode(reviews)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (fg *FakeGitHub) handlePRCommits(w http.ResponseWriter, _ *http.Request, prNum int) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	sha := "fake-sha-" + strconv.Itoa(prNum)
	if override, ok := fg.prHeadSHAs[prNum]; ok {
		sha = override
	}
	_ = json.NewEncoder(w).Encode([]map[string]any{
		{
			"sha": sha,
			"commit": map[string]any{
				"message": "initial commit",
			},
		},
	})
}

func (fg *FakeGitHub) handleCheckRuns(w http.ResponseWriter, _ *http.Request, sha string) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	runs := fg.checkRuns[sha]
	if runs == nil {
		runs = []*FakeCheckRun{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"total_count": len(runs),
		"check_runs":  runs,
	})
}

func (fg *FakeGitHub) handleCommitStatuses(w http.ResponseWriter, _ *http.Request, sha string) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	statuses := fg.commitStatuses[sha]
	if statuses == nil {
		statuses = []map[string]any{}
	}
	// Return "success" when no statuses are seeded to avoid masking future bugs
	// if production code ever checks the combined state field.
	combinedState := "success"
	if len(statuses) > 0 {
		combinedState = "failure"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"state":    combinedState,
		"statuses": statuses,
	})
}

func (fg *FakeGitHub) handleCreateIssue(w http.ResponseWriter, r *http.Request) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	var body struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	issueNum := fg.nextIssueNumber
	fg.nextIssueNumber++

	// Persist the new issue into fake state so subsequent list/get flows see it.
	labels := make([]map[string]any, 0, len(body.Labels))
	for _, l := range body.Labels {
		labels = append(labels, map[string]any{"name": l})
	}
	fg.issues[issueNum] = &FakeIssue{
		Number: issueNum,
		Title:  body.Title,
		Body:   body.Body,
		State:  "open",
		Labels: labels,
	}

	fg.CreateIssueCalls = append(fg.CreateIssueCalls, CreateIssueCall{
		Title:  body.Title,
		Body:   body.Body,
		Labels: body.Labels,
	})

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"number": issueNum,
		"title":  body.Title,
		"body":   body.Body,
	})
}

// prToJSON converts a FakePR to the JSON structure go-github expects.
func (fg *FakeGitHub) prToJSON(pr *FakePR) fakePRJSON {
	sha := "fake-sha-" + strconv.Itoa(pr.Number)
	if override, ok := fg.prHeadSHAs[pr.Number]; ok {
		sha = override
	}
	mergeableState := "clean"
	if ms, ok := fg.prMergeStates[pr.Number]; ok {
		mergeableState = ms
	}
	return fakePRJSON{
		Number: pr.Number,
		Title:  pr.Title,
		Body:   pr.Body,
		State:  pr.State,
		Merged: false,
		Head: map[string]any{
			"ref": pr.Head,
			"sha": sha,
		},
		Base: map[string]any{
			"ref": pr.Base,
		},
		MergeableState: mergeableState,
	}
}
