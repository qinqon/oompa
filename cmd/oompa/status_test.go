package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qinqon/oompa/pkg/agent"
)

func TestStatusCommand_Connects(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "oompa.sock")
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	server := agent.NewSocketEventServer(sockPath, 100, logger)

	ctx := t.Context()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	// Emit an event so there's something to display
	server.Emit(agent.Event{
		Type:   agent.EventWorkerStateChange,
		Worker: "test/repo",
		State:  "working",
		Action: "Testing status command",
	})

	// Connect and get snapshot
	client, err := agent.NewEventClient(sockPath)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer client.Close()

	snap, err := client.RequestSnapshot(1 * time.Hour)
	if err != nil {
		t.Fatalf("failed to get snapshot: %v", err)
	}

	if len(snap.Workers) != 1 {
		t.Errorf("expected 1 worker, got %d", len(snap.Workers))
	}
	if snap.Workers[0].Worker != "test/repo" {
		t.Errorf("expected test/repo, got %s", snap.Workers[0].Worker)
	}

	// Verify printStatus doesn't panic
	printStatus(snap, 1*time.Hour, statusFilters{})
}

func TestStatusCommand_NoSocket(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "nonexistent.sock")

	_, err := agent.NewEventClient(sockPath)
	if err == nil {
		t.Error("expected error when connecting to non-existent socket")
	}
}

// Helper to build test events.
func testEvents() []agent.Event {
	now := time.Now()
	return []agent.Event{
		{Type: agent.EventPollCycleStart, Category: agent.CategoryPollCycle, Worker: "owner/repo:prs", Action: "Poll cycle started", Timestamp: now.Add(-10 * time.Minute)},
		{Type: agent.EventActionStarted, Category: agent.CategoryCheck, Worker: "owner/repo:prs", Action: "Checking CI status", Timestamp: now.Add(-9 * time.Minute)},
		{Type: agent.EventActionCompleted, Category: agent.CategoryCI, Worker: "owner/repo:prs", Action: "CI unrelated", PRNumbers: []int{42}, Timestamp: now.Add(-8 * time.Minute)},
		{Type: agent.EventError, Category: agent.CategoryError, Worker: "nmstate/kube:prs", Action: "Agent failed", PRNumbers: []int{100}, Timestamp: now.Add(-7 * time.Minute)},
		{Type: agent.EventAgentCompleted, Category: agent.CategoryAgent, Worker: "nmstate/kube:issues", Action: "Agent completed", PRNumbers: []int{200}, Timestamp: now.Add(-6 * time.Minute)},
		{Type: agent.EventActionCompleted, Category: agent.CategoryReview, Worker: "owner/repo:prs", Action: "Review addressed", PRNumbers: []int{42}, Timestamp: now.Add(-5 * time.Minute)},
		{Type: agent.EventPollCycleEnd, Category: agent.CategoryPollCycle, Worker: "owner/repo:prs", Action: "Poll cycle completed", Timestamp: now.Add(-4 * time.Minute)},
		{Type: agent.EventActionStarted, Category: agent.CategoryCleanup, Worker: "owner/repo:prs", Action: "Cleanup complete", Timestamp: now.Add(-3 * time.Minute)},
		{Type: agent.EventActionStarted, Category: agent.CategorySkip, Worker: "owner/repo:prs", Action: "Skipped CI check", Timestamp: now.Add(-2 * time.Minute)},
		{Type: agent.EventActionStarted, Category: agent.CategoryTriage, Worker: "nmstate/kube:triage", Action: "Triage run", Timestamp: now.Add(-1 * time.Minute)},
	}
}

func TestDefaultFilterExcludesPollCheckCleanupSkip(t *testing.T) {
	events := testEvents()
	filters := statusFilters{categories: agent.DefaultEventCategories()}

	var filtered []agent.Event
	for _, e := range events {
		if matchesEventFilter(e, filters) {
			filtered = append(filtered, e)
		}
	}

	// poll, check, cleanup, skip should be excluded (4 events)
	// ci, error, agent, review, triage should be included (5 events)
	if len(filtered) != 5 {
		t.Errorf("expected 5 events with default filter, got %d", len(filtered))
		for _, e := range filtered {
			t.Logf("  %s: %s", e.Category, e.Action)
		}
	}

	// Verify excluded categories are not present
	for _, e := range filtered {
		switch e.Category {
		case agent.CategoryPollCycle, agent.CategoryCheck, agent.CategoryCleanup, agent.CategorySkip:
			t.Errorf("expected category %q to be excluded", e.Category)
		}
	}
}

func TestAllEventsShowsEverything(t *testing.T) {
	events := testEvents()
	filters := statusFilters{allEvents: true}

	var filtered []agent.Event
	for _, e := range events {
		if matchesEventFilter(e, filters) {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) != len(events) {
		t.Errorf("expected all %d events with --all-events, got %d", len(events), len(filtered))
	}
}

func TestEventsCategoryFilter(t *testing.T) {
	events := testEvents()
	filters := statusFilters{
		categories: map[agent.EventCategory]bool{
			agent.CategoryCI:    true,
			agent.CategoryError: true,
		},
	}

	var filtered []agent.Event
	for _, e := range events {
		if matchesEventFilter(e, filters) {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) != 2 {
		t.Errorf("expected 2 events with ci,error filter, got %d", len(filtered))
		for _, e := range filtered {
			t.Logf("  %s: %s", e.Category, e.Action)
		}
	}
}

func TestProjectFilter(t *testing.T) {
	events := testEvents()
	filters := statusFilters{allEvents: true, project: "nmstate"}

	var filtered []agent.Event
	for _, e := range events {
		if matchesEventFilter(e, filters) {
			filtered = append(filtered, e)
		}
	}

	// nmstate/kube:prs and nmstate/kube:issues and nmstate/kube:triage = 3 events
	if len(filtered) != 3 {
		t.Errorf("expected 3 nmstate events, got %d", len(filtered))
		for _, e := range filtered {
			t.Logf("  %s: %s (%s)", e.Worker, e.Action, e.Category)
		}
	}
}

func TestRoleFilter(t *testing.T) {
	events := testEvents()
	filters := statusFilters{allEvents: true, role: "triage"}

	var filtered []agent.Event
	for _, e := range events {
		if matchesEventFilter(e, filters) {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) != 1 {
		t.Errorf("expected 1 triage event, got %d", len(filtered))
	}
	if len(filtered) > 0 && filtered[0].Worker != "nmstate/kube:triage" {
		t.Errorf("expected nmstate/kube:triage, got %s", filtered[0].Worker)
	}
}

func TestPRFilter(t *testing.T) {
	events := testEvents()
	filters := statusFilters{allEvents: true, prNumbers: []int{42}}

	var filtered []agent.Event
	for _, e := range events {
		if matchesEventFilter(e, filters) {
			filtered = append(filtered, e)
		}
	}

	// PR #42 appears in 2 events (ci and review)
	if len(filtered) != 2 {
		t.Errorf("expected 2 events for PR #42, got %d", len(filtered))
		for _, e := range filtered {
			t.Logf("  %s: %s (%v)", e.Worker, e.Action, e.PRNumbers)
		}
	}
}

func TestCombinedFiltersUseAND(t *testing.T) {
	events := testEvents()
	filters := statusFilters{
		allEvents: true,
		project:   "nmstate",
		role:      "prs",
	}

	var filtered []agent.Event
	for _, e := range events {
		if matchesEventFilter(e, filters) {
			filtered = append(filtered, e)
		}
	}

	// Only nmstate/kube:prs events = 1 (error event)
	if len(filtered) != 1 {
		t.Errorf("expected 1 event for nmstate+prs, got %d", len(filtered))
		for _, e := range filtered {
			t.Logf("  %s: %s", e.Worker, e.Action)
		}
	}
}

func TestWorkerFilterByProject(t *testing.T) {
	workers := []agent.WorkerState{
		{Worker: "owner/repo:prs", State: "idle"},
		{Worker: "nmstate/kube:prs", State: "idle"},
		{Worker: "nmstate/kube:issues", State: "working"},
		{Worker: "nmstate/kube:triage", State: "idle"},
	}
	filters := statusFilters{project: "nmstate"}

	var filtered []agent.WorkerState
	for _, w := range workers {
		if matchesWorkerFilter(w, filters) {
			filtered = append(filtered, w)
		}
	}

	if len(filtered) != 3 {
		t.Errorf("expected 3 nmstate workers, got %d", len(filtered))
	}
}

func TestWorkerFilterByRole(t *testing.T) {
	workers := []agent.WorkerState{
		{Worker: "owner/repo:prs", State: "idle"},
		{Worker: "nmstate/kube:prs", State: "idle"},
		{Worker: "nmstate/kube:triage", State: "idle"},
	}
	filters := statusFilters{role: "prs"}

	var filtered []agent.WorkerState
	for _, w := range workers {
		if matchesWorkerFilter(w, filters) {
			filtered = append(filtered, w)
		}
	}

	if len(filtered) != 2 {
		t.Errorf("expected 2 prs workers, got %d", len(filtered))
	}
}

func TestWorkerFilterByPR(t *testing.T) {
	workers := []agent.WorkerState{
		{Worker: "owner/repo:prs", State: "idle", PRNumbers: []int{42, 43}},
		{Worker: "nmstate/kube:prs", State: "idle", PRNumbers: []int{100}},
		{Worker: "nmstate/kube:issues", State: "working"},
	}
	filters := statusFilters{prNumbers: []int{42}}

	var filtered []agent.WorkerState
	for _, w := range workers {
		if matchesWorkerFilter(w, filters) {
			filtered = append(filtered, w)
		}
	}

	if len(filtered) != 1 {
		t.Errorf("expected 1 worker with PR #42, got %d", len(filtered))
	}
	if len(filtered) > 0 && filtered[0].Worker != "owner/repo:prs" {
		t.Errorf("expected owner/repo:prs, got %s", filtered[0].Worker)
	}
}

func TestWorkerProject(t *testing.T) {
	tests := []struct {
		worker string
		want   string
	}{
		{"owner/repo:prs", "owner/repo"},
		{"nmstate/kube:triage", "nmstate/kube"},
		{"legacy-worker", "legacy-worker"},
	}
	for _, tt := range tests {
		got := workerProject(tt.worker)
		if got != tt.want {
			t.Errorf("workerProject(%q) = %q, want %q", tt.worker, got, tt.want)
		}
	}
}

func TestWorkerRole(t *testing.T) {
	tests := []struct {
		worker string
		want   string
	}{
		{"owner/repo:prs", "prs"},
		{"nmstate/kube:triage", "triage"},
		{"legacy-worker", ""},
	}
	for _, tt := range tests {
		got := workerRole(tt.worker)
		if got != tt.want {
			t.Errorf("workerRole(%q) = %q, want %q", tt.worker, got, tt.want)
		}
	}
}

func TestBuildFilterDescription(t *testing.T) {
	tests := []struct {
		name    string
		filters statusFilters
		want    string
	}{
		{
			name:    "no filters",
			filters: statusFilters{},
			want:    "",
		},
		{
			name:    "project only",
			filters: statusFilters{project: "nmstate"},
			want:    "project=nmstate",
		},
		{
			name:    "all-events",
			filters: statusFilters{allEvents: true},
			want:    "all-events",
		},
		{
			name:    "combined",
			filters: statusFilters{project: "nmstate", role: "prs"},
			want:    "project=nmstate, role=prs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFilterDescription(tt.filters)
			if got != tt.want {
				t.Errorf("buildFilterDescription() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCategoryTagsRendered(t *testing.T) {
	// Verify printStatus with categories doesn't panic
	snap := agent.StatusSnapshot{
		Uptime: 3600,
		Workers: []agent.WorkerState{
			{Worker: "test/repo:prs", State: "idle", Action: "Idle", LastEvent: time.Now()},
		},
		Events: []agent.Event{
			{Type: agent.EventActionCompleted, Category: agent.CategoryCI, Worker: "test/repo:prs", Action: "CI unrelated", Timestamp: time.Now()},
			{Type: agent.EventAgentCompleted, Category: agent.CategoryAgent, Worker: "test/repo:prs", Action: "Agent done", Timestamp: time.Now()},
		},
	}
	// Should not panic
	printStatus(snap, 1*time.Hour, statusFilters{})
}
