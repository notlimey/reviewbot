---
name: golang-error-handling
description: "Go error handling — fmt.Errorf wrapping, sentinel errors, errors.Is/As, multi-error, propagation across layers. Not for general Go style (→ code-style) or non-Go languages."
---

# Go Error Handling

## Core Rules

**Handle every error exactly once.** Either handle it (log, recover, default) or return it. Never both — log-and-return produces duplicate lines and confused on-call.

**Wrap with context when crossing a layer boundary.** `fmt.Errorf("doing X: %w", err)` adds what *this* function was trying to do. Don't restate what the inner error already says.

**Use `"verb noun: %w"`, not `"failed to verb noun: %w"`.** The wrap chain already conveys failure — "failed to" is redundant noise that bloats every log line.

```go
// Good
return fmt.Errorf("create role %s: %w", name, err)

// Bad
return fmt.Errorf("failed to create role %s: %w", name, err)
```

**Scope `err` inside `if` to prevent shadowing.** Use `if err := f(); err != nil` so a later `err =` in the same scope can't silently overwrite an outer error.

```go
// Good
if err := doSomething(); err != nil {
    return err
}

// Bad — overwrites outer err
err = doSomething()
if err != nil {
    return err
}
```

**Distinguish `IsNotFound` from real errors in Kubernetes client calls.** `NotFound` usually means the resource was deleted (expected); other errors are genuine failures.

```go
if apierrors.IsNotFound(err) {
    return ctrl.Result{}, nil
}
return ctrl.Result{}, fmt.Errorf("get deployment: %w", err)
```

**Use `%w` to wrap, `%v` to flatten.** `%w` preserves the chain so `errors.Is`/`errors.As` keep working. Choose `%v` only when crossing a public API where the inner type is implementation detail.

**Compare with `errors.Is`, never `==`.** Wrapped errors fail equality. `err == io.EOF` breaks the moment someone wraps it upstream.

**Extract typed errors with `errors.As`, never type assertion.** `err.(*fs.PathError)` doesn't walk the chain; `errors.As` does.

**Return errors, don't panic.** Panic is for programmer bugs (nil deref, impossible states), not runtime conditions. Library code should not panic.

## Patterns to Reach For

**Sentinel errors for stable, comparable conditions.** Export them and document in godoc.

```go
var ErrNotFound = errors.New("user: not found")

func GetUser(ctx context.Context, id string) (*User, error) {
    if rows == 0 {
        return nil, ErrNotFound
    }
}
```

**Custom error types when callers need structured fields** (status, retryable, offending input). Implement `Error() string` and `Unwrap() error` if wrapping.

```go
type ValidationError struct {
    Field string
    Msg   string
}

func (e *ValidationError) Error() string {
    return fmt.Sprintf("validation: %s: %s", e.Field, e.Msg)
}
```

**`errors.Join` for multiple independent failures.** `errors.Is`/`As` work across the joined set.

```go
var errs []error
for _, c := range closers {
    if err := c.Close(); err != nil {
        errs = append(errs, err)
    }
}
return errors.Join(errs...)
```

**Defer-with-named-return to capture cleanup errors.** A failed `Close` on a writer can mean lost data — don't drop it.

```go
func writeFile(path string, data []byte) (err error) {
    f, err := os.Create(path)
    if err != nil {
        return fmt.Errorf("create: %w", err)
    }
    defer func() {
        if cerr := f.Close(); cerr != nil && err == nil {
            err = fmt.Errorf("close: %w", cerr)
        }
    }()
    // ...
}
```

## Patterns to Avoid

**Don't ignore errors with `_`** without a one-line comment explaining why dropping it is safe.

**Don't `panic` for control flow** — not for "expected" conditions, not for "can't happen, just in case."

**Don't include the function name in the leaf error — wrap instead.** The wrap chain provides call context; the leaf describes the condition.

**Don't use `github.com/pkg/errors` in new code.** Stdlib `%w` and `errors.Is`/`As` cover it. Migrate when you touch the file.

**Don't compare error strings.** `err.Error() == "not found"` breaks on wrap. Use a sentinel or type.

**Don't return a typed nil through an `error` interface.** The interface is non-nil because it carries type information — the caller's `err != nil` check passes when you didn't mean it to.

```go
// BAD: caller's err != nil passes even though there's no error
var e *MyError
return e

// GOOD
return nil
```

## Mandatory Checks Before Shipping

1. **No log-and-return.** Grep for `log.*err` followed by `return.*err`.
2. **Every `errors.Is`/`As` target documented in the called function's godoc.** Callers shouldn't read the implementation to know what to branch on.
3. **No bare `_ =` swallowing an error** without a comment.
4. **Deferred close/cleanup errors captured**, especially on writes.
5. **No `panic` in library code.** Only `main`, test helpers, and documented programmer-bug assertions.
6. **Wrap chain reads sensibly.** Mentally print the full message — if it's gibberish or duplicates, fix the wrap site.
7. **No `"failed to ..."` prefixes.** Grep the diff for `failed to` in `fmt.Errorf` calls.
8. **No `err = f(); if err != nil`** in scopes that already have an outer `err`. Use `if err := f(); err != nil` to scope.
