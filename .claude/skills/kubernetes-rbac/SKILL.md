---
name: kubernetes-rbac
description: "kubebuilder RBAC markers and ClusterRole/Role manifests for operators — ServiceAccount permissions, generated config/rbac/role.yaml. Not for application-level RBAC."
---

# Operator RBAC

Operator permissions accumulate. A scope you grant in PR #4 is still there in PR #400 unless someone removes it. Stay tight.

## Core Rules

**Place `+kubebuilder:rbac` markers in `constructor.go`** (or whatever the controller's setup/constructor file is called), following the established Application controller pattern. Markers in random files drift away from the controller they belong to and get harder to audit.

```go
// internal/controller/bucket/constructor.go

// +kubebuilder:rbac:groups=happi.io,resources=buckets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=happi.io,resources=buckets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=happi.io,resources=buckets/finalizers,verbs=update

func New(...) *Reconciler { /* ... */ }
```

**Don't request RBAC for resources you don't operate on.** Every `objectbucketclaims/status` you list and never touch is a permission you've handed an attacker who compromises the operator's ServiceAccount. Drop unused entries when you remove the code that needed them.

**Verbs are minimal.** `get;list;watch` for read paths. Add `create;update;patch;delete` only for resources the controller actually mutates. `*` is almost never right.

**Subresources are explicit.** `resources=buckets/status` is its own grant, separate from `buckets`. Don't omit `/status` if you call `Status().Update()`; don't include it if you don't.

## Patterns to Reach For

**One block of markers per resource group**, grouped near the relevant Reconciler. Easy to scan and to remove together.

**`namespace=X` scoping for namespaced operators.** If the operator only watches one namespace, scope the marker so the generated Role is namespaced (not ClusterRole).

**Re-run `make manifests`** after marker changes. The generated `config/rbac/role.yaml` is the source of truth for what gets installed; keep it under review.

## Patterns to Avoid

**Don't grant `get;list;watch` on Secrets without a clear, narrow reason.** Operators are juicy targets — broad Secret access is the difference between "compromised operator" and "compromised cluster."

**Don't request `/finalizers` if you don't use a finalizer.** The marker exists to allow `Update()` on the finalizers subresource; without that code, it's dead permission.

**Don't add markers in files the kubebuilder generator doesn't scan.** Stick to the controller package's expected files (commonly `controller.go` / `constructor.go`).

**Don't paper over a "permission denied" by widening the marker.** Read the error, find the specific resource and verb, grant exactly that.

## Mandatory Checks Before Shipping

1. **Every marker matches a real client call.** Grep the controller for the resource and verb.
2. **No `verbs=*` and no `resources=*`** unless there's a documented reason.
3. **`/status` and `/finalizers` markers reflect actual code paths.** No stale subresource grants.
4. **`make manifests` was re-run** and the generated role updated.
5. **Markers live in `constructor.go`** (or the controller's established setup file), not scattered.
