# ReviewBot

Local LLM-powered code reviewer. Uses Ollama to scan a codebase in multiple passes and generates a prioritized markdown report.

## Quick Start

```bash
go build -o reviewbot .
reviewbot              # interactive TUI
reviewbot all ./src    # CLI mode
```

Requires [Ollama](https://ollama.ai) running locally with `gemma4:26b` pulled.
