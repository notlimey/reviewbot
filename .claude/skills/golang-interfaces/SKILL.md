---
name: golang-interfaces
description: "Go interfaces — compile-time `var _ I = (*T)(nil)` assertions, doc-on-interface contracts, nil-interface gotcha, pointer/value receivers, accept-interfaces-return-structs. Not for architecture/DI (→ abstractions)."
paths:
  - "**/*.go"
---

# Go Interfaces

Go's implicit satisfaction lets implementations drift off interfaces silently after refactors, and behavioral contracts rot when documented per-implementation. The rules below make the contract compile-checked and single-sourced.

## Lock the Contract at Compile Time

**Every implementor file ends with a compile-time assertion** for each interface it claims to satisfy.

```go
// internal/iam/rgw/client.go
var _ iam.Client = (*RGWClient)(nil)
```

Rename a method on `iam.Client` and the build breaks here, not in a caller three packages away. Use `(*T)(nil)` for pointer-receiver impls, `T{}` for value-receiver impls.

## Document on the Interface, Not the Implementation

Method comments belong on the interface. Behavioral guarantees — sentinel errors, blocking, concurrency safety, idempotency, ordering — live with the contract so there is exactly one place to update.

```go
// Client manages IAM roles. Implementations must be safe for concurrent use.
type Client interface {
    // EnsureRole creates or updates the role. Returns ErrPolicyInvalid if
    // policy fails validation. Idempotent; safe to retry on any error.
    EnsureRole(ctx context.Context, name string, policy Policy) error
}
```

Implementations only re-document when they deviate. Signature or contract changes on the interface move the doc in the same diff; the compile-time assertion drags every implementor along.

## The Nil-Interface Gotcha

A typed-nil returned through an `error` (or any interface) is **not** a nil interface.

```go
// BUG — caller sees err != nil even when the pointer is nil.
func find(id string) error {
    var e *NotFoundError
    if missing { e = &NotFoundError{ID: id} }
    return e
}

// Fix — return nil explicitly.
func find(id string) error {
    if !missing { return nil }
    return &NotFoundError{ID: id}
}
```

Never declare a typed-nil and return it as an interface. Return bare `nil` or a populated concrete value.

## Receiver Matters for Satisfaction

If a method has a pointer receiver, only `*T` satisfies the interface — `T` does not.

```go
func (c *RGWClient) EnsureRole(...) error { ... }

var _ Client = (*RGWClient)(nil) // ok
var _ Client = RGWClient{}        // does NOT compile
```

Keep all methods on a type using the same receiver kind. Mixing `func (c *T)` and `func (c T)` on one type breaks satisfaction reasoning and is a code smell.

## Accept Interfaces, Return Structs

Parameters take the narrowest interface the function actually uses. Return the concrete type so callers keep access to its full method set and godoc.

```go
// Good
func Copy(dst io.Writer, src io.Reader) (int64, error)
func NewRGWClient(cfg Config) *RGWClient

// Bad — hides the concrete API behind the interface
func NewClient(cfg Config) Client
```

Exception: factories whose whole purpose is implementation selection (`NewStore(driver) Store`).

## Mandatory Checks Before Shipping

1. **Every implementor file has `var _ I = (*T)(nil)`** for each interface it satisfies.
2. **Interface methods document the contract** — errors, concurrency, blocking, idempotency. Implementations don't re-document.
3. **No typed-nil returns through an interface.** Grep the diff for `var .* \*\w+$` near `return`.
4. **Constructors return concrete types**, except when the function's job is implementation selection.
5. **Receiver kind is consistent** across each type's methods.
