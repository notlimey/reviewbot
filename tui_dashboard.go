package main

import (
	"database/sql"
	"fmt"
	"strings"
)

// dashboardStats holds the live data shown on the dashboard.
type dashboardStats struct {
	filesTotal    int
	filesPending  int
	filesScanned  int
	filesError    int
	filesSkipped  int
	critical      int
	high          int
	medium        int
	low           int
	structural    int
	relations     int
	lastRunID     string
	lastRunStatus string
}

func fetchDashboardStats(db *sql.DB) dashboardStats {
	var s dashboardStats

	// File counts in a single query using conditional aggregation
	db.QueryRow(`SELECT
		COUNT(*),
		COUNT(CASE WHEN status = 'pending' THEN 1 END),
		COUNT(CASE WHEN status = 'scanned' THEN 1 END),
		COUNT(CASE WHEN status = 'error' THEN 1 END),
		COUNT(CASE WHEN status = 'skipped' THEN 1 END)
		FROM files`).Scan(&s.filesTotal, &s.filesPending, &s.filesScanned, &s.filesError, &s.filesSkipped)

	// Finding counts in a single query
	db.QueryRow(`SELECT
		COUNT(CASE WHEN severity = 'critical' THEN 1 END),
		COUNT(CASE WHEN severity = 'high' THEN 1 END),
		COUNT(CASE WHEN severity = 'medium' THEN 1 END),
		COUNT(CASE WHEN severity = 'low' THEN 1 END)
		FROM findings`).Scan(&s.critical, &s.high, &s.medium, &s.low)

	db.QueryRow("SELECT COUNT(*) FROM structural_findings").Scan(&s.structural)
	db.QueryRow("SELECT COUNT(*) FROM relations").Scan(&s.relations)
	db.QueryRow("SELECT run_id, status FROM run_log ORDER BY id DESC LIMIT 1").Scan(&s.lastRunID, &s.lastRunStatus)

	return s
}

func renderDashboard(m model) string {
	var b strings.Builder

	s := m.stats

	// Title
	b.WriteString(titleStyle.Render("ReviewBot"))
	b.WriteString("\n\n")

	// Config
	b.WriteString(mutedStyle.Render("Project: "))
	b.WriteString(valueStyle.Render(m.projectRoot))
	b.WriteString(mutedStyle.Render("  Model: "))
	b.WriteString(valueStyle.Render(m.model))
	b.WriteString(mutedStyle.Render("  DB: "))
	b.WriteString(valueStyle.Render(m.dbPath))
	b.WriteString(mutedStyle.Render("  Delay: "))
	b.WriteString(valueStyle.Render(fmt.Sprintf("%ds", m.delay)))
	b.WriteString("\n\n")

	// File stats
	b.WriteString(mutedStyle.Render("Files  "))
	b.WriteString(valueStyle.Render(fmt.Sprintf("%d", s.filesTotal)))
	b.WriteString(mutedStyle.Render(" total  "))
	if s.filesScanned > 0 {
		b.WriteString(successStyle.Render(fmt.Sprintf("%d", s.filesScanned)))
		b.WriteString(mutedStyle.Render(" scanned  "))
	}
	if s.filesPending > 0 {
		b.WriteString(valueStyle.Render(fmt.Sprintf("%d", s.filesPending)))
		b.WriteString(mutedStyle.Render(" pending  "))
	}
	if s.filesError > 0 {
		b.WriteString(errorStyle.Render(fmt.Sprintf("%d", s.filesError)))
		b.WriteString(mutedStyle.Render(" error  "))
	}
	if s.filesSkipped > 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("%d skipped  ", s.filesSkipped)))
	}
	b.WriteString("\n")

	// Findings
	totalFindings := s.critical + s.high + s.medium + s.low
	b.WriteString(mutedStyle.Render("Findings  "))
	b.WriteString(valueStyle.Render(fmt.Sprintf("%d", totalFindings)))
	b.WriteString(mutedStyle.Render(" total  "))
	if s.critical > 0 {
		b.WriteString(criticalBadge.Render(fmt.Sprintf("%d critical", s.critical)))
		b.WriteString("  ")
	}
	if s.high > 0 {
		b.WriteString(highBadge.Render(fmt.Sprintf("%d high", s.high)))
		b.WriteString("  ")
	}
	if s.medium > 0 {
		b.WriteString(mediumBadge.Render(fmt.Sprintf("%d medium", s.medium)))
		b.WriteString("  ")
	}
	if s.low > 0 {
		b.WriteString(lowBadge.Render(fmt.Sprintf("%d low", s.low)))
		b.WriteString("  ")
	}
	b.WriteString("\n")

	// Structural + relations
	b.WriteString(mutedStyle.Render("Structural  "))
	b.WriteString(valueStyle.Render(fmt.Sprintf("%d", s.structural)))
	b.WriteString(mutedStyle.Render("  Relations  "))
	b.WriteString(valueStyle.Render(fmt.Sprintf("%d", s.relations)))
	b.WriteString("\n")

	// Last run
	if s.lastRunID != "" {
		b.WriteString(mutedStyle.Render("Last run  "))
		b.WriteString(valueStyle.Render(s.lastRunID))
		b.WriteString(mutedStyle.Render("  ("))
		switch s.lastRunStatus {
		case "completed":
			b.WriteString(successStyle.Render(s.lastRunStatus))
		case "failed":
			b.WriteString(errorStyle.Render(s.lastRunStatus))
		default:
			b.WriteString(valueStyle.Render(s.lastRunStatus))
		}
		b.WriteString(mutedStyle.Render(")"))
		b.WriteString("\n")
	}

	b.WriteString("\n")

	// Running status
	if m.running {
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
		b.WriteString(valueStyle.Render(m.runningPass))
		b.WriteString("\n\n")
	}

	// Commands
	b.WriteString(renderCommands(m.running))
	b.WriteString("\n")

	return boxStyle.Render(b.String())
}

func renderCommands(running bool) string {
	if running {
		return helpStyle.Render("[esc] back to progress  [q] quit")
	}
	var b strings.Builder
	b.WriteString(keyStyle.Render("[d]") + mutedStyle.Render(" discover  "))
	b.WriteString(keyStyle.Render("[s]") + mutedStyle.Render(" scan  "))
	b.WriteString(keyStyle.Render("[r]") + mutedStyle.Render(" relations  "))
	b.WriteString(keyStyle.Render("[t]") + mutedStyle.Render(" structural  "))
	b.WriteString(keyStyle.Render("[p]") + mutedStyle.Render(" report\n"))
	b.WriteString(keyStyle.Render("[a]") + mutedStyle.Render(" run all  "))
	b.WriteString(keyStyle.Render("[f]") + mutedStyle.Render(" findings  "))
	b.WriteString(keyStyle.Render("[x]") + mutedStyle.Render(" reset  "))
	b.WriteString(keyStyle.Render("[+/-]") + mutedStyle.Render(" delay  "))
	b.WriteString(keyStyle.Render("[q]") + mutedStyle.Render(" quit  "))
	b.WriteString(keyStyle.Render("[?]") + mutedStyle.Render(" help"))
	return b.String()
}
