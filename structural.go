package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

const (
	structuralTimeout    = 5 * time.Minute
	structuralNumPredict = 8192

	// maxClusterContextChars caps the user message for structural review.
	// Keeps input well within the 32K context window after accounting for
	// the system prompt, tool definitions, and output budget.
	maxClusterContextChars = 20000
)

// fileInfo pairs a file ID with its relative path for cluster grouping.
type fileInfo struct {
	id   int64
	path string
}

func runStructural(db *sql.DB, projectRoot, model string, maxTools int, delay int, verbose bool, prog Progress) error {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}

	client, err := api.ClientFromEnvironment()
	if err != nil {
		return fmt.Errorf("create ollama client: %w", err)
	}

	// Clear old structural findings to prevent duplicates across runs
	if _, err := db.Exec("DELETE FROM structural_findings"); err != nil {
		return fmt.Errorf("clear old structural findings: %w", err)
	}

	// Group scanned files by directory
	rows, err := db.Query("SELECT id, path FROM files WHERE status = 'scanned'")
	if err != nil {
		return fmt.Errorf("query scanned files: %w", err)
	}
	defer rows.Close()

	clusters := make(map[string][]fileInfo)
	for rows.Next() {
		var fi fileInfo
		if err := rows.Scan(&fi.id, &fi.path); err != nil {
			prog.Warn(fmt.Sprintf("scan file row: %v", err))
			continue
		}
		dir := filepath.Dir(fi.path)
		clusters[dir] = append(clusters[dir], fi)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate scanned files: %w", err)
	}

	registry := NewToolRegistry(&ToolContext{DB: db, ProjectRoot: absRoot}, maxTools)

	// Collect and sort cluster keys for deterministic order
	var sortedDirs []string
	for dir, files := range clusters {
		if len(files) >= 2 {
			sortedDirs = append(sortedDirs, dir)
		}
	}
	sort.Strings(sortedDirs)

	clusterCount := len(sortedDirs)
	if clusterCount == 0 {
		prog.Info("No multi-file clusters to analyse.")
		return nil
	}

	prog.StructuralStart(clusterCount)

	reviewed := 0
	for _, dir := range sortedDirs {
		files := clusters[dir]
		reviewed++

		prog.StructuralClusterStart(reviewed, clusterCount, dir, len(files))

		// Build cluster context with priority-based compression.
		// Critical/high findings are always included; medium/low are trimmed
		// if the context exceeds the budget.
		fileIDs, clusterCtx := buildClusterContext(db, dir, files, prog)

		// Run structural review with tool calling
		issues, err := structuralReview(client, registry, model, clusterCtx, maxTools, verbose, prog)
		if err != nil {
			prog.ScanFileError(reviewed, clusterCount, dir, fmt.Sprintf("error: %v", err))
			continue
		}

		// Save structural findings
		fileIDsJSON, _ := json.Marshal(fileIDs)
		for _, issue := range issues {
			_, err := db.Exec(
				`INSERT INTO structural_findings (cluster_id, file_ids, category, severity, title, description)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				dir, string(fileIDsJSON), issue.Category, issue.Severity, issue.Title, issue.Description,
			)
			if err != nil {
				prog.Warn(fmt.Sprintf("failed to save structural finding: %v", err))
			}
		}

		prog.StructuralClusterDone(reviewed, clusterCount, len(issues))

		if delay > 0 && reviewed < clusterCount {
			time.Sleep(time.Duration(delay) * time.Second)
		}
	}

	prog.StructuralComplete(reviewed)
	return nil
}

// clusterFinding is a temporary struct for priority sorting.
type clusterFinding struct {
	sev, cat, title string
	priority        int // 0 = critical, 1 = high, 2 = medium, 3 = low
}

func findingPriority(sev string) int {
	switch sev {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	default:
		return 3
	}
}

// buildClusterContext assembles the user message for a structural review,
// applying priority-based compression when the context exceeds the budget.
func buildClusterContext(db *sql.DB, dir string, files []fileInfo, prog Progress) ([]int64, string) {
	var b strings.Builder
	fmt.Fprintf(&b, "Cluster: %s\n\nFiles in this cluster:\n", dir)

	var fileIDs []int64

	// Collect per-file sections: header + summary (always kept) and findings (prioritised)
	type fileSection struct {
		header   string           // always included
		findings []clusterFinding // sorted by priority
	}
	var sections []fileSection

	for _, f := range files {
		fileIDs = append(fileIDs, f.id)

		var sec fileSection

		// Header + summary (priority 0 — always kept)
		var hdr strings.Builder
		fmt.Fprintf(&hdr, "\n--- %s ---\n", f.path)

		var summary sql.NullString
		db.QueryRow("SELECT summary FROM metadata WHERE file_id = ?", f.id).Scan(&summary)
		if summary.Valid {
			fmt.Fprintf(&hdr, "Summary: %s\n", summary.String)
		}
		sec.header = hdr.String()

		// Findings with priority
		findingRows, err := db.Query(
			"SELECT severity, category, title FROM findings WHERE file_id = ?", f.id,
		)
		if err == nil {
			for findingRows.Next() {
				var sev, cat, title string
				if err := findingRows.Scan(&sev, &cat, &title); err != nil {
					prog.Warn(fmt.Sprintf("scan finding row: %v", err))
					continue
				}
				sec.findings = append(sec.findings, clusterFinding{
					sev: sev, cat: cat, title: title,
					priority: findingPriority(sev),
				})
			}
			findingRows.Close()
		}

		// Sort findings: critical first, low last
		sort.Slice(sec.findings, func(i, j int) bool {
			return sec.findings[i].priority < sec.findings[j].priority
		})

		sections = append(sections, sec)
	}

	// Phase 1: write all headers + all findings
	for _, sec := range sections {
		b.WriteString(sec.header)
		for _, f := range sec.findings {
			fmt.Fprintf(&b, "  Finding: [%s/%s] %s\n", f.sev, f.cat, f.title)
		}
	}

	// Phase 2: if over budget, rebuild with only critical/high findings
	if b.Len() > maxClusterContextChars {
		b.Reset()
		fmt.Fprintf(&b, "Cluster: %s\n\nFiles in this cluster:\n", dir)
		trimmed := 0
		for _, sec := range sections {
			b.WriteString(sec.header)
			for _, f := range sec.findings {
				if f.priority <= 1 { // critical or high
					fmt.Fprintf(&b, "  Finding: [%s/%s] %s\n", f.sev, f.cat, f.title)
				} else {
					trimmed++
				}
			}
		}
		if trimmed > 0 {
			fmt.Fprintf(&b, "\n[%d medium/low findings omitted for context budget]\n", trimmed)
		}
	}

	// Phase 3: if still over budget, drop all findings — just summaries
	if b.Len() > maxClusterContextChars {
		b.Reset()
		fmt.Fprintf(&b, "Cluster: %s\n\nFiles in this cluster:\n", dir)
		for _, sec := range sections {
			b.WriteString(sec.header)
		}
		b.WriteString("\n[All per-file findings omitted for context budget — use tools to explore]\n")
	}

	return fileIDs, b.String()
}

func structuralReview(client *api.Client, registry *ToolRegistry, model, clusterContext string, maxTools int, verbose bool, prog Progress) ([]StructuralIssue, error) {
	systemPrompt := `You are a senior architect reviewing a cluster of related files for cross-cutting issues.
You have access to tools to explore the codebase. Use them to understand relationships.

Look for:
- Inconsistencies in error handling, naming, or patterns across files
- Duplicated logic that should be shared
- Coupling issues or wrong abstraction layers
- Missing validation or error handling at boundaries
- Type mismatches between frontend and backend (TS types vs C# DTOs)

You may use tools to read related files or query existing findings.
When done exploring, provide your analysis.

Return ONLY valid JSON:
{
  "structural_issues": [
    {
      "category": "consistency|duplication|coupling|error_handling|architecture",
      "severity": "critical|high|medium|low",
      "title": "short title",
      "description": "what is wrong across these files and why it matters",
      "affected_files": ["path/to/file1.ts", "path/to/file2.ts"]
    }
  ]
}`

	messages := []api.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: clusterContext},
	}

	tools := registry.ToOllamaTools()
	toolCallCount := 0
	deadline := time.Now().Add(structuralTimeout)

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("structural review timed out after %s", structuralTimeout)
		}

		req := &api.ChatRequest{
			Model:    model,
			Messages: messages,
			Tools:    tools,
			Format:   json.RawMessage(`"json"`),
			Options: map[string]any{"temperature": 0.3, "num_predict": structuralNumPredict, "num_ctx": 32768},
		}

		resp, _, _, err := streamLLMChat(client, req, "structural", prog)
		if err != nil {
			return nil, fmt.Errorf("chat error: %w", err)
		}

		if len(resp.Message.ToolCalls) > 0 {
			// Append assistant message
			messages = append(messages, resp.Message)

			for _, tc := range resp.Message.ToolCalls {
				toolCallCount++
				if verbose {
					prog.Info(fmt.Sprintf("    tool call [%d]: %s(%v)", toolCallCount, tc.Function.Name, tc.Function.Arguments))
				}

				result, err := registry.Execute(tc.Function.Name, tc.Function.Arguments.ToMap())
				if err != nil {
					result = fmt.Sprintf("Error: %s", err.Error())
				}

				messages = append(messages, api.Message{
					Role:    "tool",
					Content: result,
				})
			}

			// Check if we've exceeded max tool calls
			if toolCallCount >= maxTools {
				messages = append(messages, api.Message{
					Role:    "user",
					Content: "You have used all available tool calls. Please provide your final analysis now.",
				})
				// Send one more request without tools to force a response
				finalReq := &api.ChatRequest{
					Model:    model,
					Messages: messages,
					Format:   json.RawMessage(`"json"`),
					Options: map[string]any{"temperature": 0.3, "num_predict": structuralNumPredict, "num_ctx": 32768},
				}
				finalResp, _, _, err := streamLLMChat(client, finalReq, "final analysis", prog)
				if err != nil {
					return nil, fmt.Errorf("final chat error: %w", err)
				}
				return parseStructuralResponse(finalResp.Message.Content, verbose, prog)
			}

			continue
		}

		// No tool calls — parse the final response
		return parseStructuralResponse(resp.Message.Content, verbose, prog)
	}
}

func parseStructuralResponse(raw string, verbose bool, prog Progress) ([]StructuralIssue, error) {
	stripped := cleanJSON(raw)
	if verbose {
		prog.Info(fmt.Sprintf("    raw structural response: %s", stripped[:min(len(stripped), 500)]))
	}

	// Handle empty responses gracefully
	if len(strings.TrimSpace(stripped)) == 0 {
		prog.Warn("empty structural response, returning no issues")
		return nil, nil
	}

	var resp StructuralResponse

	// 1. Try direct parse.
	if json.Unmarshal([]byte(stripped), &resp) == nil {
		return resp.StructuralIssues, nil
	}

	// 2. Try with newline fixing.
	fixed := fixNewlinesInStrings(stripped)
	if json.Unmarshal([]byte(fixed), &resp) == nil {
		return resp.StructuralIssues, nil
	}

	// 3. Try repairing truncated JSON.
	repaired := repairTruncatedJSON(stripped)
	if json.Unmarshal([]byte(repaired), &resp) == nil {
		return resp.StructuralIssues, nil
	}

	// 4. Try newline-fix + repair combined (truncated JSON with raw newlines).
	repairedFixed := repairTruncatedJSON(fixed)
	if err := json.Unmarshal([]byte(repairedFixed), &resp); err != nil {
		return nil, fmt.Errorf("parse structural response: %w", err)
	}
	return resp.StructuralIssues, nil
}
