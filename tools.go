package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ollama/ollama/api"
)

type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]any
	Handler     func(args map[string]any, ctx *ToolContext) (string, error)
}

type ToolContext struct {
	DB          *sql.DB
	ProjectRoot string
}

type ToolRegistry struct {
	tools    map[string]ToolDef
	ctx      *ToolContext
	maxCalls int
}

func NewToolRegistry(ctx *ToolContext, maxCalls int) *ToolRegistry {
	r := &ToolRegistry{
		tools:    make(map[string]ToolDef),
		ctx:      ctx,
		maxCalls: maxCalls,
	}
	r.registerTools()
	return r
}

func (r *ToolRegistry) registerTools() {
	r.tools["read_file"] = ToolDef{
		Name:        "read_file",
		Description: "Read the full contents of a source file. Use this when you need to see the actual code, not just the summary.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The file path relative to the project root",
				},
			},
			"required": []string{"path"},
		},
		Handler: r.handleReadFile,
	}

	r.tools["get_file_summary"] = ToolDef{
		Name:        "get_file_summary",
		Description: "Get the summary, exports, imports, and patterns for a file. Cheaper than reading the full file.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The file path",
				},
			},
			"required": []string{"path"},
		},
		Handler: r.handleGetFileSummary,
	}

	r.tools["search_findings"] = ToolDef{
		Name:        "search_findings",
		Description: "Search for existing code review findings. Can filter by file path, severity, or category.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Filter by file path (partial match)",
				},
				"severity": map[string]any{
					"type":        "string",
					"description": "Filter by severity: critical, high, medium, low",
				},
				"category": map[string]any{
					"type":        "string",
					"description": "Filter by category: bug, security, perf, style",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max results to return (default 20)",
				},
			},
		},
		Handler: r.handleSearchFindings,
	}

	r.tools["list_files"] = ToolDef{
		Name:        "list_files",
		Description: "List files in a directory or matching a pattern.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory": map[string]any{
					"type":        "string",
					"description": "Directory path relative to project root",
				},
			},
			"required": []string{"directory"},
		},
		Handler: r.handleListFiles,
	}

	r.tools["get_relations"] = ToolDef{
		Name:        "get_relations",
		Description: "Get the dependency relationships for a file. Shows what this file imports and what imports it.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The file path",
				},
			},
			"required": []string{"path"},
		},
		Handler: r.handleGetRelations,
	}

	r.tools["get_file_snippet"] = ToolDef{
		Name:        "get_file_snippet",
		Description: "Get specific lines from a file. Use when you need to see a particular section.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The file path",
				},
				"start_line": map[string]any{
					"type":        "integer",
					"description": "Starting line number (1-based)",
				},
				"end_line": map[string]any{
					"type":        "integer",
					"description": "Ending line number (1-based)",
				},
			},
			"required": []string{"path", "start_line", "end_line"},
		},
		Handler: r.handleGetFileSnippet,
	}
}

func (r *ToolRegistry) validatePath(requestedPath string) (string, error) {
	absPath, err := filepath.Abs(filepath.Join(r.ctx.ProjectRoot, requestedPath))
	if err != nil {
		return "", fmt.Errorf("invalid path")
	}

	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	absRoot, err := filepath.Abs(r.ctx.ProjectRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root symlinks: %w", err)
	}

	if !strings.HasPrefix(realPath, realRoot) {
		return "", fmt.Errorf("path escapes project root: %s", requestedPath)
	}

	return realPath, nil
}

func (r *ToolRegistry) Execute(name string, rawArgs map[string]any) (string, error) {
	tool, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	result, err := tool.Handler(rawArgs, r.ctx)
	if err != nil {
		return fmt.Sprintf("Tool error: %s", err.Error()), nil
	}

	if len(result) > 50000 {
		result = result[:50000] + "\n[truncated — response too large]"
	}

	return result, nil
}

func (r *ToolRegistry) ToOllamaTools() []api.Tool {
	var tools []api.Tool
	for _, t := range r.tools {
		props := buildToolProperties(t.Parameters)
		tools = append(tools, api.Tool{
			Type: "function",
			Function: api.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters: api.ToolFunctionParameters{
					Type:       "object",
					Required:   getRequired(t.Parameters),
					Properties: props,
				},
			},
		})
	}
	return tools
}

func getRequired(params map[string]any) []string {
	if req, ok := params["required"].([]string); ok {
		return req
	}
	return nil
}

func buildToolProperties(params map[string]any) *api.ToolPropertiesMap {
	pm := api.NewToolPropertiesMap()
	if props, ok := params["properties"].(map[string]any); ok {
		for name, val := range props {
			if propMap, ok := val.(map[string]any); ok {
				p := api.ToolProperty{}
				if t, ok := propMap["type"].(string); ok {
					p.Type = api.PropertyType{t}
				}
				if d, ok := propMap["description"].(string); ok {
					p.Description = d
				}
				pm.Set(name, p)
			}
		}
	}
	return pm
}

// Tool handlers

func (r *ToolRegistry) handleReadFile(args map[string]any, ctx *ToolContext) (string, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	absPath, err := r.validatePath(path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("file not found: %s", path)
	}

	if info.Size() > 100*1024 {
		f, err := os.Open(absPath)
		if err != nil {
			return "", err
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		var b strings.Builder
		lineCount := 0
		for scanner.Scan() {
			lineCount++
			if lineCount <= 200 {
				if lineCount > 1 {
					b.WriteByte('\n')
				}
				b.WriteString(scanner.Text())
			}
		}
		if err := scanner.Err(); err != nil {
			return "", err
		}
		if lineCount > 200 {
			b.WriteString(fmt.Sprintf("\n[truncated — file has %d lines total]", lineCount))
		}
		return b.String(), nil
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func (r *ToolRegistry) handleGetFileSummary(args map[string]any, ctx *ToolContext) (string, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	var summary, exports, imports, interfaces, patterns sql.NullString
	err := ctx.DB.QueryRow(`
		SELECT m.summary, m.exports, m.imports, m.interfaces, m.patterns
		FROM metadata m JOIN files f ON f.id = m.file_id
		WHERE f.path = ?
	`, path).Scan(&summary, &exports, &imports, &interfaces, &patterns)

	if err != nil {
		return "No metadata available for this file", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "File: %s\n", path)
	if summary.Valid {
		fmt.Fprintf(&b, "Summary: %s\n", summary.String)
	}
	if exports.Valid {
		fmt.Fprintf(&b, "Exports: %s\n", exports.String)
	}
	if imports.Valid {
		fmt.Fprintf(&b, "Imports: %s\n", imports.String)
	}
	if interfaces.Valid {
		fmt.Fprintf(&b, "Interfaces: %s\n", interfaces.String)
	}
	if patterns.Valid {
		fmt.Fprintf(&b, "Patterns: %s\n", patterns.String)
	}
	return b.String(), nil
}

func (r *ToolRegistry) handleSearchFindings(args map[string]any, ctx *ToolContext) (string, error) {
	query := "SELECT f.path, fi.severity, fi.category, fi.title, fi.description FROM findings fi JOIN files f ON f.id = fi.file_id WHERE 1=1"
	var params []any

	if path, ok := args["path"].(string); ok && path != "" {
		query += " AND f.path LIKE ?"
		params = append(params, "%"+path+"%")
	}
	if sev, ok := args["severity"].(string); ok && sev != "" {
		query += " AND fi.severity = ?"
		params = append(params, sev)
	}
	if cat, ok := args["category"].(string); ok && cat != "" {
		query += " AND fi.category = ?"
		params = append(params, cat)
	}

	limit := 20
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}
	query += " LIMIT ?"
	params = append(params, limit)

	rows, err := ctx.DB.Query(query, params...)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var b strings.Builder
	count := 0
	for rows.Next() {
		var path, sev, cat, title, desc string
		if err := rows.Scan(&path, &sev, &cat, &title, &desc); err != nil {
			continue
		}
		count++
		fmt.Fprintf(&b, "[%s/%s] %s: %s — %s\n", sev, cat, path, title, desc)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate findings: %w", err)
	}

	if count == 0 {
		return "No findings match the criteria", nil
	}
	return b.String(), nil
}

func (r *ToolRegistry) handleListFiles(args map[string]any, ctx *ToolContext) (string, error) {
	dir, _ := args["directory"].(string)
	if dir == "" {
		return "", fmt.Errorf("directory is required")
	}

	absDir, err := r.validatePath(dir)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		return "", fmt.Errorf("read directory: %w", err)
	}

	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(&b, "%s/\n", e.Name())
		} else {
			ext := filepath.Ext(e.Name())
			lang := extensions[ext]
			if lang != "" {
				fmt.Fprintf(&b, "%s [%s]\n", e.Name(), lang)
			} else {
				fmt.Fprintf(&b, "%s\n", e.Name())
			}
		}
	}
	return b.String(), nil
}

func (r *ToolRegistry) handleGetRelations(args map[string]any, ctx *ToolContext) (string, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	var fileID int64
	if err := ctx.DB.QueryRow("SELECT id FROM files WHERE path = ?", path).Scan(&fileID); err != nil {
		return "File not found in database", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Relations for: %s\n\n", path)

	// Files this imports — close cursor before opening the next query
	rows, err := ctx.DB.Query(`
		SELECT f.path, r.detail FROM relations r
		JOIN files f ON f.id = r.target_file_id
		WHERE r.source_file_id = ?
	`, fileID)
	if err == nil {
		fmt.Fprintf(&b, "Imports:\n")
		for rows.Next() {
			var p, d string
			if err := rows.Scan(&p, &d); err != nil {
				continue
			}
			fmt.Fprintf(&b, "  → %s (%s)\n", p, d)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", fmt.Errorf("iterate import relations: %w", err)
		}
		rows.Close()
	}

	// Files that import this
	rows2, err := ctx.DB.Query(`
		SELECT f.path, r.detail FROM relations r
		JOIN files f ON f.id = r.source_file_id
		WHERE r.target_file_id = ?
	`, fileID)
	if err == nil {
		defer rows2.Close()
		fmt.Fprintf(&b, "Imported by:\n")
		for rows2.Next() {
			var p, d string
			if err := rows2.Scan(&p, &d); err != nil {
				continue
			}
			fmt.Fprintf(&b, "  ← %s (%s)\n", p, d)
		}
		if err := rows2.Err(); err != nil {
			return "", fmt.Errorf("iterate imported-by relations: %w", err)
		}
	}

	return b.String(), nil
}

func (r *ToolRegistry) handleGetFileSnippet(args map[string]any, ctx *ToolContext) (string, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	startLine := getIntArg(args, "start_line")
	endLine := getIntArg(args, "end_line")
	if startLine < 1 || endLine < startLine {
		return "", fmt.Errorf("invalid line range: %d-%d", startLine, endLine)
	}

	// Cap at 100 lines
	if endLine-startLine > 100 {
		endLine = startLine + 100
	}

	absPath, err := r.validatePath(path)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("file not found: %s", path)
	}

	lines := strings.Split(string(content), "\n")
	if startLine > len(lines) {
		return "", fmt.Errorf("start_line %d exceeds file length %d", startLine, len(lines))
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}

	var b strings.Builder
	for i := startLine - 1; i < endLine; i++ {
		fmt.Fprintf(&b, "%4d | %s\n", i+1, lines[i])
	}
	return b.String(), nil
}

func getIntArg(args map[string]any, key string) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	if v, ok := args[key].(string); ok {
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}
