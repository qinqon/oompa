package main

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/qinqon/oompa/pkg/agent"
)

func TestTUIModel_Update(t *testing.T) {
	snap := agent.StatusSnapshot{
		Uptime: 3600,
		Workers: []agent.WorkerState{
			{Worker: "test/repo:prs", State: "idle", Action: "Waiting"},
		},
	}
	ch := make(chan agent.Event, 10)
	model := newTUIModel(snap, ch, nil)

	// Send an event message
	event := agent.Event{
		Type:      agent.EventWorkerStateChange,
		Worker:    "test/repo:prs",
		State:     "working",
		Action:    "Processing issue",
		Timestamp: time.Now(),
	}
	updated, _ := model.Update(eventMsg(event))
	m := updated.(TUIModel)

	if len(m.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(m.events))
	}
	if m.workers[0].State != "working" {
		t.Errorf("expected worker state 'working', got %s", m.workers[0].State)
	}
	if m.workers[0].Action != "Processing issue" {
		t.Errorf("expected action 'Processing issue', got %s", m.workers[0].Action)
	}

	// Test disconnected message
	updated, _ = model.Update(disconnectedMsg{})
	m = updated.(TUIModel)
	if m.connected {
		t.Error("expected connected=false after disconnectedMsg")
	}
}

func TestTUIModel_OompaCards(t *testing.T) {
	snap := agent.StatusSnapshot{
		Uptime: 7200,
		Workers: []agent.WorkerState{
			{Worker: "project/repo:prs", State: "working", Action: "Investigating CI", PRNumbers: []int{42, 99}},
			{Worker: "project/repo:issues", State: "idle", Action: "Waiting"},
		},
	}
	ch := make(chan agent.Event, 10)
	model := newTUIModel(snap, ch, nil)
	model.width = 120
	model.height = 40

	// Render an oompa card
	card := model.renderOompaCard(snap.Workers[0])
	if card == "" {
		t.Error("expected non-empty card rendering")
	}

	// Full view should render without panic
	view := model.View()
	if view == "" {
		t.Error("expected non-empty view")
	}
}

func TestTUIModel_SuperBox(t *testing.T) {
	snap := agent.StatusSnapshot{
		Uptime: 3600,
		Workers: []agent.WorkerState{
			{Worker: "nmstate/k8s-nmstate:prs", State: "rebasing", Action: "3 behind main", PRNumbers: []int{1467}},
			{Worker: "nmstate/k8s-nmstate:issues", State: "idle", Action: "Waiting"},
			{Worker: "nmstate/k8s-nmstate:triage", State: "scheduled", Action: "Next 09:00"},
		},
	}
	ch := make(chan agent.Event, 10)
	model := newTUIModel(snap, ch, nil)
	model.width = 120
	model.height = 40

	// Group workers
	groups := groupWorkersByProject(snap.Workers)
	if len(groups) != 1 {
		t.Fatalf("expected 1 project group, got %d", len(groups))
	}
	if groups[0].name != "nmstate/k8s-nmstate" {
		t.Errorf("expected project name 'nmstate/k8s-nmstate', got %s", groups[0].name)
	}
	if len(groups[0].workers) != 3 {
		t.Errorf("expected 3 workers in group, got %d", len(groups[0].workers))
	}

	// Render super box
	box := model.renderSuperBox(groups[0])
	if box == "" {
		t.Error("expected non-empty super box rendering")
	}
}

func TestTUIModel_ScrollLog(t *testing.T) {
	now := time.Now()
	events := make([]agent.Event, 20)
	for i := range events {
		events[i] = agent.Event{
			Type:      agent.EventActionStarted,
			Worker:    "test/repo:prs",
			Action:    "Action",
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		}
	}
	snap := agent.StatusSnapshot{
		Uptime: 3600,
		Events: events,
	}
	ch := make(chan agent.Event, 10)
	model := newTUIModel(snap, ch, nil)
	model.width = 120
	model.height = 40

	if model.logOffset != 0 {
		t.Errorf("expected initial logOffset 0, got %d", model.logOffset)
	}

	// Scroll down
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m := updated.(TUIModel)
	if m.logOffset != 1 {
		t.Errorf("expected logOffset 1 after scroll down, got %d", m.logOffset)
	}

	// Scroll up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(TUIModel)
	if m.logOffset != 0 {
		t.Errorf("expected logOffset 0 after scroll up, got %d", m.logOffset)
	}

	// Don't scroll below 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(TUIModel)
	if m.logOffset != 0 {
		t.Errorf("expected logOffset to stay at 0, got %d", m.logOffset)
	}
}

func TestSpriteAnimation(t *testing.T) {
	// Verify all sprite states have at least one frame
	states := []string{"working", "idle", "sleeping", "error", "reviewing", "rebasing", "scheduled"}
	for _, state := range states {
		frames, ok := tuiSpriteFrames[state]
		if !ok {
			t.Errorf("missing sprite frames for state %q", state)
			continue
		}
		if len(frames) == 0 {
			t.Errorf("expected at least 1 frame for state %q", state)
		}
	}

	// Verify frame cycling works
	snap := agent.StatusSnapshot{
		Workers: []agent.WorkerState{
			{Worker: "test/repo:prs", State: "working"},
		},
	}
	ch := make(chan agent.Event, 10)
	model := newTUIModel(snap, ch, nil)
	model.width = 120
	model.height = 40

	for frame := range 10 {
		model.frame = frame
		card := model.renderOompaCard(snap.Workers[0])
		if card == "" {
			t.Errorf("empty card at frame %d", frame)
		}
	}
}

func TestGroupWorkersByProject(t *testing.T) {
	workers := []agent.WorkerState{
		{Worker: "org/repo-a:prs"},
		{Worker: "org/repo-b:prs"},
		{Worker: "org/repo-a:issues"},
		{Worker: "org/repo-c:prs"},
	}

	groups := groupWorkersByProject(workers)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	if groups[0].name != "org/repo-a" {
		t.Errorf("expected first group 'org/repo-a', got %s", groups[0].name)
	}
	if len(groups[0].workers) != 2 {
		t.Errorf("expected 2 workers in first group, got %d", len(groups[0].workers))
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello..."},
		{"ab", 3, "ab"},
		{"abcd", 3, "..."},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncateRunes(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}
