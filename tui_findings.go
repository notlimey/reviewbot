package main

import (
	"database/sql"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func fetchFindings(db *sql.DB, severityFilter string) []Finding {
	query := `
		SELECT fi.id, f.path, fi.severity, fi.category, fi.confidence,
		       fi.title, fi.description, fi.line_start, fi.line_end, fi.suggestion
		FROM findings fi JOIN files f ON f.id = fi.file_id
	`
	var args []any
	if severityFilter != "" {
		query += " WHERE fi.severity = ?"
		args = append(args, severityFilter)
	}
	query += ` ORDER BY
		CASE fi.severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,
		fi.confidence DESC`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var f Finding
		var lineStart, lineEnd sql.NullInt64
		if err := rows.Scan(&f.ID, &f.FilePath, &f.Severity, &f.Category, &f.Confidence,
			&f.Title, &f.Description, &lineStart, &lineEnd, &f.Suggestion); err != nil {
			continue
		}
		if lineStart.Valid {
			ls := int(lineStart.Int64)
			f.LineStart = &ls
		}
		if lineEnd.Valid {
			le := int(lineEnd.Int64)
			f.LineEnd = &le
		}
		findings = append(findings, f)
	}
	return findings
}

func (m model) handleFindingsKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.view = viewDashboard
	case "up", "k":
		if m.findingIdx > 0 {
			m.findingIdx--
		}
	case "down", "j":
		if m.findingIdx < len(m.findings)-1 {
			m.findingIdx++
		}
	case "enter":
		if len(m.findings) > 0 {
			m.view = viewFindingDetail
		}
	case "1":
		m.findingFilter = toggleFilter(m.findingFilter, "critical")
		m.findings = fetchFindings(m.db, m.findingFilter)
		m.findingIdx = 0
	case "2":
		m.findingFilter = toggleFilter(m.findingFilter, "high")
		m.findings = fetchFindings(m.db, m.findingFilter)
		m.findingIdx = 0
	case "3":
		m.findingFilter = toggleFilter(m.findingFilter, "medium")
		m.findings = fetchFindings(m.db, m.findingFilter)
		m.findingIdx = 0
	case "4":
		m.findingFilter = toggleFilter(m.findingFilter, "low")
		m.findings = fetchFindings(m.db, m.findingFilter)
		m.findingIdx = 0
	case "0":
		m.findingFilter = ""
		m.findings = fetchFindings(m.db, "")
		m.findingIdx = 0
	}
	return m, nil
}

func (m model) handleFindingDetailKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.view = viewFindings
	}
	return m, nil
}

func toggleFilter(current, value string) string {
	if current == value {
		return ""
	}
	return value
}

func renderFindingsList(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Findings"))
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("(%d total)", len(m.findings))))
	b.WriteString("\n")

	// Filter indicators
	filters := []struct {
		key, label, severity string
	}{
		{"1", "critical", "critical"},
		{"2", "high", "high"},
		{"3", "medium", "medium"},
		{"4", "low", "low"},
	}
	for _, f := range filters {
		if m.findingFilter == f.severity {
			b.WriteString(severityBadge(f.severity).Render(fmt.Sprintf(" [%s] %s ", f.key, f.label)))
		} else {
			b.WriteString(mutedStyle.Render(fmt.Sprintf(" [%s] %s ", f.key, f.label)))
		}
	}
	b.WriteString(mutedStyle.Render(" [0] all"))
	b.WriteString("\n\n")

	if len(m.findings) == 0 {
		b.WriteString(mutedStyle.Render("No findings"))
		b.WriteString("\n")
	} else {
		visibleLines := m.height - 8
		if visibleLines < 5 {
			visibleLines = 5
		}

		// Adjust offset to keep selected item visible
		if m.findingIdx < m.findingsOffset {
			m.findingsOffset = m.findingIdx
		}
		if m.findingIdx >= m.findingsOffset+visibleLines {
			m.findingsOffset = m.findingIdx - visibleLines + 1
		}

		end := m.findingsOffset + visibleLines
		if end > len(m.findings) {
			end = len(m.findings)
		}

		for i := m.findingsOffset; i < end; i++ {
			f := m.findings[i]
			cursor := "  "
			if i == m.findingIdx {
				cursor = "> "
			}

			badge := severityBadge(f.Severity).Render(fmt.Sprintf("%-8s", f.Severity))
			title := f.Title
			if len(title) > 60 {
				title = title[:57] + "..."
			}
			path := mutedStyle.Render(f.FilePath)

			if i == m.findingIdx {
				b.WriteString(valueStyle.Render(cursor) + badge + " " + valueStyle.Render(title) + " " + path)
			} else {
				b.WriteString(cursor + badge + " " + title + " " + path)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[j/k] navigate  [enter] detail  [1-4] filter  [0] all  [esc] dashboard"))

	return b.String()
}

func renderFindingDetail(m model) string {
	if m.findingIdx >= len(m.findings) {
		return "No finding selected"
	}

	f := m.findings[m.findingIdx]
	var b strings.Builder

	b.WriteString(titleStyle.Render("Finding Detail"))
	b.WriteString("\n\n")

	b.WriteString(severityBadge(f.Severity).Render(strings.ToUpper(f.Severity)))
	b.WriteString("  ")
	b.WriteString(valueStyle.Render(f.Title))
	b.WriteString("\n\n")

	b.WriteString(mutedStyle.Render("File:       "))
	b.WriteString(f.FilePath)
	if f.LineStart != nil {
		b.WriteString(fmt.Sprintf(" (line %d", *f.LineStart))
		if f.LineEnd != nil && *f.LineEnd != *f.LineStart {
			b.WriteString(fmt.Sprintf("-%d", *f.LineEnd))
		}
		b.WriteString(")")
	}
	b.WriteString("\n")

	b.WriteString(mutedStyle.Render("Category:   "))
	b.WriteString(f.Category)
	b.WriteString("\n")

	b.WriteString(mutedStyle.Render("Confidence: "))
	b.WriteString(fmt.Sprintf("%.0f%%", f.Confidence*100))
	b.WriteString("\n\n")

	b.WriteString(mutedStyle.Render("Description:\n"))
	b.WriteString(f.Description)
	b.WriteString("\n")

	if f.Suggestion != "" {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("Suggestion:\n"))
		b.WriteString(f.Suggestion)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[esc] back to list"))

	return b.String()
}
