package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
)

// deduplicateFindings removes near-duplicate findings for the same file and
// category, keeping the one with the highest confidence. Two findings are
// considered duplicates when they share the same file_id, category, and their
// titles share a long common prefix (first 40 chars after lowercasing).
func deduplicateFindings(db *sql.DB) (int, error) {
	rows, err := db.Query(`
		SELECT fi1.id
		FROM findings fi1
		JOIN findings fi2 ON fi1.file_id = fi2.file_id
			AND fi1.category = fi2.category
			AND fi1.id > fi2.id
			AND SUBSTR(LOWER(fi1.title), 1, 40) = SUBSTR(LOWER(fi2.title), 1, 40)
		WHERE fi1.confidence <= fi2.confidence
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, id := range ids {
		db.Exec("DELETE FROM findings WHERE id = ?", id)
	}
	return len(ids), nil
}

// copyFile copies src to dst, preserving content.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func runReport(db *sql.DB, model, reportPath, runID string, prog Progress) error {
	// Deduplicate near-identical findings before generating the report
	if removed, err := deduplicateFindings(db); err != nil {
		prog.Warn(fmt.Sprintf("deduplication: %v", err))
	} else if removed > 0 {
		prog.Info(fmt.Sprintf("Deduplicated %d near-duplicate findings", removed))
	}

	var b strings.Builder

	// Header
	b.WriteString("# Code Review Report\n")
	b.WriteString(fmt.Sprintf("Generated: %s\n", time.Now().Format("2006-01-02 15:04")))
	b.WriteString(fmt.Sprintf("Model: %s | Run ID: %s\n\n", model, runID))

	// Summary stats
	var filesScanned, filesTotal int
	if err := db.QueryRow("SELECT COUNT(*) FROM files WHERE status = 'scanned'").Scan(&filesScanned); err != nil {
		return fmt.Errorf("count scanned files: %w", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM files").Scan(&filesTotal); err != nil {
		return fmt.Errorf("count total files: %w", err)
	}

	var totalFindings, criticalCount, highCount, mediumCount, lowCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM findings").Scan(&totalFindings); err != nil {
		return fmt.Errorf("count findings: %w", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM findings WHERE severity = 'critical'").Scan(&criticalCount); err != nil {
		return fmt.Errorf("count critical findings: %w", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM findings WHERE severity = 'high'").Scan(&highCount); err != nil {
		return fmt.Errorf("count high findings: %w", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM findings WHERE severity = 'medium'").Scan(&mediumCount); err != nil {
		return fmt.Errorf("count medium findings: %w", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM findings WHERE severity = 'low'").Scan(&lowCount); err != nil {
		return fmt.Errorf("count low findings: %w", err)
	}

	var structuralCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM structural_findings").Scan(&structuralCount); err != nil {
		return fmt.Errorf("count structural findings: %w", err)
	}

	b.WriteString("## Summary\n")
	b.WriteString("| Metric           | Count |\n")
	b.WriteString("|------------------|-------|\n")
	b.WriteString(fmt.Sprintf("| Files scanned    | %d / %d |\n", filesScanned, filesTotal))
	b.WriteString(fmt.Sprintf("| Total findings   | %d |\n", totalFindings))
	b.WriteString(fmt.Sprintf("| Critical         | %d |\n", criticalCount))
	b.WriteString(fmt.Sprintf("| High             | %d |\n", highCount))
	b.WriteString(fmt.Sprintf("| Medium           | %d |\n", mediumCount))
	b.WriteString(fmt.Sprintf("| Low              | %d |\n", lowCount))
	b.WriteString(fmt.Sprintf("| Structural       | %d |\n", structuralCount))
	b.WriteString("\n---\n\n")

	// Critical & High findings
	b.WriteString("## Critical & High Severity Findings\n\n")
	rows, err := db.Query(`
		SELECT f.path, fi.severity, fi.category, fi.confidence, fi.title, fi.description,
		       fi.line_start, fi.line_end, fi.suggestion
		FROM findings fi JOIN files f ON f.id = fi.file_id
		WHERE fi.severity IN ('critical', 'high')
		ORDER BY
			CASE fi.severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 END,
			fi.confidence DESC
	`)
	if err != nil {
		return fmt.Errorf("query critical/high findings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var path, sev, cat, title, desc, suggestion string
		var confidence float64
		var lineStart, lineEnd sql.NullInt64
		if err := rows.Scan(&path, &sev, &cat, &confidence, &title, &desc, &lineStart, &lineEnd, &suggestion); err != nil {
			return fmt.Errorf("scan critical/high finding: %w", err)
		}

		b.WriteString(fmt.Sprintf("### [%s] %s\n", strings.ToUpper(sev), title))
		lineInfo := ""
		if lineStart.Valid {
			lineInfo = fmt.Sprintf(" (line %d", lineStart.Int64)
			if lineEnd.Valid && lineEnd.Int64 != lineStart.Int64 {
				lineInfo += fmt.Sprintf("-%d", lineEnd.Int64)
			}
			lineInfo += ")"
		}
		b.WriteString(fmt.Sprintf("- **File:** `%s`%s\n", path, lineInfo))
		b.WriteString(fmt.Sprintf("- **Category:** %s | **Confidence:** %.0f%%\n", cat, confidence*100))
		b.WriteString(fmt.Sprintf("- **Issue:** %s\n", desc))
		if suggestion != "" {
			b.WriteString(fmt.Sprintf("- **Fix:** %s\n", suggestion))
		}
		b.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate critical/high findings: %w", err)
	}

	if criticalCount+highCount == 0 {
		b.WriteString("No critical or high severity findings.\n\n")
	}

	b.WriteString("---\n\n")

	// Structural findings
	b.WriteString("## Structural Issues\n\n")
	sRows, err := db.Query(`
		SELECT cluster_id, category, severity, title, description
		FROM structural_findings
		ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END
	`)
	if err != nil {
		return fmt.Errorf("query structural findings: %w", err)
	}
	defer sRows.Close()

	for sRows.Next() {
		var cluster, cat, sev, title, desc string
		if err := sRows.Scan(&cluster, &cat, &sev, &title, &desc); err != nil {
			return fmt.Errorf("scan structural finding: %w", err)
		}

		b.WriteString(fmt.Sprintf("### [%s] %s\n", strings.ToUpper(sev), title))
		b.WriteString(fmt.Sprintf("- **Cluster:** `%s`\n", cluster))
		b.WriteString(fmt.Sprintf("- **Category:** %s\n", cat))
		b.WriteString(fmt.Sprintf("- **Issue:** %s\n\n", desc))
	}
	if err := sRows.Err(); err != nil {
		return fmt.Errorf("iterate structural findings: %w", err)
	}

	if structuralCount == 0 {
		b.WriteString("No structural issues found.\n\n")
	}

	b.WriteString("---\n\n")

	// Medium & Low findings grouped by file
	b.WriteString("## Medium & Low Severity Findings\n\n")
	mlRows, err := db.Query(`
		SELECT f.path, fi.severity, fi.title, fi.description
		FROM findings fi JOIN files f ON f.id = fi.file_id
		WHERE fi.severity IN ('medium', 'low')
		ORDER BY f.path, CASE fi.severity WHEN 'medium' THEN 0 ELSE 1 END
	`)
	if err != nil {
		return fmt.Errorf("query medium/low findings: %w", err)
	}
	defer mlRows.Close()

	currentFile := ""
	for mlRows.Next() {
		var path, sev, title, desc string
		if err := mlRows.Scan(&path, &sev, &title, &desc); err != nil {
			return fmt.Errorf("scan medium/low finding: %w", err)
		}

		if path != currentFile {
			if currentFile != "" {
				b.WriteString("\n")
			}
			b.WriteString(fmt.Sprintf("#### `%s`\n", path))
			currentFile = path
		}
		b.WriteString(fmt.Sprintf("- **[%s]** %s — %s\n", sev, title, desc))
	}
	if err := mlRows.Err(); err != nil {
		return fmt.Errorf("iterate medium/low findings: %w", err)
	}

	if mediumCount+lowCount == 0 {
		b.WriteString("No medium or low severity findings.\n")
	}

	// Back up existing report before overwriting
	if _, err := os.Stat(reportPath); err == nil {
		backupPath := strings.TrimSuffix(reportPath, ".md") + "_" + runID + ".md"
		if copyErr := copyFile(reportPath, backupPath); copyErr != nil {
			prog.Warn(fmt.Sprintf("backup report: %v", copyErr))
		} else {
			prog.Info(fmt.Sprintf("Previous report backed up to %s", backupPath))
		}
	}

	// Write to file
	if err := os.WriteFile(reportPath, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	prog.ReportComplete(reportPath, totalFindings, structuralCount)
	return nil
}
