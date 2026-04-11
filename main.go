package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ollama/ollama/api"
	"golang.org/x/term"
)

func main() {
	// Global flags (parsed before command dispatch)
	fs := flag.NewFlagSet("reviewbot", flag.ExitOnError)
	modelFlag := fs.String("model", "gemma4:26b", "Ollama model name")
	dbFlag := fs.String("db", "review.db", "SQLite database path")
	delayFlag := fs.Int("delay", 2, "Seconds between LLM calls for thermal management")
	reportFlag := fs.String("report", "review_report.md", "Output report path")
	maxToolsFlag := fs.Int("max-tools", 10, "Max tool calls per structural review turn")
	verboseFlag := fs.Bool("verbose", false, "Print raw LLM responses for debugging")
	noTuiFlag := fs.Bool("no-tui", false, "Disable interactive TUI, use plain CLI output")

	// If no args or first arg starts with "-", launch TUI
	if len(os.Args) < 2 || os.Args[1][0] == '-' {
		fs.Parse(os.Args[1:])
		projectRoot := fs.Arg(0)
		if projectRoot == "" {
			projectRoot = "."
		}

		db, err := initDB(*dbFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		// Launch TUI if interactive terminal and not disabled
		if !*noTuiFlag && term.IsTerminal(int(os.Stdin.Fd())) {
			if err := startTUI(db, projectRoot, *modelFlag, *dbFlag, *reportFlag, *delayFlag, *maxToolsFlag, *verboseFlag); err != nil {
				fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		printUsage()
		return
	}

	command := os.Args[1]
	fs.Parse(os.Args[2:])

	projectRoot := fs.Arg(0)
	if projectRoot == "" && needsProjectRoot(command) {
		projectRoot = "."
	}

	prog := &cliProgress{}

	// scan-file is self-contained — no DB needed
	if command == "scan-file" {
		filePath := fs.Arg(0)
		if filePath == "" {
			fmt.Fprintln(os.Stderr, "Usage: reviewbot scan-file <file>")
			os.Exit(1)
		}
		if err := runScanFile(filePath, *modelFlag, *verboseFlag, prog); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	db, err := initDB(*dbFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	runID := time.Now().Format("20060102-150405")

	switch command {
	case "discover":
		err = runDiscovery(db, projectRoot, prog)
	case "scan":
		err = runScan(db, projectRoot, *modelFlag, *delayFlag, *verboseFlag, prog)
	case "relations":
		err = runRelations(db, prog)
	case "structural":
		err = runStructural(db, projectRoot, *modelFlag, *maxToolsFlag, *delayFlag, *verboseFlag, prog)
	case "report":
		err = runReport(db, *modelFlag, *reportFlag, runID, prog)
	case "status":
		err = runStatus(db)
	case "all":
		err = runAll(db, projectRoot, *modelFlag, *delayFlag, *maxToolsFlag, *verboseFlag, *reportFlag, runID, prog)
	case "reset":
		err = resetDB(db)
		if err == nil {
			fmt.Println("Database reset complete.")
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func needsProjectRoot(cmd string) bool {
	switch cmd {
	case "status", "report", "relations", "reset", "scan-file":
		return false
	default:
		return true
	}
}

func runAll(db *sql.DB, projectRoot, model string, delay, maxTools int, verbose bool, reportPath, runID string, prog Progress) error {
	// Log run start
	if _, err := db.Exec("INSERT INTO run_log (run_id, started_at, status) VALUES (?, ?, 'running')",
		runID, time.Now().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("insert run log: %w", err)
	}

	prog.PassHeader("Pass 1: Discovery")
	if err := runDiscovery(db, projectRoot, prog); err != nil {
		return updateRunStatus(db, runID, "failed", err)
	}

	prog.PassHeader("Pass 2: File Scan")
	if err := runScan(db, projectRoot, model, delay, verbose, prog); err != nil {
		return updateRunStatus(db, runID, "failed", err)
	}

	prog.PassHeader("Pass 3: Relations")
	if err := runRelations(db, prog); err != nil {
		return updateRunStatus(db, runID, "failed", err)
	}

	prog.PassHeader("Pass 4: Structural Review")
	if err := runStructural(db, projectRoot, model, maxTools, delay, verbose, prog); err != nil {
		return updateRunStatus(db, runID, "failed", err)
	}

	prog.PassHeader("Pass 5: Report")
	if err := runReport(db, model, reportPath, runID, prog); err != nil {
		return updateRunStatus(db, runID, "failed", err)
	}

	// Log run completion
	var filesScanned, findingsCount, filesTotal int
	if err := db.QueryRow("SELECT COUNT(*) FROM files WHERE status = 'scanned'").Scan(&filesScanned); err != nil {
		prog.Warn(fmt.Sprintf("count scanned files: %v", err))
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM findings").Scan(&findingsCount); err != nil {
		prog.Warn(fmt.Sprintf("count findings: %v", err))
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM files").Scan(&filesTotal); err != nil {
		prog.Warn(fmt.Sprintf("count files: %v", err))
	}

	if _, err := db.Exec("UPDATE run_log SET finished_at = ?, files_total = ?, files_scanned = ?, findings_count = ?, status = 'completed' WHERE run_id = ?",
		time.Now().Format(time.RFC3339), filesTotal, filesScanned, findingsCount, runID); err != nil {
		prog.Warn(fmt.Sprintf("update run log: %v", err))
	}

	prog.PassComplete()
	return nil
}

func updateRunStatus(db *sql.DB, runID, status string, err error) error {
	if _, execErr := db.Exec("UPDATE run_log SET finished_at = ?, status = ? WHERE run_id = ?",
		time.Now().Format(time.RFC3339), status, runID); execErr != nil {
		fmt.Fprintf(os.Stderr, "warning: update run status: %v\n", execErr)
	}
	return err
}

func runStatus(db *sql.DB) error {
	fmt.Println("=== ReviewBot Status ===")
	fmt.Println()

	// File status counts
	rows, err := db.Query("SELECT status, COUNT(*) FROM files GROUP BY status ORDER BY status")
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Println("Files:")
	total := 0
	for rows.Next() {
		var s string
		var c int
		if err := rows.Scan(&s, &c); err != nil {
			return fmt.Errorf("scan status row: %w", err)
		}
		fmt.Printf("  %-10s %d\n", s, c)
		total += c
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate file status rows: %w", err)
	}
	fmt.Printf("  %-10s %d\n", "total", total)

	// Finding counts by severity
	fmt.Println("\nFindings by severity:")
	fRows, err := db.Query("SELECT severity, COUNT(*) FROM findings GROUP BY severity ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END")
	if err == nil {
		defer fRows.Close()
		for fRows.Next() {
			var s string
			var c int
			if err := fRows.Scan(&s, &c); err != nil {
				return fmt.Errorf("scan finding row: %w", err)
			}
			fmt.Printf("  %-10s %d\n", s, c)
		}
		if err := fRows.Err(); err != nil {
			return fmt.Errorf("iterate finding rows: %w", err)
		}
	}

	// Structural findings
	var structCount int
	db.QueryRow("SELECT COUNT(*) FROM structural_findings").Scan(&structCount)
	fmt.Printf("\nStructural findings: %d\n", structCount)

	// Relations
	var relCount int
	db.QueryRow("SELECT COUNT(*) FROM relations").Scan(&relCount)
	fmt.Printf("Relations: %d\n", relCount)

	// Last run
	var lastRunID, lastStatus string
	var lastStarted, lastFinished sql.NullString
	err = db.QueryRow("SELECT run_id, status, started_at, finished_at FROM run_log ORDER BY id DESC LIMIT 1").
		Scan(&lastRunID, &lastStatus, &lastStarted, &lastFinished)
	if err == nil {
		fmt.Printf("\nLast run: %s (status: %s)\n", lastRunID, lastStatus)
		if lastStarted.Valid {
			fmt.Printf("  Started:  %s\n", lastStarted.String)
		}
		if lastFinished.Valid {
			fmt.Printf("  Finished: %s\n", lastFinished.String)
		}
	}

	return nil
}

// runScanFile reviews a single file without touching the project database.
// Useful for quick testing and debugging the scan pipeline.
func runScanFile(filePath, model string, verbose bool, prog Progress) error {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	ext := filepath.Ext(absPath)
	lang := extensions[ext]
	if lang == "" {
		return fmt.Errorf("unsupported file extension: %s", ext)
	}

	tokenEstimate := len(content) / 4
	prog.Info(fmt.Sprintf("File:     %s", filePath))
	prog.Info(fmt.Sprintf("Language: %s", lang))
	prog.Info(fmt.Sprintf("Tokens:   ~%d estimated", tokenEstimate))
	prog.Info(fmt.Sprintf("Model:    %s", model))
	prog.Info("")

	if tokenEstimate > 30000 {
		prog.Warn(fmt.Sprintf("File is very large (~%d tokens). This would be auto-skipped in a full run.", tokenEstimate))
	}

	client, err := api.ClientFromEnvironment()
	if err != nil {
		return fmt.Errorf("create ollama client: %w", err)
	}

	start := time.Now()
	resp, truncated, err := callLLMForScan(client, model, filePath, lang, string(content), tokenEstimate, prog)
	elapsed := time.Since(start)

	if err != nil {
		return fmt.Errorf("LLM error: %w", err)
	}

	if truncated {
		prog.Warn("[response truncated by token limit — attempting repair]")
	}

	parsed, err := parseScanResponse(resp)

	if err != nil && truncated {
		prog.Info("[repair failed — retrying with concise prompt]")
		resp2, _, err2 := callLLMForScanConcise(client, model, filePath, lang, string(content), tokenEstimate, prog)
		if err2 == nil {
			if p, e := parseScanResponse(resp2); e == nil {
				parsed = p
				err = nil
			}
		}
	}

	if err != nil {
		if verbose {
			prog.Info(fmt.Sprintf("Raw response:\n%s", resp))
		}
		return fmt.Errorf("parse error: %w", err)
	}

	// Print results
	prog.Info(fmt.Sprintf("\n=== Results (%s) ===", elapsed.Round(time.Millisecond)))
	prog.Info(fmt.Sprintf("Issues found: %d", len(parsed.Issues)))
	prog.Info(fmt.Sprintf("Summary: %s", parsed.Metadata.Summary))
	prog.Info("")

	if len(parsed.Issues) == 0 {
		prog.Info("No issues found.")
		return nil
	}

	for i, issue := range parsed.Issues {
		lines := ""
		if issue.LineStart != nil {
			lines = fmt.Sprintf(" (L%d", *issue.LineStart)
			if issue.LineEnd != nil && *issue.LineEnd != *issue.LineStart {
				lines += fmt.Sprintf("–%d", *issue.LineEnd)
			}
			lines += ")"
		}
		prog.Info(fmt.Sprintf("[%d] [%s/%s] %s%s", i+1, issue.Severity, issue.Category, issue.Title, lines))
		prog.Info(fmt.Sprintf("    %s", issue.Description))
		if issue.Suggestion != "" {
			prog.Info(fmt.Sprintf("    → %s", issue.Suggestion))
		}
		prog.Info("")
	}

	return nil
}

func printUsage() {
	fmt.Println(`reviewbot <command> [project_root] [flags]

Commands:
  discover      Pass 1 — scan filesystem, hash files, build work queue
  scan          Pass 2 — LLM review of each pending file (no tools)
  scan-file     Review a single file (no DB, quick test)
  relations     Pass 3 — build dependency graph from extracted metadata
  structural    Pass 4 — cross-file analysis with tool calling
  report        Pass 5 — generate markdown report
  status        Show current progress
  all           Run full pipeline (pass 1 through 5)
  reset         Drop all tables and start fresh

Flags:
  -model        Ollama model name (default: "gemma4:26b")
  -db           SQLite database path (default: "review.db")
  -delay        Seconds between LLM calls (default: 2)
  -report       Output report path (default: "review_report.md")
  -max-tools    Max tool calls per structural review (default: 10)
  -verbose      Print raw LLM responses for debugging`)
}
