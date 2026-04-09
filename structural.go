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

const structuralTimeout = 5 * time.Minute

func runStructural(db *sql.DB, projectRoot, model string, maxTools int, delay int, verbose bool) error {
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

	type fileInfo struct {
		id   int64
		path string
	}
	clusters := make(map[string][]fileInfo)
	for rows.Next() {
		var fi fileInfo
		if err := rows.Scan(&fi.id, &fi.path); err != nil {
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
		fmt.Println("No multi-file clusters to analyse.")
		return nil
	}

	fmt.Printf("Analysing %d clusters with structural review...\n", clusterCount)

	reviewed := 0
	for _, dir := range sortedDirs {
		files := clusters[dir]
		reviewed++

		fmt.Printf("  [%d/%d] Cluster: %s (%d files)\n", reviewed, clusterCount, dir, len(files))

		// Build cluster context
		var contextBuilder strings.Builder
		contextBuilder.WriteString(fmt.Sprintf("Cluster: %s\n\nFiles in this cluster:\n", dir))

		var fileIDs []int64
		for _, f := range files {
			fileIDs = append(fileIDs, f.id)

			// Get summary
			var summary sql.NullString
			db.QueryRow("SELECT summary FROM metadata WHERE file_id = ?", f.id).Scan(&summary)

			contextBuilder.WriteString(fmt.Sprintf("\n--- %s ---\n", f.path))
			if summary.Valid {
				contextBuilder.WriteString(fmt.Sprintf("Summary: %s\n", summary.String))
			}

			// Get existing findings for this file
			findingRows, err := db.Query(
				"SELECT severity, category, title FROM findings WHERE file_id = ?", f.id,
			)
			if err == nil {
				for findingRows.Next() {
					var sev, cat, title string
					findingRows.Scan(&sev, &cat, &title)
					contextBuilder.WriteString(fmt.Sprintf("  Finding: [%s/%s] %s\n", sev, cat, title))
				}
				findingRows.Close()
			}
		}

		// Run structural review with tool calling
		issues, err := structuralReview(client, registry, model, contextBuilder.String(), maxTools, verbose)
		if err != nil {
			fmt.Printf("    ✗ error: %v\n", err)
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
				fmt.Printf("    warning: failed to save structural finding: %v\n", err)
			}
		}

		fmt.Printf("    ✓ %d structural issues found\n", len(issues))

		if delay > 0 && reviewed < clusterCount {
			time.Sleep(time.Duration(delay) * time.Second)
		}
	}

	fmt.Printf("Structural review complete: %d clusters analysed\n", reviewed)
	return nil
}

func structuralReview(client *api.Client, registry *ToolRegistry, model, clusterContext string, maxTools int, verbose bool) ([]StructuralIssue, error) {
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
			Options:  map[string]any{"temperature": 0.3, "num_predict": 16384},
		}

		resp, _, _, err := streamLLMChat(client, req, "structural")
		if err != nil {
			return nil, fmt.Errorf("chat error: %w", err)
		}

		if len(resp.Message.ToolCalls) > 0 {
			// Append assistant message
			messages = append(messages, resp.Message)

			for _, tc := range resp.Message.ToolCalls {
				toolCallCount++
				if verbose {
					fmt.Printf("    tool call [%d]: %s(%v)\n", toolCallCount, tc.Function.Name, tc.Function.Arguments)
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
					Options:  map[string]any{"temperature": 0.3, "num_predict": 16384},
				}
				finalResp, _, _, err := streamLLMChat(client, finalReq, "final analysis")
				if err != nil {
					return nil, fmt.Errorf("final chat error: %w", err)
				}
				return parseStructuralResponse(finalResp.Message.Content, verbose)
			}

			continue
		}

		// No tool calls — parse the final response
		return parseStructuralResponse(resp.Message.Content, verbose)
	}
}

func parseStructuralResponse(raw string, verbose bool) ([]StructuralIssue, error) {
	cleaned := cleanJSON(raw)
	if verbose {
		fmt.Printf("    raw structural response: %s\n", cleaned[:min(len(cleaned), 500)])
	}

	var resp StructuralResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		// Try repairing truncated JSON before giving up
		repaired := repairTruncatedJSON(cleaned)
		if err2 := json.Unmarshal([]byte(repaired), &resp); err2 != nil {
			return nil, fmt.Errorf("parse structural response: %w", err)
		}
	}
	return resp.StructuralIssues, nil
}
