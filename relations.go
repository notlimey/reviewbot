package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
)

func runRelations(db *sql.DB, prog Progress) error {
	// Build export map: exportName -> []fileID (multiple files can export the same name)
	exportMap := make(map[string][]int64)

	rows, err := db.Query("SELECT file_id, exports FROM metadata WHERE exports IS NOT NULL AND exports != '[]'")
	if err != nil {
		return fmt.Errorf("query metadata exports: %w", err)
	}
	defer rows.Close()

	var totalExports int
	for rows.Next() {
		var fileID int64
		var exportsJSON string
		if err := rows.Scan(&fileID, &exportsJSON); err != nil {
			continue
		}
		var exports []string
		if err := json.Unmarshal([]byte(exportsJSON), &exports); err != nil {
			continue
		}
		for _, name := range exports {
			exportMap[name] = append(exportMap[name], fileID)
			totalExports++
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate exports: %w", err)
	}

	// Build relations from imports
	importRows, err := db.Query(`
		SELECT m.file_id, m.imports, f.path
		FROM metadata m JOIN files f ON f.id = m.file_id
		WHERE m.imports IS NOT NULL AND m.imports != '[]'
	`)
	if err != nil {
		return fmt.Errorf("query metadata imports: %w", err)
	}
	defer importRows.Close()

	type pendingRelation struct {
		sourceFileID int64
		targetFileID int64
		detail       string
		clusterID    string
	}

	var pending []pendingRelation
	for importRows.Next() {
		var fileID int64
		var importsJSON, filePath string
		if err := importRows.Scan(&fileID, &importsJSON, &filePath); err != nil {
			continue
		}
		var imports []ImportRecord
		if err := json.Unmarshal([]byte(importsJSON), &imports); err != nil {
			continue
		}

		dir := filepath.Dir(filePath)

		for _, imp := range imports {
			for _, name := range imp.Names {
				if targetIDs, ok := exportMap[name]; ok {
					for _, targetID := range targetIDs {
						if targetID != fileID {
							pending = append(pending, pendingRelation{
								sourceFileID: fileID,
								targetFileID: targetID,
								detail:       name,
								clusterID:    dir,
							})
						}
					}
				}
			}
		}
	}
	if err := importRows.Err(); err != nil {
		return fmt.Errorf("iterate imports: %w", err)
	}

	// Wrap DELETE + all INSERTs in a single transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM relations"); err != nil {
		return fmt.Errorf("clear relations: %w", err)
	}

	stmt, err := tx.Prepare(
		"INSERT INTO relations (source_file_id, target_file_id, relation_type, detail, cluster_id) VALUES (?, ?, 'imports', ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	totalRelations := 0
	for _, r := range pending {
		if _, err := stmt.Exec(r.sourceFileID, r.targetFileID, r.detail, r.clusterID); err != nil {
			prog.Warn(fmt.Sprintf("insert relation: %v", err))
			continue
		}
		totalRelations++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	prog.RelationsComplete(totalRelations, totalExports)
	return nil
}
