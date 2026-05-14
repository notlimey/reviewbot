---
name: golang-code-style
description: "General Go style — early returns, structured logging, named returns, magic strings, exported API surface, URL building, SQL injection. Not for error handling, concurrency, testing, or non-Go languages."
paths:
  - "**/*.go"
---

# Go Code Style

Style rules that the team applies consistently across the Go codebase. None of these are exotic — they're the conventions that keep diffs readable and bugs rare.

## Control Flow

**Early returns over `else` blocks.** Guard clauses keep the happy path at the leftmost indent. Nested `else` makes the reader carry state.

```go
// Good
if !condition {
    return false, nil
}
// main logic here

// Bad
if condition {
    // main logic
} else {
    return false, nil
}
```

## Logging

**Structured logging only.** Never `fmt.Printf`/`fmt.Println` for logging — they bypass log levels, structured fields, and any aggregation tooling. Use `log.FromContext(ctx)` (from `sigs.k8s.io/controller-runtime/pkg/log`) so logs carry the reconcile/request context automatically.

```go
logger := log.FromContext(ctx)
logger.Error(err, "reconcile bucket", "name", bucket.Name)
```

The log message is a short verb-phrase ("reconcile bucket"), structured fields carry the values. Don't `Sprintf` values into the message string.

## Function Signatures

**Named return values for multi-string returns** — or better, return a struct. Two unlabeled `string` returns are an off-by-one bug waiting to happen at the call site.

```go
// Good
func parseNamespace(ns string) (system, env string, err error)

// Better — when the meaning isn't obvious from a label
func parseNamespace(ns string) (Namespace, error)
```

**Unexport symbols not used outside the package.** Exported identifiers are part of your package's API; lowercase by default and only export when a caller actually needs it. Shrinks the surface that has to stay backward-compatible.

## Constants

**Constants for status strings.** Phases, conditions, statuses — anything compared by string equality should be a named constant. Magic strings drift between sites and survive typos that the compiler can't catch.

```go
const (
    conditionFailed       = "Failed"
    conditionProvisioning = "Provisioning"
)
```

## URLs and Paths

**`url.JoinPath` for URL construction**, never string concatenation. Handles trailing slashes, percent-encoding, and missing-scheme bugs that string `+` happily produces.

```go
// Good
u, err := url.JoinPath(base, "api", "v1", "users", id)

// Bad
u := base + "/api/v1/users/" + id
```

For filesystem paths, use `path/filepath`. For URL paths in templates, `url.JoinPath` or `net/url` parsing.

## SQL Safety

**Parameterize, don't `Sprintf`.** Use `?`/`$1` placeholders for values — every database driver supports them and they're the only safe way to pass user input.

**If `fmt.Sprintf` is unavoidable** (e.g. dynamic table or column names that placeholders can't carry), comment why it's safe at the call site. Validate dynamic identifiers with a **restrictive allowlist** (e.g. `regexp.MustCompile(\`^[a-zA-Z_][a-zA-Z0-9_]*$\`)`) — never a blocklist.

```go
// validateIdent restricts to identifier chars; safe to interpolate.
if !validIdent.MatchString(table) {
    return fmt.Errorf("invalid table %q", table)
}
query := fmt.Sprintf("SELECT * FROM %s WHERE id = ?", table)
```

## Mandatory Checks Before Shipping

1. **Linter is clean.** Run the configured linter (`golangci-lint run` in most repos) before commit and before approval. Don't push or approve on a red linter. The items below catch what the linter misses or isn't configured for.
2. **No `fmt.Printf`/`fmt.Println` calls** outside of `cmd/` entry points and tests.
3. **No deeply nested `if/else` ladders** where guard clauses would flatten them.
4. **No magic status/phase strings** — grep the diff for string literals that look like states.
5. **No string-concatenated URLs.** Search for `"http"` and `+` near each other.
6. **Exported identifiers have a real cross-package caller.** If they don't, lowercase them.
