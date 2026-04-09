package main

import "database/sql"

// FileRecord represents a row in the files table.
type FileRecord struct {
	ID            int64
	Path          string
	Language      string
	Hash          string
	TokenEstimate int
	Status        string
	ScannedAt     sql.NullTime
}

// Finding represents a row in the findings table.
type Finding struct {
	ID          int64
	FileID      int64
	Pass        string
	Category    string
	Severity    string
	Confidence  float64
	Title       string
	Description string
	LineStart   *int
	LineEnd     *int
	Suggestion  string
	FilePath    string // joined from files table for report queries
}

// Metadata represents a row in the metadata table.
type Metadata struct {
	ID         int64
	FileID     int64
	Exports    string // JSON array
	Imports    string // JSON array
	Interfaces string // JSON array
	Patterns   string // JSON array
	Summary    string
}

// Relation represents a row in the relations table.
type Relation struct {
	ID           int64
	SourceFileID int64
	TargetFileID int64
	RelationType string
	Detail       string
	ClusterID    string
}

// StructuralFinding represents a row in the structural_findings table.
type StructuralFinding struct {
	ID          int64
	ClusterID   string
	FileIDs     string // JSON array of ints
	Category    string
	Severity    string
	Title       string
	Description string
}

// RunLog represents a row in the run_log table.
type RunLog struct {
	ID            int64
	RunID         string
	Status        string
	FilesTotal    int
	FilesScanned  int
	FindingsCount int
}

// ScanResponse is the expected JSON response from the LLM during file scan.
type ScanResponse struct {
	Issues   []ScanIssue  `json:"issues"`
	Metadata ScanMetadata `json:"metadata"`
}

// ScanIssue represents a single issue found during file scan.
type ScanIssue struct {
	Category    string  `json:"category"`
	Severity    string  `json:"severity"`
	Confidence  float64 `json:"confidence"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	LineStart   *int    `json:"line_start"`
	LineEnd     *int    `json:"line_end"`
	Suggestion  string  `json:"suggestion"`
}

// ScanMetadata is the metadata extracted from a file scan.
type ScanMetadata struct {
	Exports    []string       `json:"exports"`
	Imports    []ImportRecord `json:"imports"`
	Interfaces []string       `json:"interfaces"`
	Patterns   []string       `json:"patterns"`
	Summary    string         `json:"summary"`
}

// ImportRecord represents a single import.
type ImportRecord struct {
	From  string   `json:"from"`
	Names []string `json:"names"`
}

// StructuralResponse is the expected JSON from the structural review LLM.
type StructuralResponse struct {
	StructuralIssues []StructuralIssue `json:"structural_issues"`
}

// StructuralIssue is a single structural issue.
type StructuralIssue struct {
	Category      string   `json:"category"`
	Severity      string   `json:"severity"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	AffectedFiles []string `json:"affected_files"`
}
