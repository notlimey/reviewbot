package main

import (
	"database/sql"
	"encoding/json"
	"strconv"
)

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

// UnmarshalJSON handles LLMs returning numeric fields as strings
// (e.g. "confidence": "0.9" instead of "confidence": 0.9).
func (s *ScanIssue) UnmarshalJSON(data []byte) error {
	var raw struct {
		Category    string          `json:"category"`
		Severity    string          `json:"severity"`
		Confidence  json.RawMessage `json:"confidence"`
		Title       string          `json:"title"`
		Description string          `json:"description"`
		LineStart   json.RawMessage `json:"line_start"`
		LineEnd     json.RawMessage `json:"line_end"`
		Suggestion  string          `json:"suggestion"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	s.Category = raw.Category
	s.Severity = raw.Severity
	s.Title = raw.Title
	s.Description = raw.Description
	s.Suggestion = raw.Suggestion
	s.Confidence = parseFloat(raw.Confidence, 0.5)
	s.LineStart = parseOptionalInt(raw.LineStart)
	s.LineEnd = parseOptionalInt(raw.LineEnd)
	return nil
}

// parseFloat parses a JSON value that may be a number or a string containing a number.
func parseFloat(raw json.RawMessage, fallback float64) float64 {
	if len(raw) == 0 {
		return fallback
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v
		}
	}
	return fallback
}

// parseOptionalInt parses a JSON value that may be a number, a string, or null.
func parseOptionalInt(raw json.RawMessage) *int {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var i int
	if json.Unmarshal(raw, &i) == nil {
		return &i
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if v, err := strconv.Atoi(s); err == nil {
			return &v
		}
	}
	return nil
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

// UnmarshalJSON handles the case where the LLM returns "names" as a single
// string instead of an array of strings.
func (r *ImportRecord) UnmarshalJSON(data []byte) error {
	// Use a raw struct to avoid infinite recursion.
	var raw struct {
		From  string          `json:"from"`
		Names json.RawMessage `json:"names"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.From = raw.From

	if len(raw.Names) == 0 {
		return nil
	}

	// Try []string first (expected case).
	if err := json.Unmarshal(raw.Names, &r.Names); err == nil {
		return nil
	}

	// Fall back to a single string.
	var single string
	if err := json.Unmarshal(raw.Names, &single); err == nil {
		r.Names = []string{single}
		return nil
	}

	// Ignore unparseable names rather than failing the whole file.
	r.Names = nil
	return nil
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
