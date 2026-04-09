package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	fs := flag.NewFlagSet(command, flag.ExitOnError)
	modelFlag := fs.String("model", "gemma4:26b", "Ollama model name")
	dbFlag := fs.String("db", "review.db", "SQLite database path")
	delayFlag := fs.Int("delay", 2, "Seconds between LLM calls for thermal management")
	reportFlag := fs.String("report", "review_report.md", "Output report path")
	maxToolsFlag := fs.Int("max-tools", 10, "Max tool calls per structural review turn")
	verboseFlag := fs.Bool("verbose", false, "Print raw LLM responses for debugging")

	// Parse flags — positional args end up in fs.Args()
	fs.Parse(os.Args[2:])

	projectRoot := fs.Arg(0)
	if projectRoot == "" && needsProjectRoot(command) {
		projectRoot = "."
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
		err = runDiscovery(db, projectRoot)
	case "scan":
		err = runScan(db, projectRoot, *modelFlag, *delayFlag, *verboseFlag)
	case "relations":
		err = runRelations(db)
	case "structural":
		err = runStructural(db, projectRoot, *modelFlag, *maxToolsFlag, *delayFlag, *verboseFlag)
	case "report":
		err = runReport(db, *modelFlag, *reportFlag, runID)
	case "status":
		err = runStatus(db)
	case "all":
		err = runAll(db, projectRoot, *modelFlag, *delayFlag, *maxToolsFlag, *verboseFlag, *reportFlag, runID)
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
	case "status", "report", "relations", "reset":
		return false
	default:
		return true
	}
}

func runAll(db *sql.DB, projectRoot, model string, delay, maxTools int, verbose bool, reportPath, runID string) error {
	// Log run start
	if _, err := db.Exec("INSERT INTO run_log (run_id, started_at, status) VALUES (?, ?, 'running')",
		runID, time.Now().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("insert run log: %w", err)
	}

	fmt.Println("=== Pass 1: Discovery ===")
	if err := runDiscovery(db, projectRoot); err != nil {
		return updateRunStatus(db, runID, "failed", err)
	}

	fmt.Println("\n=== Pass 2: File Scan ===")
	if err := runScan(db, projectRoot, model, delay, verbose); err != nil {
		return updateRunStatus(db, runID, "failed", err)
	}

	fmt.Println("\n=== Pass 3: Relations ===")
	if err := runRelations(db); err != nil {
		return updateRunStatus(db, runID, "failed", err)
	}

	fmt.Println("\n=== Pass 4: Structural Review ===")
	if err := runStructural(db, projectRoot, model, maxTools, delay, verbose); err != nil {
		return updateRunStatus(db, runID, "failed", err)
	}

	fmt.Println("\n=== Pass 5: Report ===")
	if err := runReport(db, model, reportPath, runID); err != nil {
		return updateRunStatus(db, runID, "failed", err)
	}

	// Log run completion
	var filesScanned, findingsCount, filesTotal int
	if err := db.QueryRow("SELECT COUNT(*) FROM files WHERE status = 'scanned'").Scan(&filesScanned); err != nil {
		fmt.Printf("  warning: count scanned files: %v\n", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM findings").Scan(&findingsCount); err != nil {
		fmt.Printf("  warning: count findings: %v\n", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM files").Scan(&filesTotal); err != nil {
		fmt.Printf("  warning: count files: %v\n", err)
	}

	if _, err := db.Exec("UPDATE run_log SET finished_at = ?, files_total = ?, files_scanned = ?, findings_count = ?, status = 'completed' WHERE run_id = ?",
		time.Now().Format(time.RFC3339), filesTotal, filesScanned, findingsCount, runID); err != nil {
		fmt.Printf("  warning: update run log: %v\n", err)
	}

	fmt.Println("\n=== Complete ===")
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

func printUsage() {
	fmt.Println(`reviewbot <command> [project_root] [flags]

Commands:
  discover      Pass 1 — scan filesystem, hash files, build work queue
  scan          Pass 2 — LLM review of each pending file (no tools)
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
