---
name: golang-abstractions
description: "Go interface design, dependency injection seams, shared helpers, vendor names in API types, leaky abstractions. Not for general Go style (→ code-style) or error/concurrency/testing (→ those skills)."
paths:
  - "**/*.go"
---

# Abstractions & Architecture

The reconciler should not know whether you run Ceph or AWS, Keycloak or Auth0. Keep that knowledge behind interfaces — and keep the interfaces honest.

## Core Rules

**Abstract external dependencies behind thin interfaces.** Anything outside the operator's process — Ceph/RadosGW, Keycloak, Vault, an S3 backend — gets a small consumer-defined interface. The reconciler depends on the interface; concrete clients live in their own packages.

```go
// internal/iam/iam.go — consumer-defined interface
type Client interface {
    EnsureRole(ctx context.Context, name string, policy Policy) error
    DeleteRole(ctx context.Context, name string) error
}

// internal/iam/rgw/client.go — one implementation
type RGWClient struct { /* ... */ }
func (c *RGWClient) EnsureRole(...) error { /* ... */ }
```

**Keep interfaces thin.** Only expose methods the consumer actually calls. A 12-method interface for a reconciler that uses 3 of them is a leak — every implementor pays for methods nobody asked for.

**Don't leak implementation details into API types.** CRD field names, status enums, and exported types should describe the *role*, not the vendor. `S3RoleType` survives a backend swap; `RGWRoleType` doesn't.

```go
// Good — provider-agnostic
type BucketSpec struct {
    RoleType S3RoleType `json:"roleType"`
}

// Bad — bakes RadosGW into the public API
type BucketSpec struct {
    RoleType RGWRoleType `json:"roleType"`
}
```

**DRY across controllers.** Repeated logic — namespace label lookups, status transitions, finalizer wiring — moves into shared packages once it appears in two controllers. Don't wait for the third copy.

- Generic Kubernetes helpers: `internal/kubernetes/`
- Happi-specific shared types and constants: `pkg/happi/`
- Label constants live in **`pkg/happi/labels.go`** — single source of truth, no per-controller redefinitions.

## Patterns to Reach For

**Define interfaces at the consumer.** The package that *uses* the dependency owns the interface. The package that *implements* it imports nothing from the consumer. This is what makes mocks and swap-outs work.

**Inject via struct fields, not package globals.** The reconciler holds an `IAMClient` field set at construction; tests substitute a fake. Globals defeat the abstraction.

**One interface per role, not one per implementor.** If `IAMClient` and `IDPClient` are different responsibilities, they're different interfaces — even if the same struct happens to implement both.

## Patterns to Avoid

**Don't define an interface "just in case."** A single implementor with no test seam is just indirection. Add the interface when the second implementor or the test fake shows up.

**Don't re-export concrete client types from `pkg/happi`.** That couples every controller to the concrete type and undoes the abstraction.

**Don't put label keys in the controller that uses them.** Anyone setting or reading that label has to import the controller package — backwards. Put the constant in `pkg/happi/labels.go` and import it from both producer and consumer.

**Don't let "shared helpers" become a junk drawer.** `internal/util/` with 40 unrelated functions is worse than the duplication. Group helpers by concern (`internal/kubernetes/labels`, `internal/kubernetes/finalizers`).

## Mandatory Checks Before Shipping

1. **CRD field names contain no vendor terms.** Search the diff for backend-specific words (`rgw`, `keycloak`, `aws`, `gcp`).
2. **External clients are reached through an interface field, not a concrete type.** Grep the reconciler struct.
3. **No duplicated helpers.** If two controllers grew the same function, extract before merging.
4. **New label constants land in `pkg/happi/labels.go`**, not in the controller that introduced them.
5. **Interfaces only declare methods that have callers.** Drop unused methods.
