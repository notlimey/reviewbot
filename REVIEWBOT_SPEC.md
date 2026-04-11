# ReviewBot — Local Code Review Orchestrator

## Spec for Implementation

Hand this document to Claude Code. It contains everything needed to build the project from scratch.

---

## 1. What This Is

A CLI tool written in Go that uses a local Ollama model (Gemma 4 26B) to review a codebase in multiple passes. Each file is reviewed in isolation first, then the tool builds a dependency graph, then it does cross-file structural analysis using tool calling so the model can explore related files on its own. Output is a prioritised markdown report.

Designed to run overnight on a MacBook Pro M4 Pro with 48GB unified memory.

---

## 2. Project Structure

```
reviewbot/
├── main.go              # CLI entry point, flag parsing, command routing
├── config.go            # Constants, supported extensions, skip dirs
├── db.go                # SQLite schema, init, helper queries
├── discovery.go         # Pass 1: filesystem walk, hashing, file inventory
├── scanner.go           # Pass 2: per-file LLM review (no tools)
├── relations.go         # Pass 3: build dependency graph from metadata
├── structural.go        # Pass 4: cluster analysis WITH tool calling
├── tools.go             # Tool registry, definitions, safety layer, execution
├── report.go            # Pass 5: compile findings into markdown report
├── types.go             # All shared structs
├── go.mod
├── go.sum
└── README.md
```

---

## 3. Dependencies

```
github.com/ollama/ollama   # Official Go SDK — use api.ClientFromEnvironment()
github.com/mattn/go-sqlite3 # SQLite driver (requires CGO)
```

No other external dependencies. Keep it minimal.

---

## 4. CLI Interface

```
reviewbot <command> [project_root] [flags]

Commands:
  discover      Pass 1 — scan filesystem, hash files, build work queue
  scan          Pass 2 — LLM review of each pending file (no tools)
  relations     Pass 3 — build dependency graph from extracted metadata
  structural    Pass 4 — cross-file analysis with tool calling
  report        Pass 5 — generate markdown report
  status        Show current progress (files pending/scanned/errored, finding counts)
  all           Run full pipeline (pass 1 through 5)
  reset         Drop all tables and start fresh

Flags:
  -model        string   Ollama model name (default: "gemma4:e4b")
  -db           string   SQLite database path (default: "review.db")
  -delay        int      Seconds between LLM calls for thermal management (default: 2)
  -report       string   Output report path (default: "review_report.md")
  -max-tools    int      Max tool calls per structural review turn (default: 10)
  -verbose      bool     Print raw LLM responses for debugging (default: false)
```

Examples:
```bash
reviewbot all ./src
reviewbot all ./src -model gemma4:31b -delay 0
reviewbot scan ./src -verbose
reviewbot status
nohup reviewbot all ./src -delay 3 > review.log 2>&1 &
```

---

## 5. Database Schema (SQLite)

Use WAL journal mode for concurrent reads during long runs. All tables use `IF NOT EXISTS`.

```sql
PRAGMA journal_mode=WAL;

CREATE TABLE IF NOT EXISTS files (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    path            TEXT UNIQUE NOT NULL,
    language        TEXT NOT NULL,
    hash            TEXT NOT NULL,           -- SHA-256 of file content
    token_estimate  INTEGER,                 -- len(content) / 4
    status          TEXT DEFAULT 'pending',  -- pending | scanning | scanned | skipped | error
    scanned_at      DATETIME,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS findings (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id         INTEGER NOT NULL REFERENCES files(id),
    pass            TEXT NOT NULL,            -- 'file_scan' | 'structural'
    category        TEXT NOT NULL,            -- 'bug' | 'security' | 'perf' | 'style'
    severity        TEXT NOT NULL,            -- 'critical' | 'high' | 'medium' | 'low'
    confidence      REAL,                     -- 0.0 to 1.0
    title           TEXT NOT NULL,
    description     TEXT,
    line_start      INTEGER,
    line_end        INTEGER,
    suggestion      TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS metadata (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id         INTEGER UNIQUE NOT NULL REFERENCES files(id),
    exports         TEXT,                     -- JSON array of strings
    imports         TEXT,                     -- JSON array of {from, names}
    interfaces      TEXT,                     -- JSON array of strings
    patterns        TEXT,                     -- JSON array of strings
    summary         TEXT,                     -- 2-3 sentence description
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS relations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    source_file_id  INTEGER NOT NULL REFERENCES files(id),
    target_file_id  INTEGER NOT NULL REFERENCES files(id),
    relation_type   TEXT NOT NULL,            -- 'imports' | 'implements' | 'mirrors'
    detail          TEXT,
    cluster_id      TEXT
);

CREATE TABLE IF NOT EXISTS structural_findings (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    cluster_id      TEXT,
    file_ids        TEXT,                     -- JSON array of ints
    category        TEXT NOT NULL,
    severity        TEXT NOT NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS run_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id          TEXT NOT NULL,
    started_at      DATETIME,
    finished_at     DATETIME,
    files_total     INTEGER,
    files_scanned   INTEGER,
    findings_count  INTEGER,
    status          TEXT DEFAULT 'running'    -- 'running' | 'completed' | 'failed'
);
```

---

## 6. Supported Languages

Define in `config.go`. Easy to extend.

```go
var extensions = map[string]string{
    ".ts":    "typescript",
    ".tsx":   "typescript",
    ".cs":    "csharp",
    ".razor": "csharp",
    ".go":    "go",
    ".py":    "python",
    ".js":    "javascript",
    ".jsx":   "javascript",
}

var skipDirs = map[string]bool{
    "node_modules": true, "bin": true, "obj": true,
    ".git": true, ".next": true, "dist": true,
    "vendor": true, "build": true, ".nuxt": true,
    "coverage": true, "__pycache__": true,
}
```

---

## 7. Pass 1 — Discovery (`discovery.go`)

No LLM involved. Pure filesystem.

1. `filepath.WalkDir` the project root
2. Skip directories in `skipDirs`
3. Match file extensions against `extensions` map
4. Read file content, compute SHA-256 hash
5. Check if file already exists in DB with same hash → skip if unchanged
6. Insert or update file record with status `pending`
7. Estimate tokens as `len(content) / 4`
8. Print summary: total found, new/changed, skipped

This is what makes re-runs fast. Only changed files enter the queue.

---

## 8. Pass 2 — File-Level Scan (`scanner.go`)

This is the workhorse. Each file reviewed individually, NO tool calling.

### Flow:
1. Query all files with `status = 'pending'`, ordered by `token_estimate ASC` (small files first — fast early progress)
2. For each file:
   a. Set status to `scanning`
   b. Read file content
   c. If token estimate > 50,000 → set status to `skipped`, continue
   d. Build prompt (see below)
   e. Call Ollama Chat API (no streaming, temperature 0.3)
   f. Parse JSON response
   g. Save issues to `findings` table
   h. Save metadata to `metadata` table
   i. Set status to `scanned`, record `scanned_at`
   j. Print progress: `✓ [42/500] ./src/auth/service.ts (3 issues, 28s)`
   k. Sleep for `delay` seconds

### Error handling:
- JSON parse failure → set status to `error`, log raw response if `-verbose`
- Ollama connection error → set status back to `pending` (will retry next run)
- File read error → skip, log

### Prompt template:

```
You are a senior code reviewer. Review this {language} file.
Focus on real issues: bugs, security vulnerabilities, performance problems, and genuinely bad patterns.
Do NOT flag minor style issues unless they indicate a real problem.
Be precise about line numbers when possible.

Return ONLY valid JSON, no markdown fences, no backticks, no explanation:
{
  "issues": [
    {
      "category": "bug|security|perf|style",
      "severity": "critical|high|medium|low",
      "confidence": 0.0-1.0,
      "title": "short title",
      "description": "what is wrong and why it matters",
      "line_start": null or line number,
      "line_end": null or line number,
      "suggestion": "how to fix it"
    }
  ],
  "metadata": {
    "exports": ["exported class/function/type names"],
    "imports": [{"from": "module/path", "names": ["imported names"]}],
    "interfaces": ["public API contracts or type definitions"],
    "patterns": ["repository", "middleware", "hook", "controller", "service", etc],
    "summary": "2-3 sentence description of what this file does and its role"
  }
}

If there are no issues, return {"issues": [], "metadata": {...}}.

File: {path}
```{language}
{content}
```
```

### Ollama API call config:
```go
req := &api.ChatRequest{
    Model:    model,
    Messages: []api.Message{{Role: "user", Content: prompt}},
    Stream:   ptrFalse, // no streaming
    Options: map[string]interface{}{
        "temperature": 0.3,
        "num_predict": 4096,
    },
}
```

### JSON cleaning:
The model sometimes wraps JSON in markdown fences. Strip these before parsing:
```go
func cleanJSON(s string) string {
    s = strings.TrimSpace(s)
    s = strings.TrimPrefix(s, "```json")
    s = strings.TrimPrefix(s, "```")
    s = strings.TrimSuffix(s, "```")
    return strings.TrimSpace(s)
}
```

---

## 9. Pass 3 — Relation Building (`relations.go`)

No LLM involved. Pure data processing from the metadata table.

1. Query all metadata records (exports + imports)
2. Build an export map: `exportName → fileID`
3. For each file's imports, check if the imported name exists in the export map
4. Insert a relation record: `source_file_id` (importer) → `target_file_id` (exporter)
5. Assign cluster IDs — files in the same directory or connected via relations get the same cluster_id. Use directory path as cluster_id for simplicity.
6. Print summary: `Built 347 relations from 892 exports`

---

## 10. Pass 4 — Structural Review with Tool Calling (`structural.go` + `tools.go`)

This is where tool calling happens. The model analyses clusters of related files and can request additional context using whitelisted tools.

### Cluster selection:
1. Group scanned files by directory
2. Skip single-file directories
3. For each cluster with 2+ files, run a structural review

### System prompt for structural review:
```
You are a senior architect reviewing a cluster of related files for cross-cutting issues.
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
}
```

### Initial user message:
Provide file summaries and existing findings for the cluster as the first user message, then let the model use tools if it needs more info.

### Tool call loop:
```
1. Send initial message with cluster context + tools
2. Get response
3. If response has tool_calls:
   a. Execute each tool call through the safety layer
   b. Append tool results as role:"tool" messages
   c. Loop back to step 2
   d. If tool call count exceeds max-tools flag, force a final response
4. If response has content (no tool calls), parse the JSON result
5. Save structural findings to DB
```

---

## 11. Tool Definitions (`tools.go`)

### Tool Registry Architecture

```go
type ToolDef struct {
    Name        string
    Description string
    Parameters  map[string]any        // JSON schema
    Handler     func(args map[string]any, ctx *ToolContext) (string, error)
}

type ToolContext struct {
    DB          *sql.DB
    ProjectRoot string    // all file paths must be within this
}

type ToolRegistry struct {
    tools   map[string]ToolDef
    ctx     *ToolContext
    maxCalls int
}
```

### Tool: `read_file`
- **Description:** "Read the full contents of a source file. Use this when you need to see the actual code, not just the summary."
- **Parameters:**
  - `path` (string, required): "The file path relative to the project root"
- **Handler:**
  1. Resolve and validate path is within project root (see safety layer)
  2. Check file exists and is under 100KB
  3. Return file contents as string
  4. If file too large, return first 200 lines + "[truncated — file has N lines total]"

### Tool: `get_file_summary`
- **Description:** "Get the summary, exports, imports, and patterns for a file. Cheaper than reading the full file."
- **Parameters:**
  - `path` (string, required): "The file path"
- **Handler:**
  1. Look up metadata from DB by path
  2. Return formatted string with summary, exports, imports, interfaces, patterns
  3. If not found, return "No metadata available for this file"

### Tool: `search_findings`
- **Description:** "Search for existing code review findings. Can filter by file path, severity, or category."
- **Parameters:**
  - `path` (string, optional): "Filter by file path (partial match)"
  - `severity` (string, optional): "Filter by severity: critical, high, medium, low"
  - `category` (string, optional): "Filter by category: bug, security, perf, style"
  - `limit` (integer, optional): "Max results to return (default 20)"
- **Handler:**
  1. Build SQL query with optional WHERE clauses
  2. Return formatted list of findings with file path, title, description

### Tool: `list_files`
- **Description:** "List files in a directory or matching a pattern."
- **Parameters:**
  - `directory` (string, required): "Directory path relative to project root"
- **Handler:**
  1. Validate directory is within project root
  2. List files (non-recursive, one level deep)
  3. Return file names with their language type

### Tool: `get_relations`
- **Description:** "Get the dependency relationships for a file. Shows what this file imports and what imports it."
- **Parameters:**
  - `path` (string, required): "The file path"
- **Handler:**
  1. Query relations table for both directions (source and target)
  2. Return formatted list: "imports: [file1, file2]" and "imported by: [file3, file4]"

### Tool: `get_file_snippet`
- **Description:** "Get specific lines from a file. Use when you need to see a particular section."
- **Parameters:**
  - `path` (string, required): "The file path"
  - `start_line` (integer, required): "Starting line number (1-based)"
  - `end_line` (integer, required): "Ending line number (1-based)"
- **Handler:**
  1. Validate path within project root
  2. Read file, extract lines in range
  3. Return numbered lines
  4. Cap at 100 lines max even if range is larger

---

## 12. Safety Layer (`tools.go`)

This is critical. The model must NEVER have unconstrained access.

### Path validation:
```go
func (r *ToolRegistry) validatePath(requestedPath string) (string, error) {
    // Resolve to absolute path
    absPath, err := filepath.Abs(filepath.Join(r.ctx.ProjectRoot, requestedPath))
    if err != nil {
        return "", fmt.Errorf("invalid path")
    }

    // Resolve symlinks
    realPath, err := filepath.EvalSymlinks(absPath)
    if err != nil {
        // File might not exist yet — use the resolved abs path
        realPath = absPath
    }

    // Check it's within project root
    absRoot, _ := filepath.Abs(r.ctx.ProjectRoot)
    realRoot, _ := filepath.EvalSymlinks(absRoot)

    if !strings.HasPrefix(realPath, realRoot) {
        return "", fmt.Errorf("path escapes project root: %s", requestedPath)
    }

    return realPath, nil
}
```

### Execution safety:
```go
func (r *ToolRegistry) Execute(name string, rawArgs map[string]any) (string, error) {
    // 1. Tool must exist in registry
    tool, ok := r.tools[name]
    if !ok {
        return "", fmt.Errorf("unknown tool: %s", name)
    }

    // 2. Execute with timeout
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    // 3. Run handler
    result, err := tool.Handler(rawArgs, r.ctx)
    if err != nil {
        return fmt.Sprintf("Tool error: %s", err.Error()), nil // return error as text, don't crash
    }

    // 4. Cap response size (don't blow up context)
    if len(result) > 50000 {
        result = result[:50000] + "\n[truncated — response too large]"
    }

    return result, nil
}
```

### Rules:
- **Whitelist only** — only the 6 tools defined above can be called
- **Read-only** — no tool writes to the filesystem or database
- **Path sandboxed** — every path resolved and checked against project root, symlinks resolved
- **Size capped** — tool responses truncated at 50KB to protect context window
- **Rate limited** — max tool calls per structural review configurable via `-max-tools` flag (default 10)
- **Timeout** — each tool execution has a 10-second timeout
- **Errors returned as text** — tool failures don't crash the pipeline, they're returned to the model as error messages so it can proceed

---

## 13. Pass 5 — Report Generation (`report.go`)

No LLM involved. Pure SQL queries and string formatting.

### Report structure:
```markdown
# Code Review Report
Generated: 2026-04-09 08:30
Model: gemma4:e4b | Run ID: 20260409-230000

## Summary
| Metric           | Count |
|------------------|-------|
| Files scanned    | 487 / 512 |
| Total findings   | 234 |
| Critical         | 3 |
| High             | 18 |
| Medium           | 89 |
| Low              | 124 |
| Structural       | 12 |

---

## Critical & High Severity Findings

### [CRITICAL] SQL injection in user search endpoint
- **File:** `src/api/UserController.cs` (line 47)
- **Category:** security | **Confidence:** 95%
- **Issue:** Raw string interpolation used in SQL query...
- **Fix:** Use parameterised queries with EF Core...

(... more findings sorted by severity then confidence ...)

---

## Structural Issues

### [HIGH] Inconsistent error handling in auth flow
- **Cluster:** `src/auth/`
- **Category:** error_handling
- **Affected files:** AuthService.cs, TokenValidator.cs, AuthMiddleware.cs
- **Issue:** AuthService returns null on failure, TokenValidator throws...

(... more structural findings ...)

---

## Medium & Low Severity Findings

#### `src/components/UserCard.tsx`
- **[medium]** Missing error boundary — component will crash parent on API failure
- **[low]** Unused import of `useEffect`

(... grouped by file path ...)
```

### SQL queries for report:
- Critical/high: `SELECT ... FROM findings JOIN files ... WHERE severity IN ('critical','high') ORDER BY severity, confidence DESC`
- Structural: `SELECT ... FROM structural_findings ORDER BY severity`
- Medium/low: `SELECT ... FROM findings JOIN files ... WHERE severity IN ('medium','low') ORDER BY path, severity`
- Summary stats: `SELECT COUNT(*) FROM findings WHERE severity = ?` for each level

---

## 14. Ollama API Usage Notes

### Connecting:
```go
client, err := api.ClientFromEnvironment()
// Respects OLLAMA_HOST env var, defaults to http://localhost:11434
```

### Chat (no tools — Pass 2):
```go
stream := false
req := &api.ChatRequest{
    Model:    model,
    Messages: messages,
    Stream:   &stream,
    Options:  map[string]interface{}{"temperature": 0.3, "num_predict": 4096},
}

var response strings.Builder
client.Chat(ctx, req, func(resp api.ChatResponse) error {
    response.WriteString(resp.Message.Content)
    return nil
})
```

### Chat with tools (Pass 4):
```go
// Convert tool definitions to api.Tool format
tools := registry.ToOllamaTools() // returns []api.Tool

req := &api.ChatRequest{
    Model:    model,
    Messages: messages,
    Tools:    tools,
    Stream:   &stream,
    Options:  map[string]interface{}{"temperature": 0.3, "num_predict": 4096},
}

client.Chat(ctx, req, func(resp api.ChatResponse) error {
    if len(resp.Message.ToolCalls) > 0 {
        // Model wants to use tools
        // 1. Append assistant message to history
        // 2. Execute each tool call via registry
        // 3. Append tool result as Message{Role: "tool", Content: result}
        // 4. Call Chat again with updated messages
    } else {
        // Model gave final response — parse JSON
    }
    return nil
})
```

### Tool call response format from Ollama:
```go
resp.Message.ToolCalls = []api.ToolCall{
    {
        Function: api.ToolCallFunction{
            Name:      "read_file",
            Arguments: map[string]any{"path": "src/auth/service.ts"},
        },
    },
}
```

### Sending tool results back:
```go
messages = append(messages, resp.Message) // assistant's tool call message
messages = append(messages, api.Message{
    Role:    "tool",
    Content: toolResult, // the string returned by your handler
})
```

---

## 15. Error Handling & Resilience

### Crash recovery:
- Every file has a status field in SQLite
- `scanning` status means it was in-progress when crash happened
- On startup of `scan` command: reset any `scanning` status back to `pending`
- The scan loop queries `WHERE status = 'pending'` so it naturally resumes

### JSON parse failures:
- Log the raw response if `-verbose` flag is set
- Set file status to `error`
- Continue to next file — never stop the pipeline for one bad parse

### Ollama connection errors:
- Retry 3 times with 5-second backoff
- If still failing, set file status back to `pending` and move on
- Log the error

### Large files:
- Files with token_estimate > 50,000 → status `skipped`
- These are typically generated files, bundles, or vendored code — not worth reviewing

### Tool call loop protection:
- Track tool call count per structural review
- If it exceeds `-max-tools`, stop calling tools and force the model to respond
- Do this by sending a message: "You have used all available tool calls. Please provide your final analysis now."

---

## 16. Performance Expectations

On MacBook Pro M4 Pro, 48GB, running gemma4:e4b at Q4:

| Metric                    | Estimate          |
|---------------------------|-------------------|
| Pass 1 (discovery)        | < 5 seconds       |
| Pass 2 (per file)         | 15-45 seconds     |
| Pass 2 (50 files)         | ~20-30 minutes    |
| Pass 2 (500 files)        | ~3-6 hours        |
| Pass 2 (2000 files)       | ~12-24 hours      |
| Pass 3 (relations)        | < 5 seconds       |
| Pass 4 (per cluster)      | 1-3 minutes       |
| Pass 5 (report)           | < 5 seconds       |
| Delay between files       | 2s default        |

The `-delay` flag adds sleep between Pass 2 LLM calls. Set to 0 for maximum speed, 3+ for overnight runs where thermal management matters.

---

## 17. Testing Strategy

### Start with 50 files:
1. Pick a subfolder of the real codebase (~50 files)
2. Run `reviewbot all ./test-folder -delay 0`
3. Check: does it complete without crashes? Are findings reasonable? Does the report look right?
4. Inspect `review.db` with `sqlite3 review.db` — are all tables populated?

### Verify re-run behaviour:
1. Run the same command again
2. It should skip all files (all unchanged)
3. Modify one file, run again → only that file should be scanned

### Verify crash recovery:
1. Start a scan, kill it mid-way (Ctrl+C)
2. Run `reviewbot status` — should show some pending, some scanned
3. Run `reviewbot scan` — should resume from where it stopped

### Verify tool calling (Pass 4):
1. Run `reviewbot structural ./test-folder -verbose`
2. Check logs — model should be making tool calls
3. Verify tools respect path boundaries

---

## 18. Future Improvements (NOT in v1)

These are explicitly out of scope for the first build. Note them in the README but don't implement:

- **Parallel scanning** — run multiple files concurrently (needs Ollama queue management)
- **Opus validation pass** — send critical findings to Claude API for second opinion
- **Web dashboard** — serve findings on localhost with filtering and search
- **Git integration** — only scan files changed since last commit
- **Custom rules** — user-defined patterns to look for (e.g., "we never use console.log in production")
- **Incremental structural review** — only re-analyse clusters where member files changed

---

## 19. Key Implementation Notes

1. **Use `github.com/ollama/ollama/api` directly** — don't use a wrapper SDK. The official package IS the CLI's own client.

2. **`api.ClientFromEnvironment()`** — this respects the `OLLAMA_HOST` env var automatically.

3. **CGO is required** for `go-sqlite3`. On macOS this should work out of the box with Xcode command line tools.

4. **Tool call arguments** come as `map[string]any` from the Ollama SDK. Type-assert carefully — numbers might come as `float64`, strings as `string`.

5. **JSON responses from the model** sometimes have markdown fences (`\`\`\`json ... \`\`\``). Always strip these before parsing.

6. **The model may return empty issues arrays** — this is valid. Not every file has problems.

7. **Keep prompts under the model's context window** — gemma4:e4b has 256K context but practically you want to stay under 32K per call for speed. Skip files that would exceed this.

8. **`Stream: &falseVar`** — the Ollama Go SDK requires a pointer to bool for the stream field. Create `var falseBool = false` and pass `&falseBool`.

9. **All timestamps in RFC3339 format** — `time.Now().Format(time.RFC3339)`.

10. **The structural review tool calling loop** is the most complex part. Implement it as a recursive function or a for loop with a counter. Always have an exit condition.
