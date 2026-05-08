package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/qinqon/oompa/pkg/agent"
)

func runTUICommand(args []string) {
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	socketPath := fs.String("socket", "", "Override socket path (default: auto-detect)")
	fs.Parse(args) //nolint:errcheck // ExitOnError flag set handles parse errors

	sock := *socketPath
	if sock == "" {
		sock = agent.DefaultSocketPath()
	}

	// Use a single connection: RequestStream returns the initial snapshot
	// and event channel over one connection, avoiding the race condition
	// where events could be missed between a separate snapshot and stream.
	client, err := agent.NewEventClient(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not connect to oompa daemon at %s\n", sock)
		fmt.Fprintln(os.Stderr, "Is oompa running? Check with: systemctl --user status oompa")
		os.Exit(1)
	}

	snap, eventCh, err := client.RequestStream()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to start event stream: %v\n", err)
		client.Close()
		os.Exit(1)
	}

	model := newTUIModel(snap, eventCh, client)

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

// ── Styles ──────────────────────────────────────────────────────────

const cardInnerWidth = 22

var (
	tuiHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("57")).
			Padding(0, 1)

	// Inner oompa card styles
	oompaCardIdle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1).
			Width(cardInnerWidth + 4)

	oompaCardActive = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("39")).
			Padding(0, 1).
			Width(cardInnerWidth + 4)

	oompaCardError = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("196")).
			Padding(0, 1).
			Width(cardInnerWidth + 4)

	oompaCardScheduled = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("141")).
				Padding(0, 1).
				Width(cardInnerWidth + 4)

	// Super box styles (thick border to distinguish from inner cards)
	superBoxIdle = lipgloss.NewStyle().
			Border(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)

	superBoxActive = lipgloss.NewStyle().
			Border(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("39")).
			Padding(0, 1)

	superBoxError = lipgloss.NewStyle().
			Border(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("196")).
			Padding(0, 1)

	superBoxMixed = lipgloss.NewStyle().
			Border(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("214")).
			Padding(0, 1)

	projectNameStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214")).
				Bold(true)

	roleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250"))

	prStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("117"))

	stateWorkingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	stateIdleStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	stateErrorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	stateScheduleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))

	tuiDimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	beltActiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	beltIdleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	beltErrorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	tuiLogStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	tuiTitleStyle = lipgloss.NewStyle().Bold(true)
)

// ── Sprite frames ───────────────────────────────────────────────────

// spriteFrames defines animation frames for oompa-loompa sprites per state.
var tuiSpriteFrames = map[string][][4]string{
	"idle": {
		{"   ___   ", "  (o.o)  ", " --|--|-- ", "  _/ \\_ "},
		{"   ___   ", "  (o.o)  ", " --|--|-- ", "  _/ \\_ "},
	},
	"working": {
		{"   ___   ", "  (o.o)  ", " --|--|\\  ", "  _/ \\_🔨"},
		{"   ___  🔨", "  (o.o)/  ", " --|--|  ", "  _/ \\_  "},
	},
	"sleeping": {
		{"   ___  Z", "  (-.-) z", "   |__|  ", "  _/ \\_  "},
		{"   ___ Zz", "  (-.-)  ", "   |__|  ", "  _/ \\_  "},
		{"   ___Zzz", "  (-.-)  ", "   |__|  ", "  _/ \\_  "},
	},
	"rebasing": {
		{"  ╠═╬═╣  ", "  ╠═╬═╣  ", "  ╠═╬═╣  ", "  ╠(o.o) "},
		{"  ╠═╬═╣  ", "  ╠═╬═╣  ", "  ╠(o.o) ", "  ╠--|-- "},
		{"  ╠═╬═╣  ", "  ╠(o.o) ", "  ╠--|-- ", "  ╠═╬═╣  "},
		{"  ╠(o.o) ", "  ╠--|-- ", "  ╠═╬═╣  ", "  ╠═╬═╣  "},
		{"  ╠═╬═╣  ", "  ╠═╬═╣  ", "  ╠═╬═╣  ", "  ╠═╬═╣  "},
	},
	"reviewing": {
		{"   ___   ", "  (o.o)  ", " --|--|🔍 ", "  _/ \\_  "},
		{"   ___   ", "  (o.o)  ", " --|--|🔎 ", "  _/ \\_  "},
	},
	"error": {
		{"   ___   ", "  (x.x)  ", " --|--|-- ", "  _/ \\_  "},
		{"   ___ ★ ", "  (x.x)  ", " --|--|-- ", "  _/ \\_  "},
		{" ★ ___ ★ ", "  (x.x)  ", " --|--|-- ", "  _/ \\_  "},
	},
	"scheduled": {
		{"   ___   ", "  (-.-) ☽", "   |__|  ", "  _/ \\_  "},
		{"   ___ ☽ ", "  (-.-)  ", "   |__|  ", "  _/ \\_  "},
	},
}

// stateLabels maps states to plain descriptive words.
var stateLabels = map[string]string{
	"idle":      "idle",
	"working":   "working",
	"sleeping":  "sleeping",
	"rebasing":  "rebasing",
	"reviewing": "reviewing",
	"error":     "error",
	"scheduled": "scheduled",
}

// ── ProjectGroup ────────────────────────────────────────────────────

type projectGroup struct {
	name    string
	workers []agent.WorkerState
}

// ── TUI Model ───────────────────────────────────────────────────────

// TUIModel is the bubbletea model for the live TUI dashboard.
type TUIModel struct {
	workers      []agent.WorkerState
	events       []agent.Event
	width        int
	height       int
	frame        int
	logOffset    int
	connected    bool
	eventCh      <-chan agent.Event
	streamClient *agent.EventClient
	uptime       float64
}

type eventMsg agent.Event
type tickMsg struct{}
type disconnectedMsg struct{}

func newTUIModel(snap agent.StatusSnapshot, eventCh <-chan agent.Event, streamClient *agent.EventClient) TUIModel {
	return TUIModel{
		workers:      snap.Workers,
		events:       snap.Events,
		connected:    true,
		eventCh:      eventCh,
		streamClient: streamClient,
		uptime:       snap.Uptime,
	}
}

func (m TUIModel) Init() tea.Cmd {
	return tea.Batch(tickAnimation(), listenForEvents(m.eventCh))
}

func tickAnimation() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
}

func listenForEvents(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return disconnectedMsg{}
		}
		return eventMsg(event)
	}
}

func (m TUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			if m.streamClient != nil {
				m.streamClient.Close()
			}
			return m, tea.Quit
		case "up", "k":
			if m.logOffset > 0 {
				m.logOffset--
			}
		case "down", "j":
			maxOffset := max(len(m.events)-10, 0)
			if m.logOffset < maxOffset {
				m.logOffset++
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		m.frame++
		m.uptime += 0.15
		return m, tickAnimation()

	case disconnectedMsg:
		m.connected = false
		return m, nil

	case eventMsg:
		event := agent.Event(msg)
		m.events = append(m.events, event)

		// Update worker state
		found := false
		for i, w := range m.workers {
			if w.Worker != event.Worker {
				continue
			}
			if event.State != "" {
				m.workers[i].State = event.State
			}
			if event.Action != "" {
				m.workers[i].Action = event.Action
			}
			if event.Detail != "" {
				m.workers[i].Detail = event.Detail
			}
			if len(event.PRNumbers) > 0 {
				m.workers[i].PRNumbers = event.PRNumbers
			}
			m.workers[i].LastEvent = event.Timestamp
			found = true
			break
		}
		if !found && event.Worker != "" {
			m.workers = append(m.workers, agent.WorkerState{
				Worker:    event.Worker,
				State:     event.State,
				Action:    event.Action,
				Detail:    event.Detail,
				PRNumbers: event.PRNumbers,
				LastEvent: event.Timestamp,
			})
		}

		m.logOffset = 0
		return m, listenForEvents(m.eventCh)
	}

	return m, nil
}

// ── Rendering ───────────────────────────────────────────────────────

func tuiStateIcon(state string) string {
	switch state {
	case "working", "reviewing", "rebasing":
		return "●"
	case "error":
		return "✖"
	case "scheduled":
		return "◐"
	default:
		return "○"
	}
}

func (m TUIModel) renderBelt(state string, width int) string {
	if width < 4 {
		width = 4
	}
	dotsLen := width - 2

	var buf strings.Builder
	for i := 0; i < dotsLen; i++ {
		switch state {
		case "working", "reviewing", "rebasing":
			pos := (i + m.frame) % 5
			if pos < 3 {
				buf.WriteRune('●')
			} else {
				buf.WriteRune('○')
			}
		case "error":
			if m.frame%6 < 3 {
				buf.WriteRune('○')
			} else {
				buf.WriteRune('●')
			}
		default:
			buf.WriteRune('○')
		}
	}

	dots := buf.String() + "━▶"
	switch state {
	case "working", "reviewing", "rebasing":
		return beltActiveStyle.Render(dots)
	case "error":
		return beltErrorStyle.Render(dots)
	default:
		return beltIdleStyle.Render(dots)
	}
}

func (m TUIModel) renderOompaCard(w agent.WorkerState) string {
	cw := cardInnerWidth

	state := w.State
	if state == "" {
		state = "idle"
	}

	// Sprite
	frames, ok := tuiSpriteFrames[state]
	if !ok {
		frames = tuiSpriteFrames["idle"]
	}
	frameIdx := (m.frame / 3) % len(frames)
	sprite := frames[frameIdx]

	// State label
	label := stateLabels[state]
	if label == "" {
		label = state
	}
	var labelLine string
	switch state {
	case "working", "reviewing", "rebasing":
		labelLine = stateWorkingStyle.Render("  ~ " + label + " ~")
	case "error":
		labelLine = stateErrorStyle.Render("  ~ " + label + " ~")
	case "scheduled":
		labelLine = stateScheduleStyle.Render("  ~ " + label + " ~")
	default:
		labelLine = stateIdleStyle.Render("  ~ " + label + " ~")
	}

	// Belt
	belt := m.renderBelt(state, cw)

	// Role + PRs
	// Extract role from worker name (format: "owner/repo:role")
	role := w.Worker
	if idx := strings.LastIndex(role, ":"); idx >= 0 {
		role = role[idx+1:]
	}
	roleLine := roleStyle.Render(role)
	if len(w.PRNumbers) > 0 {
		nums := make([]string, len(w.PRNumbers))
		for i, n := range w.PRNumbers {
			nums[i] = fmt.Sprintf("#%d", n)
		}
		roleLine += " " + prStyle.Render(strings.Join(nums, ","))
	}

	// State + action
	icon := tuiStateIcon(state)
	stateText := icon + " " + truncateRunes(w.Action, cw-4)
	var stateLine string
	switch state {
	case "working", "reviewing", "rebasing":
		stateLine = stateWorkingStyle.Render(stateText)
	case "error":
		stateLine = stateErrorStyle.Render(stateText)
	case "scheduled":
		stateLine = stateScheduleStyle.Render(stateText)
	default:
		stateLine = stateIdleStyle.Render(stateText)
	}

	content := strings.Join([]string{
		sprite[0],
		sprite[1],
		sprite[2],
		sprite[3],
		labelLine,
		belt,
		roleLine,
		stateLine,
	}, "\n")

	// Pick card border style
	cardStyle := oompaCardIdle
	switch state {
	case "working", "reviewing", "rebasing":
		cardStyle = oompaCardActive
	case "error":
		cardStyle = oompaCardError
	case "scheduled":
		cardStyle = oompaCardScheduled
	}

	return cardStyle.Render(content)
}

// groupWorkersByProject groups workers by project name, preserving order.
func groupWorkersByProject(workers []agent.WorkerState) []projectGroup {
	projectMap := make(map[string][]agent.WorkerState)
	var projectOrder []string
	for _, w := range workers {
		// Extract project from worker name (format: "owner/repo:role" or "owner/repo")
		project := w.Worker
		if idx := strings.LastIndex(project, ":"); idx >= 0 {
			project = project[:idx]
		}
		if _, exists := projectMap[project]; !exists {
			projectOrder = append(projectOrder, project)
		}
		projectMap[project] = append(projectMap[project], w)
	}
	var groups []projectGroup
	for _, name := range projectOrder {
		groups = append(groups, projectGroup{name: name, workers: projectMap[name]})
	}
	return groups
}

// bestGroupState returns the most important state for super box border coloring.
func bestGroupState(workers []agent.WorkerState) string {
	hasActive := false
	hasError := false
	for _, w := range workers {
		switch w.State {
		case "working", "reviewing", "rebasing":
			hasActive = true
		case "error":
			hasError = true
		}
	}
	if hasError {
		return "error"
	}
	if hasActive {
		return "active"
	}
	return "idle"
}

func (m TUIModel) renderSuperBox(group projectGroup) string {
	// Render each oompa card
	var cards []string
	for _, w := range group.workers {
		cards = append(cards, m.renderOompaCard(w))
	}

	// Join cards horizontally
	innerContent := lipgloss.JoinHorizontal(lipgloss.Top, cards...)

	// Project name header
	header := " " + projectNameStyle.Render(group.name)

	// Full content
	fullContent := header + "\n" + innerContent

	// Pick super box style
	boxStyle := superBoxIdle
	switch bestGroupState(group.workers) {
	case "active":
		boxStyle = superBoxActive
	case "error":
		boxStyle = superBoxError
	}
	if len(group.workers) > 1 {
		boxStyle = superBoxMixed
	}

	return boxStyle.Render(fullContent)
}

func (m TUIModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	// Header
	uptime := time.Duration(m.uptime * float64(time.Second))
	hours := int(uptime.Hours())
	minutes := int(uptime.Minutes()) % 60
	connStatus := "● Connected"
	if !m.connected {
		connStatus = "● Disconnected"
	}
	header := tuiHeaderStyle.Width(m.width).Render(
		fmt.Sprintf("  🏭 OOMPA FACTORY                                       %s  %02d:%02d UTC  uptime %dh%02dm",
			connStatus,
			time.Now().UTC().Hour(),
			time.Now().UTC().Minute(),
			hours, minutes,
		),
	)
	b.WriteString(header + "\n\n")

	// Sort workers alphabetically
	sort.Slice(m.workers, func(i, j int) bool {
		return m.workers[i].Worker < m.workers[j].Worker
	})

	// Group workers by project
	groups := groupWorkersByProject(m.workers)

	// Render all super boxes
	type renderedBox struct {
		content string
		width   int
	}
	var boxes []renderedBox
	for _, g := range groups {
		rendered := m.renderSuperBox(g)
		w := lipgloss.Width(rendered)
		boxes = append(boxes, renderedBox{content: rendered, width: w})
	}

	// Adaptive layout: pack super boxes into rows
	gap := 1
	var currentRow []string
	currentWidth := 0

	for _, box := range boxes {
		needed := box.width
		if currentWidth > 0 {
			needed += gap
		}
		if currentWidth > 0 && currentWidth+needed > m.width {
			b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, currentRow...) + "\n")
			currentRow = nil
			currentWidth = 0
		}
		if currentWidth > 0 {
			currentRow = append(currentRow, strings.Repeat(" ", gap))
			currentWidth += gap
		}
		currentRow = append(currentRow, box.content)
		currentWidth += box.width
	}
	if len(currentRow) > 0 {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, currentRow...) + "\n")
	}

	// Activity log
	linesUsed := strings.Count(b.String(), "\n")
	logHeight := max(m.height-linesUsed-2, 3)
	b.WriteString(m.renderTUIActivityLog(logHeight))
	b.WriteString(tuiDimStyle.Render("  q: quit  j/k: scroll log"))

	return b.String()
}

func (m TUIModel) renderTUIActivityLog(height int) string {
	title := fmt.Sprintf(" Activity Log (%d events)", len(m.events))

	sortedEvents := make([]agent.Event, len(m.events))
	copy(sortedEvents, m.events)
	sort.Slice(sortedEvents, func(i, j int) bool {
		return sortedEvents[i].Timestamp.After(sortedEvents[j].Timestamp)
	})

	startIdx := m.logOffset
	if startIdx >= len(sortedEvents) {
		startIdx = 0
	}
	endIdx := min(startIdx+height-2, len(sortedEvents))

	var lines []string
	lines = append(lines, tuiTitleStyle.Render(title))
	for _, e := range sortedEvents[startIdx:endIdx] {
		ts := e.Timestamp.Local().Format("15:04:05")
		action := e.Action
		if e.Detail != "" {
			action += " - " + e.Detail
		}
		maxActionLen := max(m.width-35, 20)
		action = truncateRunes(action, maxActionLen)
		line := fmt.Sprintf(" %s  %-18s %s", tuiDimStyle.Render(ts), e.Worker, action)
		lines = append(lines, line)
	}

	content := strings.Join(lines, "\n")
	return tuiLogStyle.Width(m.width - 2).Render(content) + "\n"
}

// truncateRunes truncates a string to maxLen runes, appending "..." if truncated.
// Operates on runes to avoid splitting multi-byte UTF-8 sequences.
func truncateRunes(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return "..."
	}
	return string(runes[:maxLen-3]) + "..."
}
