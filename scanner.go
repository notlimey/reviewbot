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

const (
	llmTimeout        = 10 * time.Minute
	scanNumPredictMin = 4096  // output limit for small files
	scanNumPredictMax = 16384 // output limit for large files
)

// Fixed context tiers. Using a small set of fixed values (instead of per-file
// dynamic sizing) lets Ollama reuse the KV cache across files in the same tier.
// This dramatically speeds up inference because the system prompt prefix only
// needs to be computed once per tier.
const (
	ctxTierSmall  = 8192  // files up to ~3K tokens
	ctxTierMedium = 16384 // files up to ~7K tokens
	ctxTierLarge  = 32768 // files up to ~15K tokens
	ctxTierXLarge = 49152 // files up to ~30K tokens
)

// scanContextTier returns a fixed num_ctx tier for the given token estimate.
func scanContextTier(tokenEstimate, numPredict int) int {
	needed := tokenEstimate + numPredict + 1024
	switch {
	case needed <= ctxTierSmall:
		return ctxTierSmall
	case needed <= ctxTierMedium:
		return ctxTierMedium
	case needed <= ctxTierLarge:
		return ctxTierLarge
	default:
		return ctxTierXLarge
	}
}

// scanNumPredict scales the output token budget based on estimated input size.
// Larger files produce more findings & metadata and need more room to respond.
func scanNumPredict(tokenEstimate int) int {
	switch {
	case tokenEstimate > 20000:
		return scanNumPredictMax
	case tokenEstimate > 8000:
		return 8192
	default:
		return scanNumPredictMin
	}
}

func runScan(db *sql.DB, projectRoot, model string, delay int, verbose bool, prog Progress) error {
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
		prog.Info("No pending files to scan.")
		return nil
	}

	prog.ScanStart(totalPending, model)

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
		result := scanFile(scanFileInput{
			db:            db,
			client:        client,
			absRoot:       absRoot,
			model:         model,
			file:          f,
			n:             scanned,
			total:         totalPending,
			verbose:       verbose,
			prog:          prog,
		})

		switch result.status {
		case scanSkipped:
			// already handled inside scanFile
		case scanRetryLater:
			// LLM connection error — leave as pending for next run
			if _, execErr := db.Exec("UPDATE files SET status = 'pending' WHERE id = ?", f.ID); execErr != nil {
				prog.Warn(fmt.Sprintf("update status: %v", execErr))
			}
		case scanError:
			if _, execErr := db.Exec("UPDATE files SET status = 'error' WHERE id = ?", f.ID); execErr != nil {
				prog.Warn(fmt.Sprintf("update status: %v", execErr))
			}
		case scanOK:
			prog.ScanFileDone(scanned, totalPending, f.Path, result.issueCount, result.elapsed)
		}

		if delay > 0 && scanned < len(files) {
			time.Sleep(time.Duration(delay) * time.Second)
		}
	}

	prog.ScanComplete(scanned)
	return nil
}

// ---------------------------------------------------------------------------
// Per-file scan pipeline: pre-hook → LLM call → post-hook (recover + filter)
// ---------------------------------------------------------------------------

type scanStatus int

const (
	scanOK         scanStatus = iota
	scanSkipped               // file too large, already handled
	scanRetryLater            // LLM unavailable, leave as pending
	scanError                 // parse/db error, mark as error
)

type scanFileInput struct {
	db      *sql.DB
	client  *api.Client
	absRoot string
	model   string
	file    FileRecord
	n       int
	total   int
	verbose bool
	prog    Progress
}

type scanFileResult struct {
	status     scanStatus
	issueCount int
	elapsed    time.Duration
}

// scanFile runs the full pre-hook → LLM → post-hook pipeline for one file.
func scanFile(in scanFileInput) scanFileResult {
	f := in.file

	// ── Pre-hook: validate inputs ──────────────────────────────────────
	if f.TokenEstimate > 30000 {
		if _, err := in.db.Exec("UPDATE files SET status = 'skipped' WHERE id = ?", f.ID); err != nil {
			in.prog.Warn(fmt.Sprintf("db error: %v", err))
		}
		_, _ = in.db.Exec(
			`INSERT INTO findings (file_id, pass, category, severity, confidence, title, description, line_start, line_end, suggestion)
			 VALUES (?, 'file_scan', 'style', 'medium', 1.0,
			         'File too large for automated review',
			         ?, NULL, NULL,
			         'Consider splitting this file into smaller, focused modules.')`,
			f.ID,
			fmt.Sprintf("At ~%d estimated tokens this file exceeds the review limit. Large files are harder to reason about and often indicate that the module has too many responsibilities.", f.TokenEstimate),
		)
		in.prog.ScanFileSkipped(in.n, in.total, f.Path, fmt.Sprintf("too large: ~%d tokens (finding created)", f.TokenEstimate))
		return scanFileResult{status: scanSkipped}
	}

	if _, err := in.db.Exec("UPDATE files SET status = 'scanning' WHERE id = ?", f.ID); err != nil {
		in.prog.Warn(fmt.Sprintf("db error: %v", err))
	}

	content, err := os.ReadFile(filepath.Join(in.absRoot, f.Path))
	if err != nil {
		in.prog.ScanFileError(in.n, in.total, f.Path, fmt.Sprintf("read error: %v", err))
		return scanFileResult{status: scanError}
	}

	in.prog.ScanFileStart(in.n, in.total, f.Path, f.TokenEstimate, f.Language)

	// ── LLM call ───────────────────────────────────────────────────────
	start := time.Now()
	resp, truncated, err := callLLMForScan(in.client, in.model, f.Path, f.Language, string(content), f.TokenEstimate, in.prog)
	elapsed := time.Since(start)

	if err != nil {
		in.prog.ScanFileError(in.n, in.total, f.Path, fmt.Sprintf("LLM error: %v", err))
		return scanFileResult{status: scanRetryLater}
	}

	// ── Post-hook: recover, filter, save ───────────────────────────────
	parsed, err := recoverScanResponse(recoverCtx{
		client:        in.client,
		model:         in.model,
		path:          f.Path,
		language:      f.Language,
		content:       string(content),
		tokenEstimate: f.TokenEstimate,
		rawResp:       resp,
		truncated:     truncated,
		verbose:       in.verbose,
		prog:          in.prog,
	})
	if err != nil {
		in.prog.ScanFileError(in.n, in.total, f.Path, fmt.Sprintf("parse error: %v", err))
		if in.verbose {
			in.prog.Info(fmt.Sprintf("    raw response: %s", resp))
		}
		return scanFileResult{status: scanError}
	}

	issueCount, err := saveScanResults(in.db, f.ID, parsed)
	if err != nil {
		in.prog.ScanFileError(in.n, in.total, f.Path, fmt.Sprintf("db error: %v", err))
		return scanFileResult{status: scanError}
	}

	return scanFileResult{status: scanOK, issueCount: issueCount, elapsed: elapsed}
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

// ---------------------------------------------------------------------------
// Recovery pipeline
// ---------------------------------------------------------------------------
//
// When the LLM returns malformed output, we run a sequence of increasingly
// aggressive recovery steps. Each step is tried in order; the first one that
// produces a valid *ScanResponse wins.
//
//   Step 1: parseScanResponse (direct parse → newline fix → truncation repair
//           → newline+repair → partial extraction)
//   Step 2: if truncated, re-prompt with concise system prompt + larger budget
//   Step 3: give up and return the parse error

// recoverCtx carries everything needed by the recovery pipeline so we don't
// have to thread a dozen parameters through each step.
type recoverCtx struct {
	client        *api.Client
	model         string
	path          string
	language      string
	content       string
	tokenEstimate int
	rawResp       string
	truncated     bool
	verbose       bool
	prog          Progress
}

// recoverScanResponse runs the recovery pipeline and returns a post-filtered
// *ScanResponse or an error if all steps fail.
func recoverScanResponse(rc recoverCtx) (*ScanResponse, error) {
	if rc.truncated {
		rc.prog.Info("    [step 1] truncated response — attempting parse + repair")
	}

	// Step 1: multi-strategy parse (5 sub-strategies inside parseScanResponse)
	parsed, err := parseScanResponse(rc.rawResp)
	if err == nil {
		return postFilter(parsed, rc.prog), nil
	}

	// Step 2: if truncated, retry with concise prompt
	if rc.truncated {
		rc.prog.Info("    [step 2] repair failed — retrying with concise prompt")
		resp2, _, err2 := callLLMForScanConcise(
			rc.client, rc.model, rc.path, rc.language,
			rc.content, rc.tokenEstimate, rc.prog,
		)
		if err2 == nil {
			if p, e := parseScanResponse(resp2); e == nil {
				return postFilter(p, rc.prog), nil
			}
		}
	}

	// Step 3: all recovery failed
	return nil, fmt.Errorf("all recovery steps failed: %w", err)
}

// postFilter applies the finding filter and logs how many were removed.
func postFilter(parsed *ScanResponse, prog Progress) *ScanResponse {
	before := len(parsed.Issues)
	parsed.Issues = filterFindings(parsed.Issues)
	if filtered := before - len(parsed.Issues); filtered > 0 {
		prog.Info(fmt.Sprintf("    filtered %d low-signal findings", filtered))
	}
	return parsed
}

// scanSystemPrompt is the static portion of the review prompt. It defines the
// role, rules, and output format. Separated from per-file content so that
// Ollama can cache the KV state for this prefix across files.
const scanSystemPrompt = `You are a senior code reviewer. Report ONLY real bugs, security issues, or performance problems. Skip style nits.

Be extremely concise. Each field value should be 1-2 sentences max. Do NOT repeat code. Do NOT explain obvious things. The entire response must be compact JSON.

CONFIDENCE GUIDELINES — be honest about certainty:
- 0.9-1.0: You can see the bug directly in this file with no ambiguity (e.g. null deref, SQL injection, off-by-one)
- 0.7-0.8: Very likely an issue but depends on how callers use this code or on runtime conditions
- 0.5-0.6: Possible issue that depends on context outside this file (other files, configuration, etc.)
- Below 0.5: Do not report — if you are not reasonably confident, omit the finding entirely

DO NOT REPORT any of these — they are NOT bugs:
- SQL queries using parameterized placeholders (? or $1) are NOT SQL injection, even with LIKE
- Functions called from test files that exist in the same package/module — tests can access all same-package symbols
- Nil pointer risks where a nil check already guards the access on the same or preceding line
- Division by zero where the divisor is already checked (e.g. if x > 0 before x used as divisor)
- Approximate calculations that are clearly labeled as estimates (e.g. token estimation)
- Default/fallback return values in parser helpers — returning a default for unparseable input is intentional
- Standard resource cleanup patterns (e.g. defer rows.Close() after a query)
- Hardcoded constants used in SQL DDL (e.g. DROP TABLE with a fixed table name list)

LINE NUMBERS: The code has line numbers. Use them in line_start/line_end. Only report a line range if you can point to the exact lines.

Return ONLY this JSON structure:
{"issues":[{"category":"bug|security|perf|style","severity":"critical|high|medium|low","confidence":0.0-1.0,"title":"short title","description":"what is wrong","line_start":null,"line_end":null,"suggestion":"how to fix"}],"metadata":{"exports":["names"],"imports":[{"from":"path","names":["names"]}],"interfaces":["names"],"patterns":["label"],"summary":"one sentence"}}

If no issues: {"issues":[],"metadata":{"exports":[],"imports":[],"interfaces":[],"patterns":[],"summary":"one sentence"}}`

// callLLMForScan sends a file to the LLM for review and returns the raw
// response, whether it was truncated, and any error.
func callLLMForScan(client *api.Client, model, path, language, content string, tokenEstimate int, prog Progress) (string, bool, error) {
	// Add line numbers so the model can reference exact lines
	numberedContent := addLineNumbers(content)

	// Dynamic per-file content — only this part changes between files.
	userMsg := fmt.Sprintf("Review this %s file.\n\nFile: %s\n```%s\n%s\n```",
		language, path, language, numberedContent)

	numPredict := scanNumPredict(tokenEstimate)
	numCtx := scanContextTier(tokenEstimate, numPredict)

	stream := true
	req := &api.ChatRequest{
		Model: model,
		Messages: []api.Message{
			{Role: "system", Content: scanSystemPrompt},
			{Role: "user", Content: userMsg},
		},
		Stream: &stream,
		Format: json.RawMessage(`"json"`),
		Options: map[string]any{
			"temperature": 0.3,
			"num_predict": numPredict,
			"num_ctx":     numCtx,
		},
	}

	var response strings.Builder
	var lastErr error

	for attempt := range 3 {
		response.Reset()
		tokenCount := 0
		truncated := false

		ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
		lastErr = client.Chat(ctx, req, func(resp api.ChatResponse) error {
			if resp.Message.Content != "" {
				response.WriteString(resp.Message.Content)
				tokenCount++
				if tokenCount%10 == 0 {
					prog.Tokens("generating", tokenCount)
				}
			}
			if resp.Done && resp.DoneReason == "length" {
				truncated = true
			}
			return nil
		})
		cancel()

		if lastErr == nil {
			prog.TokensDone("generating", tokenCount)
			return response.String(), truncated, nil
		}
		prog.Info(fmt.Sprintf("\n    retry %d/3 for %s: %v", attempt+1, path, lastErr))
		time.Sleep(5 * time.Second)
	}

	return "", false, fmt.Errorf("after 3 retries: %w", lastErr)
}

// scanConciseSystemPrompt is a stripped-down system prompt for the retry path.
const scanConciseSystemPrompt = `You are a senior code reviewer. Report ONLY critical and high severity bugs or security issues. Skip everything else.

Keep your response as SHORT as possible. One sentence per field. Limit to at most 5 issues.
Use confidence 0.9+ only for bugs you can see directly. Use 0.7-0.8 for likely issues. Do not report anything below 0.5 confidence.

Return ONLY this JSON:
{"issues":[{"category":"bug|security|perf","severity":"critical|high","confidence":0.0-1.0,"title":"short","description":"brief","line_start":null,"line_end":null,"suggestion":"brief"}],"metadata":{"exports":[],"imports":[],"interfaces":[],"patterns":[],"summary":"one sentence"}}`

// callLLMForScanConcise is a retry variant that asks the LLM to report only
// critical/high issues and uses a larger output budget. Called when the first
// attempt was truncated and JSON repair failed.
func callLLMForScanConcise(client *api.Client, model, path, language, content string, tokenEstimate int, prog Progress) (string, bool, error) {
	numberedContent := addLineNumbers(content)

	userMsg := fmt.Sprintf("Review this %s file.\n\nFile: %s\n```%s\n%s\n```",
		language, path, language, numberedContent)

	numPredict := scanNumPredictMax
	numCtx := scanContextTier(tokenEstimate, numPredict)

	stream := true
	req := &api.ChatRequest{
		Model: model,
		Messages: []api.Message{
			{Role: "system", Content: scanConciseSystemPrompt},
			{Role: "user", Content: userMsg},
		},
		Stream: &stream,
		Format: json.RawMessage(`"json"`),
		Options: map[string]any{
			"temperature": 0.2,
			"num_predict": numPredict,
			"num_ctx":     numCtx,
		},
	}

	var response strings.Builder
	tokenCount := 0
	truncated := false

	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
		if resp.Message.Content != "" {
			response.WriteString(resp.Message.Content)
			tokenCount++
			if tokenCount%10 == 0 {
				prog.Tokens("retry", tokenCount)
			}
		}
		if resp.Done && resp.DoneReason == "length" {
			truncated = true
		}
		return nil
	})
	cancel()

	if err != nil {
		return "", false, err
	}
	prog.TokensDone("retry", tokenCount)
	return response.String(), truncated, nil
}

// streamLLMChat streams an LLM chat request with a live token counter.
// Returns the final ChatResponse (for tool calls), the accumulated text,
// whether it was truncated, and any error.
func streamLLMChat(client *api.Client, req *api.ChatRequest, label string, prog Progress) (api.ChatResponse, string, bool, error) {
	stream := true
	req.Stream = &stream

	var response strings.Builder
	tokenCount := 0
	truncated := false
	var finalResp api.ChatResponse

	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	err := client.Chat(ctx, req, func(r api.ChatResponse) error {
		finalResp = r
		if r.Message.Content != "" {
			response.WriteString(r.Message.Content)
			tokenCount++
			if tokenCount%10 == 0 {
				prog.Tokens(label, tokenCount)
			}
		}
		if r.Done && r.DoneReason == "length" {
			truncated = true
		}
		return nil
	})
	cancel()

	if tokenCount > 0 {
		prog.TokensDone(label, tokenCount)
	}

	finalResp.Message.Content = response.String()
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

	// 3. Try repairing truncated JSON.
	repaired := repairTruncatedJSON(stripped)
	if json.Unmarshal([]byte(repaired), &resp) == nil {
		return &resp, nil
	}

	// 4. Try newline-fix + repair combined (truncated JSON with raw newlines).
	repairedFixed := repairTruncatedJSON(fixed)
	if json.Unmarshal([]byte(repairedFixed), &resp) == nil {
		return &resp, nil
	}

	// 5. Last resort: extract just the issues array.
	if partial := extractPartialScanResponse(repairedFixed); partial != nil {
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

// filterFindings removes low-signal findings that are likely false positives.
// It drops findings that:
//   - have no line reference (the model is probably guessing)
//   - contain excessive hedging language suggesting uncertainty
//   - are severity high/critical but describe stylistic or theoretical concerns
func filterFindings(issues []ScanIssue) []ScanIssue {
	kept := make([]ScanIssue, 0, len(issues))
	for _, issue := range issues {
		if !keepFinding(issue) {
			continue
		}
		kept = append(kept, issue)
	}
	return kept
}

// hedgeWords are phrases that signal the model is unsure — findings with
// multiple hedges and no line number are almost always false positives.
var hedgeWords = []string{
	"potential", "potentially", "might", "could lead to",
	"if the", "if this", "although", "theoretically",
	"worth noting", "might be", "could be", "may be",
	"not guaranteed", "depending on", "in theory",
}

// falsePositivePatterns are phrases that indicate confidently wrong findings —
// the model believes these are real issues but they stem from fundamental
// misunderstandings (e.g., Go package visibility, reading function signatures).
var falsePositivePatterns = []string{
	// Go package visibility: model thinks unexported = inaccessible from tests
	"unexported function",
	"unexported method",
	"not visible in this test",
	"not accessible to this test",
	"not defined in the provided file",
	"not defined in this file",
	"relies on unexported",
	// Flag/parameter confusion: model misreads which variable is passed
	"passes the flag value string instead of",
	"flag value string instead of the actual",
	"instead of the actual *sql.db",
	"instead of the actual database",
	"receives an incorrect type or value",
}

func keepFinding(issue ScanIssue) bool {
	desc := strings.ToLower(issue.Description + " " + issue.Title)

	// Drop findings matching known false positive patterns
	for _, p := range falsePositivePatterns {
		if strings.Contains(desc, p) {
			return false
		}
	}

	// Count hedging phrases
	hedges := 0
	for _, h := range hedgeWords {
		if strings.Contains(desc, h) {
			hedges++
		}
	}

	hasLine := issue.LineStart != nil

	// No line number + any hedging = drop (model is speculating)
	if !hasLine && hedges > 0 {
		return false
	}

	// 2+ hedges even with a line number = drop (model is very unsure)
	if hedges >= 2 {
		return false
	}

	return true
}

// addLineNumbers prefixes each line with its 1-based line number.
// This helps the LLM reference exact lines in its findings.
func addLineNumbers(content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	b.Grow(len(content) + len(lines)*6) // rough pre-allocation
	for i, line := range lines {
		fmt.Fprintf(&b, "%d: %s\n", i+1, line)
	}
	return b.String()
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
