package main

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// tuiProgress sends tea.Msg values into the Bubble Tea event loop.
type tuiProgress struct {
	program *tea.Program
}

func (p *tuiProgress) send(msg tea.Msg) {
	if p.program != nil {
		p.program.Send(msg)
	}
}

func (p *tuiProgress) Info(msg string) {
	p.send(passLogMsg{line: msg})
}

func (p *tuiProgress) Warn(msg string) {
	p.send(passLogMsg{line: fmt.Sprintf("  warning: %s", msg)})
}

func (p *tuiProgress) PassHeader(name string) {
	p.send(passLogMsg{line: fmt.Sprintf("=== %s ===", name)})
	p.send(passStartMsg{name: name})
}

func (p *tuiProgress) PassComplete() {
	// Handled by passCompleteMsg from the tea.Cmd return
}

func (p *tuiProgress) DiscoveryComplete(total, newChanged, unchanged int) {
	p.send(passLogMsg{line: fmt.Sprintf("Discovery: %d files, %d new/changed, %d unchanged", total, newChanged, unchanged)})
}

func (p *tuiProgress) ScanStart(total int, model string) {
	p.send(passLogMsg{line: fmt.Sprintf("Scanning %d files with %s", total, model)})
	p.send(passProgressMsg{n: 0, total: total})
}

func (p *tuiProgress) ScanFileStart(n, total int, path string, tokens int, lang string) {
	p.send(passProgressMsg{n: n, total: total, path: path})
	p.send(passTokensMsg{label: "scanning", count: 0})
}

func (p *tuiProgress) ScanFileDone(n, total int, path string, issues int, elapsed time.Duration) {
	p.send(passLogMsg{line: successStyle.Render(fmt.Sprintf("  [%d/%d] %s (%d issues, %s)", n, total, path, issues, elapsed.Round(time.Second)))})
	p.send(passProgressMsg{n: n, total: total})
}

func (p *tuiProgress) ScanFileSkipped(n, total int, path string, reason string) {
	p.send(passLogMsg{line: mutedStyle.Render(fmt.Sprintf("  [%d/%d] %s (skipped: %s)", n, total, path, reason))})
	p.send(passProgressMsg{n: n, total: total})
}

func (p *tuiProgress) ScanFileError(n, total int, path string, errMsg string) {
	p.send(passLogMsg{line: errorStyle.Render(fmt.Sprintf("  [%d/%d] %s (%s)", n, total, path, errMsg))})
	p.send(passProgressMsg{n: n, total: total})
}

func (p *tuiProgress) ScanComplete(scanned int) {
	p.send(passLogMsg{line: fmt.Sprintf("Scan complete: %d files", scanned)})
}

func (p *tuiProgress) Tokens(label string, count int) {
	p.send(passTokensMsg{label: label, count: count})
}

func (p *tuiProgress) TokensDone(label string, count int) {
	p.send(passTokensMsg{label: label, count: count})
}

func (p *tuiProgress) RelationsComplete(relations, exports int) {
	p.send(passLogMsg{line: fmt.Sprintf("Built %d relations from %d exports", relations, exports)})
}

func (p *tuiProgress) StructuralStart(clusterCount int) {
	p.send(passLogMsg{line: fmt.Sprintf("Analysing %d clusters", clusterCount)})
}

func (p *tuiProgress) StructuralClusterStart(n, total int, cluster string, fileCount int) {
	p.send(passProgressMsg{n: n, total: total, path: cluster})
	p.send(passLogMsg{line: fmt.Sprintf("  [%d/%d] %s (%d files)", n, total, cluster, fileCount)})
}

func (p *tuiProgress) StructuralClusterDone(n, total int, issues int) {
	p.send(passLogMsg{line: successStyle.Render(fmt.Sprintf("  %d structural issues found", issues))})
}

func (p *tuiProgress) StructuralComplete(reviewed int) {
	p.send(passLogMsg{line: fmt.Sprintf("Structural review: %d clusters", reviewed)})
}

func (p *tuiProgress) ReportComplete(path string, findings, structural int) {
	p.send(passLogMsg{line: fmt.Sprintf("Report: %s (%d findings, %d structural)", path, findings, structural)})
}
