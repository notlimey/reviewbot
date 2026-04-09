package main

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

func initDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	if err := createTables(db); err != nil {
		return nil, fmt.Errorf("create tables: %w", err)
	}

	return db, nil
}

func createTables(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS files (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			path            TEXT UNIQUE NOT NULL,
			language        TEXT NOT NULL,
			hash            TEXT NOT NULL,
			token_estimate  INTEGER,
			status          TEXT DEFAULT 'pending',
			scanned_at      DATETIME,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS findings (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			file_id         INTEGER NOT NULL REFERENCES files(id),
			pass            TEXT NOT NULL,
			category        TEXT NOT NULL,
			severity        TEXT NOT NULL,
			confidence      REAL,
			title           TEXT NOT NULL,
			description     TEXT,
			line_start      INTEGER,
			line_end        INTEGER,
			suggestion      TEXT,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS metadata (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			file_id         INTEGER UNIQUE NOT NULL REFERENCES files(id),
			exports         TEXT,
			imports         TEXT,
			interfaces      TEXT,
			patterns        TEXT,
			summary         TEXT,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS relations (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			source_file_id  INTEGER NOT NULL REFERENCES files(id),
			target_file_id  INTEGER NOT NULL REFERENCES files(id),
			relation_type   TEXT NOT NULL,
			detail          TEXT,
			cluster_id      TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS structural_findings (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			cluster_id      TEXT,
			file_ids        TEXT,
			category        TEXT NOT NULL,
			severity        TEXT NOT NULL,
			title           TEXT NOT NULL,
			description     TEXT,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS run_log (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id          TEXT NOT NULL,
			started_at      DATETIME,
			finished_at     DATETIME,
			files_total     INTEGER,
			files_scanned   INTEGER,
			findings_count  INTEGER,
			status          TEXT DEFAULT 'running'
		)`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:40], err)
		}
	}

	return nil
}

func resetDB(db *sql.DB) error {
	tables := []string{"run_log", "structural_findings", "relations", "metadata", "findings", "files"}
	for _, t := range tables {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + t); err != nil {
			return fmt.Errorf("drop table %s: %w", t, err)
		}
	}
	return createTables(db)
}
