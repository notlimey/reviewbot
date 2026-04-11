package main

import (
	"fmt"
	"time"
)

// Progress abstracts all user-facing output from the pipeline passes.
// Two implementations: cliProgress (terminal) and tuiProgress (Bubble Tea).
type Progress interface {
	// Generic
	Info(msg string)
	Warn(msg string)

	// Pass headers (for "run all" mode)
	PassHeader(name string)
	PassComplete()

	// Discovery
	DiscoveryComplete(total, newChanged, unchanged int)

	// Scan
	ScanStart(total int, model string)
	ScanFileStart(n, total int, path string, tokens int, lang string)
	ScanFileDone(n, total int, path string, issues int, elapsed time.Duration)
	ScanFileSkipped(n, total int, path string, reason string)
	ScanFileError(n, total int, path string, errMsg string)
	ScanComplete(scanned int)

	// Streaming tokens (used by both scan and structural passes)
	Tokens(label string, count int)
	TokensDone(label string, count int)

	// Relations
	RelationsComplete(relations, exports int)

	// Structural
	StructuralStart(clusterCount int)
	StructuralClusterStart(n, total int, cluster string, fileCount int)
	StructuralClusterDone(n, total int, issues int)
	StructuralComplete(reviewed int)

	// Report
	ReportComplete(path string, findings, structural int)
}

// cliProgress reproduces the exact terminal output of the original CLI.
type cliProgress struct{}

func (p *cliProgress) Info(msg string)                          { fmt.Println(msg) }
func (p *cliProgress) Warn(msg string)                          { fmt.Printf("    warning: %s\n", msg) }
func (p *cliProgress) PassHeader(name string)                   { fmt.Printf("\n=== %s ===\n", name) }
func (p *cliProgress) PassComplete()                            { fmt.Println("\n=== Complete ===") }
func (p *cliProgress) DiscoveryComplete(t, nc, unch int)        { fmt.Printf("Discovery complete: %d files found, %d new/changed, %d unchanged (skipped)\n", t, nc, unch) }
func (p *cliProgress) ScanStart(total int, model string)        { fmt.Printf("Scanning %d pending files with model %s...\n", total, model) }
func (p *cliProgress) ScanFileStart(n, t int, path string, tokens int, lang string) {
	fmt.Printf("  … [%d/%d] %s (~%d tokens, %s)\n", n, t, path, tokens, lang)
}
func (p *cliProgress) ScanFileDone(n, t int, path string, issues int, elapsed time.Duration) {
	fmt.Printf("  ✓ [%d/%d] %s (%d issues, %s)\n", n, t, path, issues, elapsed.Round(time.Second))
}
func (p *cliProgress) ScanFileSkipped(n, t int, path string, reason string) {
	fmt.Printf("  ⊘ [%d/%d] %s (skipped — %s)\n", n, t, path, reason)
}
func (p *cliProgress) ScanFileError(n, t int, path string, errMsg string) {
	fmt.Printf("  ✗ [%d/%d] %s (%s)\n", n, t, path, errMsg)
}
func (p *cliProgress) ScanComplete(scanned int) {
	fmt.Printf("Scan complete: %d files processed\n", scanned)
}
func (p *cliProgress) Tokens(label string, count int) {
	fmt.Printf("\r    … %s: %d tokens", label, count)
}
func (p *cliProgress) TokensDone(label string, count int) {
	fmt.Printf("\r    … %s: %d tokens            \n", label, count)
}
func (p *cliProgress) RelationsComplete(relations, exports int) {
	fmt.Printf("Built %d relations from %d exports\n", relations, exports)
}
func (p *cliProgress) StructuralStart(clusterCount int) {
	fmt.Printf("Analysing %d clusters with structural review...\n", clusterCount)
}
func (p *cliProgress) StructuralClusterStart(n, t int, cluster string, fileCount int) {
	fmt.Printf("  [%d/%d] Cluster: %s (%d files)\n", n, t, cluster, fileCount)
}
func (p *cliProgress) StructuralClusterDone(n, t int, issues int) {
	fmt.Printf("    ✓ %d structural issues found\n", issues)
}
func (p *cliProgress) StructuralComplete(reviewed int) {
	fmt.Printf("Structural review complete: %d clusters analysed\n", reviewed)
}
func (p *cliProgress) ReportComplete(path string, findings, structural int) {
	fmt.Printf("Report written to %s (%d findings, %d structural issues)\n", path, findings, structural)
}
