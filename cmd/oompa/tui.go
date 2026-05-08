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

// Sprite layout constants.
const (
	spriteWidth  = 11 // characters per sprite frame (padded)
	spriteHeight = 5  // lines per sprite (all frames normalized)
	spriteGap    = 2  // characters between adjacent oompas
)

// TUI styles
var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("57")).
			Padding(0, 1)

	logStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	titleStyle = lipgloss.NewStyle().Bold(true)

	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	statusWorking = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	statusIdle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	statusError   = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)

	beltStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))

	conveyorTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("208"))

)

// spriteFrames defines animation frames for oompa-loompa sprites per state.
// All frames are normalized to spriteWidth (11) chars wide and spriteHeight (5) lines tall.
var spriteFrames = map[string][][]string{
	"working": {
		{"   ___     ", "  (o.o)    ", " --|--|--  ", "   |  |    ", "  _/  \\_   "},
		{"   ___     ", "  (o.o)    ", " --|--|--  ", "   |  |    ", "  _/  \\_   "},
	},
	"idle": {
		{"   ___     ", "  (o.o)    ", " --|--|--  ", "   |  |    ", "  _/  \\_   "},
		{"   ___     ", "  (o.o)    ", " --|--|--  ", "   |  |    ", " _/  \\_    "},
	},
	"sleeping": {
		{"   ___     ", "  (-.- ) Z ", "   |__|    ", "  _/  \\_   ", "           "},
		{"   ___     ", "  (-.- ) Zz", "   |__|    ", "  _/  \\_   ", "           "},
		{"   ___     ", "  (-.- )Zzz", "   |__|    ", "  _/  \\_   ", "           "},
	},
	"error": {
		{"   ___     ", "  (x.x)    ", " --|--|--  ", "   |  |    ", "  _/  \\_   "},
		{"   ___     ", "  (x.x)  * ", " --|--|--  ", "   |  |    ", "  _/  \\_   "},
	},
	"reviewing": {
		{"   ___     ", "  (o.o)    ", " --|--|Q   ", "   |  |    ", "  _/  \\_   "},
		{"   ___     ", "  (o.o)    ", " --|--|q   ", "   |  |    ", "  _/  \\_   "},
	},
	"rebasing": {
		{"   ___     ", "  (o.o)    ", " --|--|>   ", "   |  |    ", "  _/  \\_   "},
		{"   ___     ", "  (o.o)    ", " --|--|]   ", "   |  |    ", "  _/  \\_   "},
	},
}

// TUIModel is the bubbletea model for the live TUI dashboard.
type TUIModel struct {
	workers      []agent.WorkerState
	events       []agent.Event
	width        int
	height       int
	frame        int // animation frame counter
	logOffset    int // scroll position in activity log
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
	return tea.Tick(250*time.Millisecond, func(_ time.Time) tea.Msg {
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
		m.uptime += 0.25
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

		// Auto-scroll to top (newest event first in the sorted view)
		m.logOffset = 0

		return m, listenForEvents(m.eventCh)
	}

	return m, nil
}

// projectGroup holds workers grouped by project (owner/repo).
type projectGroup struct {
	project string
	workers []agent.WorkerState
}

// parseWorkerProject splits a worker name "owner/repo:role" into project and role.
func parseWorkerProject(worker string) (project, role string) {
	if idx := strings.LastIndex(worker, ":"); idx != -1 {
		return worker[:idx], worker[idx+1:]
	}
	return worker, ""
}

// groupWorkersByProject groups workers into project groups, sorted by project name.
// Workers within each group are sorted by name for stable UI ordering.
func groupWorkersByProject(workers []agent.WorkerState) []projectGroup {
	grouped := make(map[string][]agent.WorkerState)
	var projectOrder []string

	for _, w := range workers {
		project, _ := parseWorkerProject(w.Worker)
		if _, exists := grouped[project]; !exists {
			projectOrder = append(projectOrder, project)
		}
		grouped[project] = append(grouped[project], w)
	}

	sort.Strings(projectOrder)

	groups := make([]projectGroup, 0, len(projectOrder))
	for _, p := range projectOrder {
		ws := grouped[p]
		sort.Slice(ws, func(i, j int) bool {
			return ws[i].Worker < ws[j].Worker
		})
		groups = append(groups, projectGroup{project: p, workers: ws})
	}
	return groups
}

func (m TUIModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	// Header
	connStatus := "\u25cf Connected"
	if !m.connected {
		connStatus = "\u25cb Disconnected"
	}
	now := time.Now().UTC()
	header := headerStyle.Width(m.width).Render(
		fmt.Sprintf("\U0001f3ed OOMPA FACTORY%s%s  %02d:%02d UTC",
			strings.Repeat(" ", max(m.width-55, 1)),
			connStatus,
			now.Hour(),
			now.Minute(),
		),
	)
	b.WriteString(header + "\n\n")

	// Group workers by project and render conveyor belts
	groups := groupWorkersByProject(m.workers)

	// Tile projects into columns based on terminal width
	if len(groups) > 0 {
		b.WriteString(m.renderConveyorBelts(groups))
	}

	// Activity log
	availableHeight := max(m.height-lipgloss.Height(b.String())-4, 3)
	b.WriteString(m.renderActivityLog(availableHeight))
	b.WriteString(dimStyle.Render("  q: quit  j/k: scroll log"))

	return b.String()
}

// beltWidth returns the width of a conveyor belt based on the number of oompas
// and the project name length, ensuring the title line fits.
func beltWidth(numOompas int, project string) int {
	// Width based on oompa count: spriteWidth per oompa + spriteGap between + margins
	minSingleOompa := 28
	w := (spriteWidth+spriteGap)*numOompas + 4
	if numOompas <= 1 {
		w = minSingleOompa
	}
	// Ensure belt is at least as wide as the project title
	// (title has ═══ padding + ▶ arrow = ~8 extra chars)
	titleOverhead := 8
	if titleLen := len(project) + titleOverhead; titleLen > w {
		return titleLen
	}
	return w
}

// renderConveyorBelts renders all project groups as conveyor belts tiled into columns.
func (m TUIModel) renderConveyorBelts(groups []projectGroup) string {
	var rows []string
	usedWidth := 0
	var currentRow []string
	const interBeltGap = 4 // gap between adjacent belts in a row

	for _, g := range groups {
		bw := beltWidth(len(g.workers), g.project)
		neededWidth := bw
		if usedWidth > 0 {
			neededWidth += interBeltGap // only add gap after the first belt
		}
		if usedWidth > 0 && usedWidth+neededWidth > m.width {
			// Wrap to next row
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, currentRow...))
			currentRow = nil
			usedWidth = 0
		}

		belt := m.renderConveyorBelt(g, bw)
		// Pad belt to its full width so JoinHorizontal aligns columns correctly
		belt = lipgloss.NewStyle().Width(bw + interBeltGap).Render(belt)
		currentRow = append(currentRow, belt)
		usedWidth += bw + interBeltGap
	}
	if len(currentRow) > 0 {
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, currentRow...))
	}

	return strings.Join(rows, "\n") + "\n"
}

// renderConveyorBelt renders a single project as a conveyor belt with oompas on top.
func (m TUIModel) renderConveyorBelt(g projectGroup, width int) string {
	var lines []string

	// Belt title line: ═══ project-name ═══▶
	// Total visible width = leftEquals + titleText + rightEquals + 1 (arrow) = width
	titleText := " " + g.project + " "
	remainingWidth := max(width-len(titleText)-1, 4) // -1 for the trailing ▶ arrow
	leftEquals := remainingWidth / 2
	rightEquals := remainingWidth - leftEquals
	beltTitle := conveyorTitleStyle.Render(
		strings.Repeat("\u2550", leftEquals) + titleText + strings.Repeat("\u2550", rightEquals) + "\u25b6",
	)
	lines = append(lines, beltTitle)

	// Render oompa sprites side-by-side
	for row := range spriteHeight {
		var rowStr strings.Builder
		for _, w := range g.workers {
			state := w.State
			if state == "" {
				state = "idle"
			}
			frames, ok := spriteFrames[state]
			if !ok {
				frames = spriteFrames["idle"]
			}
			frameIdx := (m.frame / 2) % len(frames)
			frame := frames[frameIdx]
			if row < len(frame) {
				rowStr.WriteString(frame[row])
			} else {
				rowStr.WriteString(strings.Repeat(" ", spriteWidth))
			}
			rowStr.WriteString(strings.Repeat(" ", spriteGap))
		}
		lines = append(lines, rowStr.String())
	}

	// Conveyor belt surface: ●●●● dots
	beltActive := false
	for _, w := range g.workers {
		s := w.State
		if s == "working" || s == "reviewing" || s == "rebasing" {
			beltActive = true
			break
		}
	}
	beltDots := m.renderBeltDots(width, beltActive)
	lines = append(lines, beltStyle.Render(beltDots))

	// Role labels and status line
	var roleLabels []string
	var statusParts []string
	for _, w := range g.workers {
		_, role := parseWorkerProject(w.Worker)
		if role == "" {
			role = "worker"
		}

		// Build role label with PR numbers
		label := role
		if len(w.PRNumbers) > 0 {
			nums := make([]string, len(w.PRNumbers))
			for i, n := range w.PRNumbers {
				nums[i] = fmt.Sprintf("#%d", n)
			}
			label += " [" + strings.Join(nums, ",") + "]"
		}
		roleLabels = append(roleLabels, label)

		// Status indicator
		state := w.State
		if state == "" {
			state = "idle"
		}
		icon := stateIcon(state)
		action := truncateRunes(w.Action, 20)
		statusLine := icon + " " + action
		switch state {
		case "working", "reviewing", "rebasing":
			statusParts = append(statusParts, statusWorking.Render(statusLine))
		case "error", "stuck":
			statusParts = append(statusParts, statusError.Render(statusLine))
		default:
			statusParts = append(statusParts, statusIdle.Render(statusLine))
		}
	}
	lines = append(lines, "  "+strings.Join(roleLabels, "    "))
	lines = append(lines, "  "+strings.Join(statusParts, "  "))
	lines = append(lines, "") // blank separator

	return strings.Join(lines, "\n")
}

// renderBeltDots renders the conveyor belt surface with optional animation.
func (m TUIModel) renderBeltDots(width int, active bool) string {
	dotCount := max(width-2, 10)
	if !active {
		return "  " + strings.Repeat("\u25cf", dotCount)
	}
	// Animated: shift dots right
	offset := m.frame % 3
	var dots strings.Builder
	dots.WriteString("  ")
	for i := range dotCount {
		if (i+offset)%3 == 0 {
			dots.WriteString("\u25cb") // hollow dot for animation
		} else {
			dots.WriteString("\u25cf") // filled dot
		}
	}
	return dots.String()
}

// shortWorkerName returns a compact worker name for the activity log.
// "nmstate/kubernetes-nmstate:prs" -> "kubernetes-nmstate:prs"
// "qinqon/oompa:issues" -> "oompa:issues"
func shortWorkerName(worker string) string {
	project, role := parseWorkerProject(worker)
	// Use just the repo part (strip owner prefix)
	if idx := strings.Index(project, "/"); idx != -1 {
		project = project[idx+1:]
	}
	if role != "" {
		return project + ":" + role
	}
	return project
}

func (m TUIModel) renderWorkerCard(w agent.WorkerState) string {
	state := w.State
	if state == "" {
		state = "idle"
	}

	// Get sprite for this state
	spriteKey := state
	frames, ok := spriteFrames[spriteKey]
	if !ok {
		frames = spriteFrames["idle"]
	}

	frameIdx := (m.frame / 2) % len(frames) // change every 500ms
	sprite := frames[frameIdx]

	// Build card content
	var lines []string

	// Worker name with PR info
	name := w.Worker
	if len(w.PRNumbers) > 0 {
		nums := make([]string, len(w.PRNumbers))
		for i, n := range w.PRNumbers {
			nums[i] = fmt.Sprintf("%d", n)
		}
		name += " [" + strings.Join(nums, ",") + "]"
	}
	lines = append(lines, titleStyle.Render(truncateRunes(name, 26)))

	// Sprite
	lines = append(lines, sprite...)

	// State
	stateStr := stateIcon(state) + " " + state
	switch state {
	case "working", "reviewing", "rebasing":
		lines = append(lines, statusWorking.Render(stateStr))
	case "error", "stuck":
		lines = append(lines, statusError.Render(stateStr))
	default:
		lines = append(lines, statusIdle.Render(stateStr))
	}

	// Action
	action := w.Action
	if action != "" {
		lines = append(lines, truncateRunes(action, 26))
	}
	// Detail
	detail := w.Detail
	if detail != "" {
		lines = append(lines, dimStyle.Render(truncateRunes(detail, 26)))
	}

	return strings.Join(lines, "\n")
}

// Layout thresholds for the activity log.
const (
	twoColumnMinWidth = 100 // minimum terminal width for 2-column activity log
	logWorkerWidth    = 16  // fixed column width for worker names in the log
)

func (m TUIModel) renderActivityLog(height int) string {
	title := fmt.Sprintf(" Activity Log (%d events)", len(m.events))

	// Sort events newest first for display
	sortedEvents := make([]agent.Event, len(m.events))
	copy(sortedEvents, m.events)
	sort.Slice(sortedEvents, func(i, j int) bool {
		return sortedEvents[i].Timestamp.After(sortedEvents[j].Timestamp)
	})

	// Apply scroll offset
	startIdx := m.logOffset
	if startIdx >= len(sortedEvents) {
		startIdx = 0
	}
	endIdx := min(
		// -2 for title and border
		startIdx+height-2, len(sortedEvents))

	visibleEvents := sortedEvents[startIdx:endIdx]

	var lines []string
	lines = append(lines, titleStyle.Render(title))

	if m.width < twoColumnMinWidth {
		// Single-column layout for narrow terminals
		for _, e := range visibleEvents {
			lines = append(lines, formatLogEntry(e, m.width-4))
		}
	} else {
		// Two-column layout: split available events into two columns.
		// Use lipgloss.Width for column padding because log entries contain
		// ANSI escape sequences that break fmt.Sprintf byte-based %-*s padding.
		halfWidth := (m.width - 8) / 2
		colStyle := lipgloss.NewStyle().Width(halfWidth)
		for i := 0; i < len(visibleEvents); i += 2 {
			left := colStyle.Render(formatLogEntry(visibleEvents[i], halfWidth))
			right := ""
			if i+1 < len(visibleEvents) {
				right = formatLogEntry(visibleEvents[i+1], halfWidth)
			}
			lines = append(lines, " "+left+"  "+right)
		}
	}

	content := strings.Join(lines, "\n")
	return logStyle.Width(m.width-2).Render(content) + "\n"
}

// formatLogEntry formats a single log entry for the activity log.
func formatLogEntry(e agent.Event, maxWidth int) string {
	ts := e.Timestamp.Local().Format("15:04")
	worker := truncateRunes(shortWorkerName(e.Worker), logWorkerWidth)
	action := e.Action
	if e.Detail != "" {
		action += " - " + e.Detail
	}
	// 5 for "HH:MM" + 2 spaces + 1 gap between worker and action = 8
	maxActionLen := max(maxWidth-8-logWorkerWidth, 10)
	action = truncateRunes(action, maxActionLen)
	return fmt.Sprintf("%s  %-*s %s", dimStyle.Render(ts), logWorkerWidth, worker, action)
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
