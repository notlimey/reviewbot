package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ollama/ollama/api"
)

const architectureClusterID = "__architecture__"

// runArchitecture performs a project-wide architecture analysis using metrics
// derived from the DB (file counts, dependency graph, finding hotspots) rather
// than reading individual files. This gives the LLM a bird's-eye view that
// per-file and per-directory passes can't provide.
func runArchitecture(db *sql.DB, projectRoot, model string, verbose bool, prog Progress) error {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return fmt.Errorf("create ollama client: %w", err)
	}

	// Clear old architecture findings
	if _, err := db.Exec("DELETE FROM structural_findings WHERE cluster_id = ?", architectureClusterID); err != nil {
		return fmt.Errorf("clear old architecture findings: %w", err)
	}

	prog.Info("Collecting project metrics...")

	summary, err := collectArchitectureMetrics(db)
	if err != nil {
		return fmt.Errorf("collect metrics: %w", err)
	}

	prog.Info(fmt.Sprintf("Project: %d files, %d relations, %d findings across %d directories",
		summary.totalFiles, summary.totalRelations, summary.totalFindings, len(summary.directories)))

	// Load project context
	absRoot, _ := filepath.Abs(projectRoot)
	projectCtx := loadProjectContext(absRoot)

	context := buildArchitecturePrompt(summary, projectCtx)

	if verbose {
		prog.Info(fmt.Sprintf("Architecture context (%d chars):\n%s", len(context), context[:min(len(context), 1000)]))
	}

	// Single LLM call with the full project summary
	issues, err := architectureReview(client, model, context, verbose, prog)
	if err != nil {
		return fmt.Errorf("architecture review: %w", err)
	}

	// Save findings
	for _, issue := range issues {
		_, err := db.Exec(
			`INSERT INTO structural_findings (cluster_id, file_ids, category, severity, title, description)
			 VALUES (?, '[]', ?, ?, ?, ?)`,
			architectureClusterID, issue.Category, issue.Severity, issue.Title, issue.Description,
		)
		if err != nil {
			prog.Warn(fmt.Sprintf("save architecture finding: %v", err))
		}
	}

	prog.Info(fmt.Sprintf("Architecture review complete: %d findings", len(issues)))
	return nil
}

// architectureMetrics holds the aggregated project data for the LLM.
type architectureMetrics struct {
	totalFiles     int
	totalRelations int
	totalFindings  int
	languages      map[string]int       // language → file count
	directories    map[string]dirStats  // directory → stats
	hotspots       []hotspotFile        // files with most findings
	fanIn          []depFile            // most imported files
	fanOut         []depFile            // files that import the most
	cycles         []string             // circular dependency chains (if any)
}

type dirStats struct {
	fileCount    int
	totalTokens  int
	findingCount int
	languages    map[string]int
}

type hotspotFile struct {
	path     string
	findings int
	severity string // worst severity
}

type depFile struct {
	path  string
	count int
}

func collectArchitectureMetrics(db *sql.DB) (*architectureMetrics, error) {
	m := &architectureMetrics{
		languages:   make(map[string]int),
		directories: make(map[string]dirStats),
	}

	// Total counts
	db.QueryRow("SELECT COUNT(*) FROM files WHERE status = 'scanned'").Scan(&m.totalFiles)
	db.QueryRow("SELECT COUNT(*) FROM relations").Scan(&m.totalRelations)
	db.QueryRow("SELECT COUNT(*) FROM findings").Scan(&m.totalFindings)

	// Language breakdown
	langRows, err := db.Query("SELECT language, COUNT(*) FROM files WHERE status = 'scanned' GROUP BY language ORDER BY COUNT(*) DESC")
	if err == nil {
		for langRows.Next() {
			var lang string
			var count int
			if langRows.Scan(&lang, &count) == nil {
				m.languages[lang] = count
			}
		}
		langRows.Close()
	}

	// Directory stats
	dirRows, err := db.Query(`
		SELECT
			f.path, f.language, f.token_estimate,
			(SELECT COUNT(*) FROM findings fi WHERE fi.file_id = f.id) as finding_count
		FROM files f WHERE f.status = 'scanned'
	`)
	if err == nil {
		for dirRows.Next() {
			var path, lang string
			var tokens, findings int
			if dirRows.Scan(&path, &lang, &tokens, &findings) != nil {
				continue
			}
			dir := filepath.Dir(path)
			ds := m.directories[dir]
			ds.fileCount++
			ds.totalTokens += tokens
			ds.findingCount += findings
			if ds.languages == nil {
				ds.languages = make(map[string]int)
			}
			ds.languages[lang]++
			m.directories[dir] = ds
		}
		dirRows.Close()
	}

	// Hotspot files (most findings)
	hotRows, err := db.Query(`
		SELECT f.path, COUNT(*) as cnt,
			MIN(CASE fi.severity
				WHEN 'critical' THEN 0 WHEN 'high' THEN 1
				WHEN 'medium' THEN 2 ELSE 3 END) as worst
		FROM findings fi JOIN files f ON f.id = fi.file_id
		GROUP BY fi.file_id
		ORDER BY cnt DESC LIMIT 10
	`)
	if err == nil {
		for hotRows.Next() {
			var path string
			var count, worst int
			if hotRows.Scan(&path, &count, &worst) != nil {
				continue
			}
			sevName := []string{"critical", "high", "medium", "low"}[worst]
			m.hotspots = append(m.hotspots, hotspotFile{path: path, findings: count, severity: sevName})
		}
		hotRows.Close()
	}

	// Fan-in: most imported files (most depended upon)
	fanInRows, err := db.Query(`
		SELECT f.path, COUNT(*) as cnt
		FROM relations r JOIN files f ON f.id = r.target_file_id
		GROUP BY r.target_file_id
		ORDER BY cnt DESC LIMIT 10
	`)
	if err == nil {
		for fanInRows.Next() {
			var path string
			var count int
			if fanInRows.Scan(&path, &count) == nil {
				m.fanIn = append(m.fanIn, depFile{path: path, count: count})
			}
		}
		fanInRows.Close()
	}

	// Fan-out: files that import the most
	fanOutRows, err := db.Query(`
		SELECT f.path, COUNT(*) as cnt
		FROM relations r JOIN files f ON f.id = r.source_file_id
		GROUP BY r.source_file_id
		ORDER BY cnt DESC LIMIT 10
	`)
	if err == nil {
		for fanOutRows.Next() {
			var path string
			var count int
			if fanOutRows.Scan(&path, &count) == nil {
				m.fanOut = append(m.fanOut, depFile{path: path, count: count})
			}
		}
		fanOutRows.Close()
	}

	// Simple cycle detection: A→B and B→A
	cycleRows, err := db.Query(`
		SELECT DISTINCT f1.path, f2.path
		FROM relations r1
		JOIN relations r2 ON r1.source_file_id = r2.target_file_id AND r1.target_file_id = r2.source_file_id
		JOIN files f1 ON f1.id = r1.source_file_id
		JOIN files f2 ON f2.id = r1.target_file_id
		WHERE f1.path < f2.path
	`)
	if err == nil {
		for cycleRows.Next() {
			var a, b string
			if cycleRows.Scan(&a, &b) == nil {
				m.cycles = append(m.cycles, fmt.Sprintf("%s <-> %s", a, b))
			}
		}
		cycleRows.Close()
	}

	return m, nil
}

func buildArchitecturePrompt(m *architectureMetrics, projectCtx string) string {
	var b strings.Builder

	if projectCtx != "" {
		fmt.Fprintf(&b, "PROJECT CONTEXT:\n%s\n\n---\n\n", projectCtx)
	}

	fmt.Fprintf(&b, "PROJECT OVERVIEW:\n")
	fmt.Fprintf(&b, "Files: %d | Relations: %d | Findings: %d\n\n", m.totalFiles, m.totalRelations, m.totalFindings)

	// Languages
	fmt.Fprintf(&b, "LANGUAGES:\n")
	for lang, count := range m.languages {
		fmt.Fprintf(&b, "  %s: %d files\n", lang, count)
	}

	// Directory structure sorted by file count
	fmt.Fprintf(&b, "\nDIRECTORY STRUCTURE:\n")
	type dirEntry struct {
		path  string
		stats dirStats
	}
	var dirs []dirEntry
	for path, stats := range m.directories {
		dirs = append(dirs, dirEntry{path, stats})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].stats.fileCount > dirs[j].stats.fileCount })
	for _, d := range dirs {
		langs := make([]string, 0)
		for l, c := range d.stats.languages {
			langs = append(langs, fmt.Sprintf("%s:%d", l, c))
		}
		fmt.Fprintf(&b, "  %-40s %2d files  %5d tokens  %2d findings  [%s]\n",
			d.path, d.stats.fileCount, d.stats.totalTokens, d.stats.findingCount, strings.Join(langs, ", "))
	}

	// Hotspots
	if len(m.hotspots) > 0 {
		fmt.Fprintf(&b, "\nFINDING HOTSPOTS (files with most issues):\n")
		for _, h := range m.hotspots {
			fmt.Fprintf(&b, "  %-50s %d findings (worst: %s)\n", h.path, h.findings, h.severity)
		}
	}

	// Dependency graph
	if len(m.fanIn) > 0 {
		fmt.Fprintf(&b, "\nMOST DEPENDED UPON (fan-in):\n")
		for _, f := range m.fanIn {
			fmt.Fprintf(&b, "  %-50s imported by %d files\n", f.path, f.count)
		}
	}
	if len(m.fanOut) > 0 {
		fmt.Fprintf(&b, "\nMOST DEPENDENCIES (fan-out):\n")
		for _, f := range m.fanOut {
			fmt.Fprintf(&b, "  %-50s imports %d files\n", f.path, f.count)
		}
	}

	// Cycles
	if len(m.cycles) > 0 {
		fmt.Fprintf(&b, "\nCIRCULAR DEPENDENCIES:\n")
		for _, c := range m.cycles {
			fmt.Fprintf(&b, "  %s\n", c)
		}
	} else {
		fmt.Fprintf(&b, "\nNo circular dependencies detected.\n")
	}

	return b.String()
}

func architectureReview(client *api.Client, model, context string, verbose bool, prog Progress) ([]StructuralIssue, error) {
	systemPrompt := `You are a senior software architect reviewing a project's overall structure and dependency graph.
You are given aggregated metrics — NOT source code. Focus on architecture-level concerns:

- God files: files with too many dependencies (high fan-out) or too many dependents (high fan-in)
- Circular dependencies: mutual imports that indicate entangled modules
- Finding hotspots: files with concentrated issues suggest poor code quality or excessive complexity
- Directory organization: mixed languages or too many files in one directory
- Code distribution: very large files (high token count) or directories that dominate the codebase
- DRY violations: directories with similar names/patterns that might contain duplicated logic
- Layer violations: presentation code importing data layer directly, or utility modules with too many dependents

Only report issues you can clearly see in the metrics. Do NOT speculate about code you haven't seen.
Be specific — name the files and directories involved.

Return ONLY valid JSON:
{
  "architecture_issues": [
    {
      "category": "god_file|circular_dep|hotspot|organization|distribution|dry|layer_violation",
      "severity": "critical|high|medium|low",
      "title": "short title",
      "description": "what the metrics show and why it matters",
      "affected_files": ["path/to/file1", "path/to/file2"]
    }
  ]
}`

	messages := []api.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: context},
	}

	req := &api.ChatRequest{
		Model:    model,
		Messages: messages,
		Format:   json.RawMessage(`"json"`),
		Options:  map[string]any{"temperature": 0.3, "num_predict": 4096, "num_ctx": 16384},
	}

	resp, _, _, err := streamLLMChat(client, req, "architecture", prog)
	if err != nil {
		return nil, fmt.Errorf("chat error: %w", err)
	}

	return parseArchitectureResponse(resp.Message.Content, verbose, prog)
}

func parseArchitectureResponse(raw string, verbose bool, prog Progress) ([]StructuralIssue, error) {
	stripped := cleanJSON(raw)
	if verbose {
		prog.Info(fmt.Sprintf("    raw architecture response: %s", stripped[:min(len(stripped), 500)]))
	}

	if len(strings.TrimSpace(stripped)) == 0 {
		prog.Warn("empty architecture response")
		return nil, nil
	}

	// Try parsing as architecture_issues wrapper
	var archResp struct {
		Issues []StructuralIssue `json:"architecture_issues"`
	}
	if json.Unmarshal([]byte(stripped), &archResp) == nil {
		return archResp.Issues, nil
	}

	// Try as structural_issues (model might reuse the format)
	var structResp StructuralResponse
	if json.Unmarshal([]byte(stripped), &structResp) == nil {
		return structResp.StructuralIssues, nil
	}

	// Try repair
	repaired := repairTruncatedJSON(stripped)
	if json.Unmarshal([]byte(repaired), &archResp) == nil {
		return archResp.Issues, nil
	}

	return nil, fmt.Errorf("parse architecture response failed")
}
