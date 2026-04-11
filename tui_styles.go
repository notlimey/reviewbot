package main

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorPrimary   = lipgloss.Color("#7C3AED") // purple
	colorSuccess   = lipgloss.Color("#10B981") // green
	colorWarning   = lipgloss.Color("#F59E0B") // amber
	colorDanger    = lipgloss.Color("#EF4444") // red
	colorMuted     = lipgloss.Color("#6B7280") // gray
	colorHighlight = lipgloss.Color("#3B82F6") // blue

	// Severity colors
	colorCritical = lipgloss.Color("#EF4444")
	colorHigh     = lipgloss.Color("#F97316")
	colorMedium   = lipgloss.Color("#F59E0B")
	colorLow      = lipgloss.Color("#6B7280")

	// Box
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Padding(1, 2)

	// Title
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	// Status bar
	statusStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	// Key hint
	keyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorHighlight)

	// Values
	valueStyle = lipgloss.NewStyle().
			Bold(true)

	// Severity badges
	criticalBadge = lipgloss.NewStyle().Bold(true).Foreground(colorCritical)
	highBadge     = lipgloss.NewStyle().Bold(true).Foreground(colorHigh)
	mediumBadge   = lipgloss.NewStyle().Bold(true).Foreground(colorMedium)
	lowBadge      = lipgloss.NewStyle().Bold(true).Foreground(colorLow)

	// Log entries
	successStyle = lipgloss.NewStyle().Foreground(colorSuccess)
	errorStyle   = lipgloss.NewStyle().Foreground(colorDanger)
	mutedStyle   = lipgloss.NewStyle().Foreground(colorMuted)

	// Help bar
	helpStyle = lipgloss.NewStyle().Foreground(colorMuted)
)

func severityBadge(severity string) lipgloss.Style {
	switch severity {
	case "critical":
		return criticalBadge
	case "high":
		return highBadge
	case "medium":
		return mediumBadge
	default:
		return lowBadge
	}
}
