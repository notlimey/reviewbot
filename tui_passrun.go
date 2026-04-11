package main

import (
	"fmt"
	"strings"
)

func renderPassRun(m model) string {
	var b strings.Builder

	// Header
	b.WriteString(titleStyle.Render("ReviewBot"))
	b.WriteString("  ")
	if m.running {
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
		b.WriteString(valueStyle.Render(m.runningPass))
	} else {
		b.WriteString(successStyle.Render("Done"))
	}
	b.WriteString("\n\n")

	// Progress bar
	if m.passTotal > 0 {
		pct := float64(m.passN) / float64(m.passTotal)
		barWidth := 40
		filled := int(pct * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}

		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		b.WriteString(fmt.Sprintf("  %s %d/%d (%.0f%%)\n", bar, m.passN, m.passTotal, pct*100))
	}

	// Current file + tokens
	if m.running && m.passPath != "" {
		b.WriteString(mutedStyle.Render("  Current: "))
		b.WriteString(valueStyle.Render(m.passPath))
		if m.passTokens > 0 {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  (%d tokens)", m.passTokens)))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Log
	visibleLines := m.height - 10
	if visibleLines < 5 {
		visibleLines = 5
	}

	start := 0
	if len(m.passLog) > visibleLines {
		start = len(m.passLog) - visibleLines
	}

	for _, line := range m.passLog[start:] {
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Footer
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[esc] dashboard  [q] quit"))

	return b.String()
}
