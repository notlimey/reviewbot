package main

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// Views
type viewType int

const (
	viewDashboard viewType = iota
	viewPassRun
	viewFindings
	viewFindingDetail
)

// Messages
type statsRefreshMsg struct{ stats dashboardStats }
type passStartMsg struct{ name string }
type passCompleteMsg struct{ err error }
type passLogMsg struct{ line string }
type passProgressMsg struct {
	n, total int
	path     string
}
type passTokensMsg struct {
	label string
	count int
}
type resetCompleteMsg struct{}

// model is the root Bubble Tea model.
type model struct {
	db          *sql.DB
	projectRoot string
	model       string
	dbPath      string
	delay       int
	maxTools    int
	verbose     bool
	reportPath  string

	view    viewType
	stats   dashboardStats
	spinner spinner.Model

	// Pass execution state
	running     bool
	runningPass string
	passLog     []string
	passN       int
	passTotal   int
	passPath    string
	passTokens  int

	// Findings browser
	findings       []Finding
	findingIdx     int
	findingFilter  string
	findingsOffset int

	// Help overlay
	showHelp bool

	// Window size
	width  int
	height int

	// TUI program reference (for tuiProgress)
	program *tea.Program
}

func newModel(db *sql.DB, projectRoot, modelName, dbPath, reportPath string, delay, maxTools int, verbose bool) model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = valueStyle

	return model{
		db:          db,
		projectRoot: projectRoot,
		model:       modelName,
		dbPath:      dbPath,
		reportPath:  reportPath,
		delay:       delay,
		maxTools:    maxTools,
		verbose:     verbose,
		view:        viewDashboard,
		spinner:     s,
		stats:       fetchDashboardStats(db),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tickRefresh())
}

// tickRefresh refreshes dashboard stats every 2 seconds.
func tickRefresh() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

type tickMsg struct{}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tickMsg:
		m.stats = fetchDashboardStats(m.db)
		return m, tickRefresh()

	case statsRefreshMsg:
		m.stats = msg.stats
		return m, nil

	case passStartMsg:
		m.running = true
		m.runningPass = msg.name
		m.passLog = nil
		m.passN = 0
		m.passTotal = 0
		m.passPath = ""
		m.passTokens = 0
		m.view = viewPassRun
		return m, nil

	case passLogMsg:
		m.passLog = append(m.passLog, msg.line)
		// Keep last 100 lines
		if len(m.passLog) > 100 {
			m.passLog = m.passLog[len(m.passLog)-100:]
		}
		return m, nil

	case passProgressMsg:
		m.passN = msg.n
		m.passTotal = msg.total
		m.passPath = msg.path
		return m, nil

	case passTokensMsg:
		m.passTokens = msg.count
		return m, nil

	case passCompleteMsg:
		m.running = false
		if msg.err != nil {
			m.passLog = append(m.passLog, errorStyle.Render(fmt.Sprintf("Error: %v", msg.err)))
		} else {
			m.passLog = append(m.passLog, successStyle.Render("Complete!"))
		}
		m.stats = fetchDashboardStats(m.db)
		return m, nil

	case resetCompleteMsg:
		m.stats = fetchDashboardStats(m.db)
		return m, nil

	case programRefMsg:
		m.program = msg.program
		return m, nil
	}

	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global keys
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "q":
		if !m.showHelp {
			return m, tea.Quit
		}
		m.showHelp = false
		return m, nil
	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	}

	if m.showHelp {
		m.showHelp = false
		return m, nil
	}

	switch m.view {
	case viewDashboard:
		return m.handleDashboardKey(key)
	case viewPassRun:
		return m.handlePassRunKey(key)
	case viewFindings:
		return m.handleFindingsKey(key)
	case viewFindingDetail:
		return m.handleFindingDetailKey(key)
	}

	return m, nil
}

func (m model) handleDashboardKey(key string) (tea.Model, tea.Cmd) {
	if m.running {
		// While a pass is running from dashboard, only allow switching to pass view or quit
		if key == "esc" || key == "enter" {
			m.view = viewPassRun
		}
		return m, nil
	}

	switch key {
	case "d":
		return m, m.startPass("Discovery", func(prog Progress) error {
			return runDiscovery(m.db, m.projectRoot, prog)
		})
	case "s":
		return m, m.startPass("Scan", func(prog Progress) error {
			return runScan(m.db, m.projectRoot, m.model, m.delay, m.verbose, prog)
		})
	case "r":
		return m, m.startPass("Relations", func(prog Progress) error {
			return runRelations(m.db, prog)
		})
	case "t":
		return m, m.startPass("Structural", func(prog Progress) error {
			return runStructural(m.db, m.projectRoot, m.model, m.maxTools, m.delay, m.verbose, prog)
		})
	case "p":
		runID := time.Now().Format("20060102-150405")
		return m, m.startPass("Report", func(prog Progress) error {
			return runReport(m.db, m.model, m.reportPath, runID, prog)
		})
	case "a":
		runID := time.Now().Format("20060102-150405")
		return m, m.startPass("Full Pipeline", func(prog Progress) error {
			return runAll(m.db, m.projectRoot, m.model, m.delay, m.maxTools, m.verbose, m.reportPath, runID, prog)
		})
	case "f":
		m.findings = fetchFindings(m.db, "")
		m.findingIdx = 0
		m.findingFilter = ""
		m.findingsOffset = 0
		m.view = viewFindings
		return m, nil
	case "x":
		return m, func() tea.Msg {
			resetDB(m.db)
			return resetCompleteMsg{}
		}
	}

	return m, nil
}

func (m model) startPass(name string, fn func(Progress) error) tea.Cmd {
	return func() tea.Msg {
		// Send start message
		if m.program != nil {
			m.program.Send(passStartMsg{name: name})
		}

		prog := &tuiProgress{program: m.program}
		err := fn(prog)
		return passCompleteMsg{err: err}
	}
}

func (m model) handlePassRunKey(key string) (tea.Model, tea.Cmd) {
	if key == "esc" {
		m.view = viewDashboard
	}
	return m, nil
}

func (m model) View() string {
	if m.showHelp {
		return renderHelp()
	}

	switch m.view {
	case viewDashboard:
		return renderDashboard(m)
	case viewPassRun:
		return renderPassRun(m)
	case viewFindings:
		return renderFindingsList(m)
	case viewFindingDetail:
		return renderFindingDetail(m)
	default:
		return renderDashboard(m)
	}
}

func renderHelp() string {
	help := titleStyle.Render("ReviewBot Help") + "\n\n"
	help += valueStyle.Render("Dashboard") + "\n"
	help += "  " + keyStyle.Render("d") + mutedStyle.Render("  Run discovery (scan filesystem)") + "\n"
	help += "  " + keyStyle.Render("s") + mutedStyle.Render("  Run scan (LLM review per file)") + "\n"
	help += "  " + keyStyle.Render("r") + mutedStyle.Render("  Run relations (build dependency graph)") + "\n"
	help += "  " + keyStyle.Render("t") + mutedStyle.Render("  Run structural (cross-file analysis)") + "\n"
	help += "  " + keyStyle.Render("p") + mutedStyle.Render("  Run report (generate markdown)") + "\n"
	help += "  " + keyStyle.Render("a") + mutedStyle.Render("  Run all passes (full pipeline)") + "\n"
	help += "  " + keyStyle.Render("f") + mutedStyle.Render("  Browse findings") + "\n"
	help += "  " + keyStyle.Render("x") + mutedStyle.Render("  Reset database") + "\n"
	help += "\n"
	help += valueStyle.Render("Findings Browser") + "\n"
	help += "  " + keyStyle.Render("j/k") + mutedStyle.Render("    Navigate up/down") + "\n"
	help += "  " + keyStyle.Render("enter") + mutedStyle.Render("  View detail") + "\n"
	help += "  " + keyStyle.Render("1-4") + mutedStyle.Render("    Filter by severity") + "\n"
	help += "  " + keyStyle.Render("0") + mutedStyle.Render("      Show all") + "\n"
	help += "\n"
	help += valueStyle.Render("Global") + "\n"
	help += "  " + keyStyle.Render("?") + mutedStyle.Render("      Toggle help") + "\n"
	help += "  " + keyStyle.Render("esc") + mutedStyle.Render("    Go back") + "\n"
	help += "  " + keyStyle.Render("q") + mutedStyle.Render("      Quit") + "\n"
	help += "\n"
	help += helpStyle.Render("Press any key to close")
	return help
}

func startTUI(db *sql.DB, projectRoot, modelName, dbPath, reportPath string, delay, maxTools int, verbose bool) error {
	m := newModel(db, projectRoot, modelName, dbPath, reportPath, delay, maxTools, verbose)
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Wire up the program reference for tuiProgress
	go func() {
		// Small delay to let the program start
		time.Sleep(50 * time.Millisecond)
		p.Send(programRefMsg{program: p})
	}()

	_, err := p.Run()
	return err
}

type programRefMsg struct{ program *tea.Program }
