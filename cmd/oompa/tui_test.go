package main

import (
	"strings"
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

func TestTUIModel_WorkerCards(t *testing.T) {
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

	// Render a worker card
	card := model.renderWorkerCard(snap.Workers[0])
	if card == "" {
		t.Error("expected non-empty card rendering")
	}

	// Full view should render without panic
	view := model.View()
	if view == "" {
		t.Error("expected non-empty view")
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

	// Initial offset should be 0 (newest first)
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
	states := []string{"working", "idle", "sleeping", "error", "reviewing", "rebasing"}
	for _, state := range states {
		frames, ok := spriteFrames[state]
		if !ok {
			t.Errorf("missing sprite frames for state %q", state)
			continue
		}
		if len(frames) == 0 {
			t.Errorf("expected at least 1 frame for state %q", state)
		}
		for i, frame := range frames {
			if len(frame) == 0 {
				t.Errorf("frame %d for state %q has no lines", i, state)
			}
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

	// Render at different frames to ensure no panic
	for frame := range 10 {
		model.frame = frame
		card := model.renderWorkerCard(snap.Workers[0])
		if card == "" {
			t.Errorf("empty card at frame %d", frame)
		}
	}
}

func TestParseWorkerProject(t *testing.T) {
	tests := []struct {
		worker      string
		wantProject string
		wantRole    string
	}{
		{"owner/repo:prs", "owner/repo", "prs"},
		{"owner/repo:issues", "owner/repo", "issues"},
		{"owner/repo:triage", "owner/repo", "triage"},
		{"owner/repo", "owner/repo", ""},
	}
	for _, tt := range tests {
		project, role := parseWorkerProject(tt.worker)
		if project != tt.wantProject {
			t.Errorf("parseWorkerProject(%q) project = %q, want %q", tt.worker, project, tt.wantProject)
		}
		if role != tt.wantRole {
			t.Errorf("parseWorkerProject(%q) role = %q, want %q", tt.worker, role, tt.wantRole)
		}
	}
}

func TestGroupWorkersByProject(t *testing.T) {
	workers := []agent.WorkerState{
		{Worker: "nmstate/kubernetes-nmstate:prs", State: "working"},
		{Worker: "nmstate/kubernetes-nmstate:issues", State: "idle"},
		{Worker: "nmstate/kubernetes-nmstate:triage", State: "sleeping"},
		{Worker: "qinqon/oompa:issues", State: "working"},
	}

	groups := groupWorkersByProject(workers)

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	// Groups should be sorted by project name
	if groups[0].project != "nmstate/kubernetes-nmstate" {
		t.Errorf("expected first group 'nmstate/kubernetes-nmstate', got %q", groups[0].project)
	}
	if len(groups[0].workers) != 3 {
		t.Errorf("expected 3 workers in nmstate group, got %d", len(groups[0].workers))
	}

	if groups[1].project != "qinqon/oompa" {
		t.Errorf("expected second group 'qinqon/oompa', got %q", groups[1].project)
	}
	if len(groups[1].workers) != 1 {
		t.Errorf("expected 1 worker in oompa group, got %d", len(groups[1].workers))
	}
}

func TestConveyorBeltRendering(t *testing.T) {
	// Single-oompa belt
	snap := agent.StatusSnapshot{
		Workers: []agent.WorkerState{
			{Worker: "owner/repo:prs", State: "working", Action: "Investigating CI"},
		},
	}
	ch := make(chan agent.Event, 10)
	model := newTUIModel(snap, ch, nil)
	model.width = 120
	model.height = 40

	groups := groupWorkersByProject(model.workers)
	belt := model.renderConveyorBelt(groups[0], beltWidth(1, groups[0].project))
	if belt == "" {
		t.Error("expected non-empty belt rendering")
	}
	if !strings.Contains(belt, "owner/repo") {
		t.Error("expected belt to contain project name")
	}

	// Multi-oompa belt
	snap.Workers = []agent.WorkerState{
		{Worker: "nmstate/k8s-nmstate:prs", State: "working"},
		{Worker: "nmstate/k8s-nmstate:issues", State: "idle"},
		{Worker: "nmstate/k8s-nmstate:triage", State: "sleeping"},
	}
	model = newTUIModel(snap, ch, nil)
	model.width = 120
	model.height = 40

	groups = groupWorkersByProject(model.workers)
	multiBeltWidth := beltWidth(3, groups[0].project)
	belt = model.renderConveyorBelt(groups[0], multiBeltWidth)
	if belt == "" {
		t.Error("expected non-empty multi-oompa belt")
	}

	// Multi-oompa belt should be wider than single-oompa
	singleWidth := beltWidth(1, "x/y")
	if multiBeltWidth <= singleWidth {
		t.Errorf("expected multi-oompa belt (%d) wider than single (%d)", multiBeltWidth, singleWidth)
	}
}

func TestBeltWidthAdapts(t *testing.T) {
	single := beltWidth(1, "o/r")
	double := beltWidth(2, "o/r")
	triple := beltWidth(3, "o/r")

	if single >= double {
		t.Errorf("single (%d) should be less than double (%d)", single, double)
	}
	if double >= triple {
		t.Errorf("double (%d) should be less than triple (%d)", double, triple)
	}
	if single != 28 {
		t.Errorf("single oompa belt width should be 28, got %d", single)
	}

	// Long project name should expand belt width
	longProject := "ovn-kubernetes/ovn-kubernetes"
	longWidth := beltWidth(1, longProject)
	if longWidth < len(longProject)+8 {
		t.Errorf("belt width (%d) should accommodate project name '%s' + overhead", longWidth, longProject)
	}
}

func TestColumnTilingAdaptsToWidth(t *testing.T) {
	workers := []agent.WorkerState{
		{Worker: "proj-a/repo:prs", State: "working"},
		{Worker: "proj-b/repo:prs", State: "idle"},
		{Worker: "proj-c/repo:prs", State: "sleeping"},
	}
	snap := agent.StatusSnapshot{Workers: workers}
	ch := make(chan agent.Event, 10)

	// Wide terminal: all belts should fit in one row
	model := newTUIModel(snap, ch, nil)
	model.width = 200
	model.height = 40
	view := model.View()
	if view == "" {
		t.Error("expected non-empty view for wide terminal")
	}

	// Narrow terminal: belts should wrap
	model = newTUIModel(snap, ch, nil)
	model.width = 40
	model.height = 40
	view = model.View()
	if view == "" {
		t.Error("expected non-empty view for narrow terminal")
	}
}

func TestShortWorkerName(t *testing.T) {
	tests := []struct {
		worker string
		want   string
	}{
		{"nmstate/kubernetes-nmstate:prs", "kubernetes-nmstate:prs"},
		{"qinqon/oompa:issues", "oompa:issues"},
		{"owner/repo", "repo"},
		{"owner/repo:triage", "repo:triage"},
	}
	for _, tt := range tests {
		got := shortWorkerName(tt.worker)
		if got != tt.want {
			t.Errorf("shortWorkerName(%q) = %q, want %q", tt.worker, got, tt.want)
		}
	}
}
