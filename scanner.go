package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

const llmTimeout = 10 * time.Minute

func runScan(db *sql.DB, projectRoot, model string, delay int, verbose bool) error {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}

	// Reset any files stuck in 'scanning' from a previous crash
	if _, err := db.Exec("UPDATE files SET status = 'pending' WHERE status = 'scanning'"); err != nil {
		return fmt.Errorf("reset scanning status: %w", err)
	}

	client, err := api.ClientFromEnvironment()
	if err != nil {
		return fmt.Errorf("create ollama client: %w", err)
	}

	// Count total pending
	var totalPending int
	if err := db.QueryRow("SELECT COUNT(*) FROM files WHERE status = 'pending'").Scan(&totalPending); err != nil {
		return fmt.Errorf("count pending: %w", err)
	}

	if totalPending == 0 {
		fmt.Println("No pending files to scan.")
		return nil
	}

	fmt.Printf("Scanning %d pending files with model %s...\n", totalPending, model)

	rows, err := db.Query(
		"SELECT id, path, language, token_estimate FROM files WHERE status = 'pending' ORDER BY token_estimate ASC",
	)
	if err != nil {
		return fmt.Errorf("query pending files: %w", err)
	}

	var files []FileRecord
	for rows.Next() {
		var f FileRecord
		if err := rows.Scan(&f.ID, &f.Path, &f.Language, &f.TokenEstimate); err != nil {
			rows.Close()
			return fmt.Errorf("scan row: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate pending files: %w", err)
	}
	rows.Close()

	scanned := 0
	for _, f := range files {
		scanned++

		// Skip very large files
		if f.TokenEstimate > 50000 {
			if _, err := db.Exec("UPDATE files SET status = 'skipped' WHERE id = ?", f.ID); err != nil {
				fmt.Printf("    warning: db error: %v\n", err)
			}
			fmt.Printf("  ⊘ [%d/%d] %s (skipped — too large: ~%d tokens)\n", scanned, totalPending, f.Path, f.TokenEstimate)
			continue
		}

		// Set status to scanning
		if _, err := db.Exec("UPDATE files SET status = 'scanning' WHERE id = ?", f.ID); err != nil {
			fmt.Printf("    warning: db error: %v\n", err)
		}

		// Read file content
		content, err := os.ReadFile(filepath.Join(absRoot, f.Path))
		if err != nil {
			fmt.Printf("  ✗ [%d/%d] %s (read error: %v)\n", scanned, totalPending, f.Path, err)
			if _, execErr := db.Exec("UPDATE files SET status = 'error' WHERE id = ?", f.ID); execErr != nil {
				fmt.Printf("    warning: update status: %v\n", execErr)
			}
			continue
		}

		fmt.Printf("  … [%d/%d] %s (~%d tokens, %s)\n", scanned, totalPending, f.Path, f.TokenEstimate, f.Language)

		start := time.Now()
		resp, truncated, err := callLLMForScan(client, model, f.Path, f.Language, string(content), f.TokenEstimate)
		elapsed := time.Since(start)

		if err != nil {
			fmt.Printf("  ✗ [%d/%d] %s (LLM error: %v)\n", scanned, totalPending, f.Path, err)
			if _, execErr := db.Exec("UPDATE files SET status = 'pending' WHERE id = ?", f.ID); execErr != nil {
				fmt.Printf("    warning: update status: %v\n", execErr)
			}
			continue
		}

		if truncated {
			fmt.Printf("    [truncated by token limit — attempting repair]\n")
		}

		parsed, err := parseScanResponse(resp)
		if err != nil {
			fmt.Printf("  ✗ [%d/%d] %s (parse error: %v)\n", scanned, totalPending, f.Path, err)
			if verbose {
				fmt.Printf("    raw response: %s\n", resp)
			}
			if _, execErr := db.Exec("UPDATE files SET status = 'error' WHERE id = ?", f.ID); execErr != nil {
				fmt.Printf("    warning: update status: %v\n", execErr)
			}
			continue
		}

		// Save findings + metadata + status in a transaction to avoid duplicates on crash
		issueCount, err := saveScanResults(db, f.ID, parsed)
		if err != nil {
			fmt.Printf("  ✗ [%d/%d] %s (db error: %v)\n", scanned, totalPending, f.Path, err)
			if _, execErr := db.Exec("UPDATE files SET status = 'error' WHERE id = ?", f.ID); execErr != nil {
				fmt.Printf("    warning: update status: %v\n", execErr)
			}
			continue
		}

		fmt.Printf("  ✓ [%d/%d] %s (%d issues, %s)\n", scanned, totalPending, f.Path, issueCount, elapsed.Round(time.Second))

		if delay > 0 && scanned < len(files) {
			time.Sleep(time.Duration(delay) * time.Second)
		}
	}

	fmt.Printf("Scan complete: %d files processed\n", scanned)
	return nil
}

func saveScanResults(db *sql.DB, fileID int64, parsed *ScanResponse) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete any existing findings for this file (prevents duplicates on re-scan)
	if _, err := tx.Exec("DELETE FROM findings WHERE file_id = ? AND pass = 'file_scan'", fileID); err != nil {
		return 0, fmt.Errorf("clear old findings: %w", err)
	}

	// Insert findings
	issueCount := 0
	for _, issue := range parsed.Issues {
		_, err := tx.Exec(
			`INSERT INTO findings (file_id, pass, category, severity, confidence, title, description, line_start, line_end, suggestion)
			 VALUES (?, 'file_scan', ?, ?, ?, ?, ?, ?, ?, ?)`,
			fileID, issue.Category, issue.Severity, issue.Confidence,
			issue.Title, issue.Description, issue.LineStart, issue.LineEnd, issue.Suggestion,
		)
		if err != nil {
			return 0, fmt.Errorf("insert finding: %w", err)
		}
		issueCount++
	}

	// Save metadata
	exportsJSON, _ := json.Marshal(parsed.Metadata.Exports)
	importsJSON, _ := json.Marshal(parsed.Metadata.Imports)
	interfacesJSON, _ := json.Marshal(parsed.Metadata.Interfaces)
	patternsJSON, _ := json.Marshal(parsed.Metadata.Patterns)

	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO metadata (file_id, exports, imports, interfaces, patterns, summary)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		fileID, string(exportsJSON), string(importsJSON), string(interfacesJSON),
		string(patternsJSON), parsed.Metadata.Summary,
	); err != nil {
		return 0, fmt.Errorf("insert metadata: %w", err)
	}

	// Mark scanned
	if _, err := tx.Exec("UPDATE files SET status = 'scanned', scanned_at = ? WHERE id = ?",
		time.Now().Format(time.RFC3339), fileID); err != nil {
		return 0, fmt.Errorf("update file status: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return issueCount, nil
}

// callLLMForScan streams the response from the LLM, printing a live token counter.
// Returns the response text, whether it was truncated by the token limit, and any error.
func callLLMForScan(client *api.Client, model, path, language, content string, inputTokens int) (string, bool, error) {
	prompt := fmt.Sprintf(`Review this %s file. Report ONLY real bugs, security issues, or performance problems. Skip style nits.

IMPORTANT: Be extremely concise. Each field value should be 1-2 sentences max. Do NOT repeat code. Do NOT explain obvious things. The entire response must be compact JSON.

Return ONLY this JSON structure:
{"issues":[{"category":"bug|security|perf|style","severity":"critical|high|medium|low","confidence":0.0-1.0,"title":"short title","description":"what is wrong","line_start":null,"line_end":null,"suggestion":"how to fix"}],"metadata":{"exports":["names"],"imports":[{"from":"path","names":["names"]}],"interfaces":["names"],"patterns":["label"],"summary":"one sentence"}}

If no issues: {"issues":[],"metadata":{"exports":[],"imports":[],"interfaces":[],"patterns":[],"summary":"one sentence"}}

File: %s
`+"```%s\n%s\n```", language, path, language, content)

	// Scale token budget: base of 2048 + 3x input tokens, capped at 16384.
	// A well-formed response should be much smaller than the input.
	maxTokens := 2048 + inputTokens*3
	if maxTokens > 16384 {
		maxTokens = 16384
	}

	stream := true
	req := &api.ChatRequest{
		Model: model,
		Messages: []api.Message{
			{Role: "user", Content: prompt},
		},
		Stream:  &stream,
		Format:  json.RawMessage(`"json"`),
		Options: map[string]any{"temperature": 0.3, "num_predict": maxTokens},
	}

	var response strings.Builder
	var lastErr error

	for attempt := range 3 {
		response.Reset()
		tokenCount := 0
		truncated := false

		cancelled := false
		ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
		lastErr = client.Chat(ctx, req, func(resp api.ChatResponse) error {
			// Stop accumulating after we've cancelled — buffered
			// callbacks may still arrive.
			if cancelled {
				return nil
			}
			if resp.Message.Content != "" {
				response.WriteString(resp.Message.Content)
				tokenCount++
				if tokenCount%10 == 0 {
					fmt.Printf("\r    … generating: %d tokens", tokenCount)
				}
			}
			if resp.Done && resp.DoneReason == "length" {
				truncated = true
			}
			// Hard cutoff — cancel the stream if we've exceeded our budget.
			// Ollama's JSON mode can ignore num_predict to complete JSON.
			if tokenCount >= maxTokens {
				truncated = true
				cancelled = true
				cancel()
			}
			return nil
		})
		cancel()

		// If we cancelled due to our hard cutoff, treat as success with truncation
		if lastErr == nil || (truncated && response.Len() > 0) {
			fmt.Printf("\r    … generated %d tokens            \n", tokenCount)
			return response.String(), truncated, nil
		}
		fmt.Printf("\n    retry %d/3 for %s: %v\n", attempt+1, path, lastErr)
		time.Sleep(5 * time.Second)
	}

	return "", false, fmt.Errorf("after 3 retries: %w", lastErr)
}

// streamLLMChat streams an LLM chat request with a live token counter.
// Returns the accumulated text, the final ChatResponse (for tool calls), whether truncated, and any error.
const streamHardLimit = 16384

func streamLLMChat(client *api.Client, req *api.ChatRequest, label string) (api.ChatResponse, string, bool, error) {
	stream := true
	req.Stream = &stream

	var response strings.Builder
	tokenCount := 0
	truncated := false
	var finalResp api.ChatResponse

	cancelled := false
	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	err := client.Chat(ctx, req, func(r api.ChatResponse) error {
		if cancelled {
			return nil
		}
		finalResp = r
		if r.Message.Content != "" {
			response.WriteString(r.Message.Content)
			tokenCount++
			if tokenCount%10 == 0 {
				fmt.Printf("\r    … %s: %d tokens", label, tokenCount)
			}
		}
		if r.Done && r.DoneReason == "length" {
			truncated = true
		}
		if tokenCount >= streamHardLimit {
			truncated = true
			cancelled = true
			cancel()
		}
		return nil
	})
	cancel()

	if tokenCount > 0 {
		fmt.Printf("\r    … %s: %d tokens            \n", label, tokenCount)
	}

	// Put the accumulated content into the final response message
	finalResp.Message.Content = response.String()

	// If we cancelled due to our hard cutoff, ignore the context error
	if truncated && response.Len() > 0 {
		err = nil
	}

	return finalResp, response.String(), truncated, err
}

func parseScanResponse(raw string) (*ScanResponse, error) {
	stripped := cleanJSON(raw)
	var resp ScanResponse

	// 1. Try direct parse.
	if json.Unmarshal([]byte(stripped), &resp) == nil {
		return &resp, nil
	}

	// 2. Try with newline fixing (LLM sometimes puts raw newlines in string values).
	fixed := fixNewlinesInStrings(stripped)
	if json.Unmarshal([]byte(fixed), &resp) == nil {
		return &resp, nil
	}

	// 3. Try repairing truncated JSON (on the stripped version, not the
	//    newline-fixed version, so repair sees the original structure).
	repaired := repairTruncatedJSON(stripped)
	if json.Unmarshal([]byte(repaired), &resp) == nil {
		return &resp, nil
	}

	// 4. Last resort: extract just the issues array.
	if partial := extractPartialScanResponse(repaired); partial != nil {
		return partial, nil
	}

	return nil, fmt.Errorf("unmarshal: all parse strategies failed")
}

// extractPartialScanResponse attempts to salvage a partial response by
// extracting just the issues array from a malformed JSON object.
func extractPartialScanResponse(s string) *ScanResponse {
	// Find "issues" as a structural key (not inside a string value).
	idx := findStructuralKey(s, "issues")
	if idx < 0 {
		return nil
	}

	// Find the start of the array after "issues":
	rest := s[idx:]
	rest = strings.TrimLeft(rest, " \t\n\r:")
	if len(rest) == 0 || rest[0] != '[' {
		return nil
	}

	// Find the matching ']'
	depth := 0
	inStr := false
	esc := false
	end := -1
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if esc {
			esc = false
			continue
		}
		if inStr {
			if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				end = i + 1
			}
		}
		if end >= 0 {
			break
		}
	}

	if end < 0 {
		// Array wasn't closed — repair it
		arr := repairTruncatedJSON(rest)
		var issues []ScanIssue
		if err := json.Unmarshal([]byte(arr), &issues); err != nil {
			return nil
		}
		return &ScanResponse{Issues: issues}
	}

	var issues []ScanIssue
	if err := json.Unmarshal([]byte(rest[:end]), &issues); err != nil {
		return nil
	}
	return &ScanResponse{Issues: issues}
}

// cleanJSON strips markdown fences and conversational text around JSON.
// It does NOT fix newlines or repair truncation — those are separate steps.
func cleanJSON(s string) string {
	s = strings.TrimSpace(s)

	// Strip markdown fences
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	// If the LLM prepended conversational text before the JSON, find the first '{'
	if idx := strings.Index(s, "{"); idx > 0 {
		s = s[idx:]
	}

	// If the LLM appended text after the JSON, find the structural closing '}'.
	// We must walk the JSON properly to avoid cutting at a '}' inside a string.
	if end := findStructuralEnd(s); end >= 0 && end < len(s)-1 {
		s = s[:end+1]
	}

	return s
}

// findStructuralEnd walks the string tracking JSON structure and returns the
// position of the outermost closing bracket/brace, ignoring those inside strings.
// Uses a stack to properly match '{' with '}' and '[' with ']'.
// Returns -1 if the top-level structure is never closed (truncated JSON).
func findStructuralEnd(s string) int {
	inString := false
	escaped := false
	var stack []byte
	lastClose := -1

	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) > 0 && stack[len(stack)-1] == c {
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 {
				lastClose = i
			}
		}
	}
	return lastClose
}

// findStructuralKey searches for a JSON key by name at any nesting depth,
// skipping occurrences inside string values. Returns the position just after
// the closing quote of the key, or -1 if not found.
func findStructuralKey(s string, key string) int {
	target := `"` + key + `"`
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		// Outside a string: check if this position starts our key
		if c == '"' && i+len(target) <= len(s) && s[i:i+len(target)] == target {
			afterKey := i + len(target)
			// Verify it's followed by ':' (whitespace allowed)
			rest := strings.TrimLeft(s[afterKey:], " \t\n\r")
			if len(rest) > 0 && rest[0] == ':' {
				return afterKey
			}
		}
		if c == '"' {
			inString = true
		}
	}
	return -1
}

// repairTruncatedJSON attempts to close an incomplete JSON object that was
// cut off by the token limit. It walks the string tracking JSON structure,
// trims back to the last valid structural point, then appends closing chars.
func repairTruncatedJSON(s string) string {
	s = strings.TrimSpace(s)

	if json.Valid([]byte(s)) {
		return s
	}

	// Walk the string to find the last position where JSON structure was valid.
	// Track: are we in a string? What braces/brackets are open?
	inString := false
	escaped := false
	var stack []byte          // tracks expected closing chars: } or ]
	lastStructuralPos := 0    // last position after a complete structural token

	for i := 0; i < len(s); i++ {
		c := s[i]

		if escaped {
			escaped = false
			continue
		}

		if inString {
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
				lastStructuralPos = i + 1
			}
			continue
		}

		// Outside a string
		switch c {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
			lastStructuralPos = i + 1
		case '[':
			stack = append(stack, ']')
			lastStructuralPos = i + 1
		case '}', ']':
			if len(stack) > 0 && stack[len(stack)-1] == c {
				stack = stack[:len(stack)-1]
			}
			lastStructuralPos = i + 1
		case ',', ':':
			lastStructuralPos = i + 1
		}
	}

	// If we ended inside a string or after a dangling colon/comma,
	// trim back to the last good structural position.
	trimmed := s
	if inString || lastStructuralPos < len(s) {
		trimmed = s[:lastStructuralPos]
	}

	// Remove trailing colons (key with no value) or commas.
	// Also remove a dangling key like `"key":` by trimming back to before the key.
	for {
		trimmed = strings.TrimRight(trimmed, " \t\n\r")
		if len(trimmed) == 0 {
			break
		}
		last := trimmed[len(trimmed)-1]
		if last == ',' {
			trimmed = trimmed[:len(trimmed)-1]
			continue
		}
		if last == ':' {
			// Dangling colon means we have `"key":` — remove the key too
			trimmed = trimmed[:len(trimmed)-1] // remove ':'
			trimmed = strings.TrimRight(trimmed, " \t\n\r")
			// Now remove the quoted key by walking backwards to find the
			// matching opening quote, properly skipping escaped quotes.
			if len(trimmed) > 0 && trimmed[len(trimmed)-1] == '"' {
				pos := len(trimmed) - 2 // skip the closing quote
				for pos >= 0 {
					if trimmed[pos] == '"' {
						// Count preceding backslashes to check if escaped
						bs := 0
						for j := pos - 1; j >= 0 && trimmed[j] == '\\'; j-- {
							bs++
						}
						if bs%2 == 0 {
							// Not escaped — this is the opening quote
							trimmed = trimmed[:pos]
							break
						}
					}
					pos--
				}
			}
			trimmed = strings.TrimRight(trimmed, " \t\n\r")
			// Remove trailing comma left before the removed key
			if len(trimmed) > 0 && trimmed[len(trimmed)-1] == ',' {
				trimmed = trimmed[:len(trimmed)-1]
			}
			continue
		}
		break
	}

	// Re-count the stack from the trimmed string
	stack = stack[:0]
	inString = false
	escaped = false
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) > 0 && stack[len(stack)-1] == c {
				stack = stack[:len(stack)-1]
			}
		}
	}

	// Append closing characters in reverse order
	var closing strings.Builder
	for i := len(stack) - 1; i >= 0; i-- {
		closing.WriteByte(stack[i])
	}

	return trimmed + closing.String()
}

func fixNewlinesInStrings(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		c := s[i]

		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}

		if c == '\\' && inString {
			b.WriteByte(c)
			escaped = true
			continue
		}

		if c == '"' {
			inString = !inString
			b.WriteByte(c)
			continue
		}

		if inString && c == '\n' {
			b.WriteString("\\n")
			continue
		}
		if inString && c == '\r' {
			// If followed by \n, skip — the \n case handles the newline.
			// Standalone \r (classic Mac line endings) gets escaped.
			if i+1 < len(s) && s[i+1] == '\n' {
				continue
			}
			b.WriteString("\\n")
			continue
		}
		if inString && c == '\t' {
			b.WriteString("\\t")
			continue
		}

		b.WriteByte(c)
	}

	return b.String()
}
