# ReviewBot

A CLI tool that uses a local Ollama model (Gemma 4 26B) to review a codebase in multiple passes. Each file is reviewed in isolation, then a dependency graph is built, and cross-file structural analysis is performed using tool calling. Output is a prioritised markdown report.

Designed to run overnight on a MacBook Pro M4 Pro with 48GB unified memory.

## Requirements

- Go 1.21+ (with CGO enabled)
- [Ollama](https://ollama.ai) running locally with `gemma4:26b` pulled
- Xcode command line tools (for SQLite CGO compilation on macOS)

## Build

```bash
go build -o reviewbot .
```

## Usage

```
reviewbot <command> [project_root] [flags]

Commands:
  discover      Pass 1 — scan filesystem, hash files, build work queue
  scan          Pass 2 — LLM review of each pending file (no tools)
  relations     Pass 3 — build dependency graph from extracted metadata
  structural    Pass 4 — cross-file analysis with tool calling
  report        Pass 5 — generate markdown report
  status        Show current progress
  all           Run full pipeline (pass 1 through 5)
  reset         Drop all tables and start fresh

Flags:
  -model        Ollama model name (default: "gemma4:26b")
  -db           SQLite database path (default: "review.db")
  -delay        Seconds between LLM calls (default: 2)
  -report       Output report path (default: "review_report.md")
  -max-tools    Max tool calls per structural review (default: 10)
  -verbose      Print raw LLM responses for debugging
```

### Examples

```bash
# Full review of a project
reviewbot all ./src

# Use a different model with no delay
reviewbot all ./src -model gemma4:31b -delay 0

# Run overnight with thermal management
nohup reviewbot all ./src -delay 3 > review.log 2>&1 &

# Check progress
reviewbot status

# Re-run — only changed files will be scanned
reviewbot all ./src
```

## Pipeline

| Pass | Command      | LLM? | Description |
|------|-------------|------|-------------|
| 1    | `discover`   | No   | Walk filesystem, hash files, build work queue |
| 2    | `scan`       | Yes  | Per-file review, extract metadata |
| 3    | `relations`  | No   | Build dependency graph from metadata |
| 4    | `structural` | Yes  | Cross-file analysis with tool calling |
| 5    | `report`     | No   | Generate markdown report |

## Supported Languages

TypeScript (.ts, .tsx), C# (.cs, .razor), Go (.go), Python (.py), JavaScript (.js, .jsx)

## Future Improvements (not in v1)

- Parallel scanning with Ollama queue management
- Opus validation pass for critical findings
- Web dashboard with filtering and search
- Git integration — only scan files changed since last commit
- Custom rules for project-specific patterns
- Incremental structural review for changed clusters
