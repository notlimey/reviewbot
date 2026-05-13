---
name: kubernetes-crd-design
description: "CustomResourceDefinitions and Go API types — kubebuilder markers, +listType, +listMapKey, Spec/Status shape, validation. Not for reconcile logic (→ controller-runtime) or RBAC (→ rbac)."
---

# CRD Design

A CRD is a public API. Once installed in a real cluster you can add fields, but you can't remove or rename them without breaking existing objects. Design conservatively.

## Core Rules

**Mark list fields with `+listType=map` and `+listMapKey=name`.** This unlocks server-side apply with partial updates and enforces uniqueness on the key. Without it, two clients applying the same CR will fight over the whole list.

```go
// +listType=map
// +listMapKey=name
Containers []ContainerSpec `json:"containers,omitempty"`
```

Use `+listType=set` for primitives that should be unique, `+listType=atomic` only when the list really is opaque (rare).

**Restrict the API surface to what fits the resource's lifecycle.** Don't reuse upstream PodSpec wholesale just because it's there. An ephemeral workload shouldn't accept `volumes: [persistentVolumeClaim: ...]`. Define a restricted subset that maps to what the controller can actually honor — anything you let users put in a CRD becomes a support burden.

```go
// Good — narrow, intentional
type WorkloadVolume struct {
    Name      string         `json:"name"`
    EmptyDir  *EmptyDirSpec  `json:"emptyDir,omitempty"`
    ConfigMap *ConfigMapRef  `json:"configMap,omitempty"`
}

// Bad — accepts things the controller can't deliver
type WorkloadVolume = corev1.Volume
```

**Don't shadow Kubernetes terminology.** Avoid CRD names that already mean something to Kubernetes — `Workload`, `Pod`, `Service`, `Deployment`, `Job`. Operator users will conflate them with the upstream concept. Prefix Happi-specific kinds with `Happi` when there's overlap.

```yaml
# Good
kind: HappiWorkload
kind: HappiBucket

# Bad — collides with general "workload" terminology
kind: Workload
```

**Plan for Pod-level fields you'll need.** `command`, `args`, env-from, security context, resource requests — even if a future PR adds them. Reserve the field names now so you don't have to migrate users from `cmd` → `command` later.

**Provider-agnostic field names.** API types describe the role, not the backend. `s3RoleType` not `rgwRoleType`; `idpProvider` not `keycloakProvider`. The CRD outlives the implementation.

## Patterns to Reach For

**Validation markers do as much work as you can push them to.** `+kubebuilder:validation:Enum`, `+kubebuilder:validation:Pattern`, `+kubebuilder:validation:Required`, `+kubebuilder:validation:MaxLength`. Catching invalid input at admission is cheaper than catching it in Reconcile.

**Pointer types for optional fields with default semantics.** `*int32` distinguishes "unset" from "zero." Use a value type only when zero is genuinely a valid, intentional value.

**Status subresource and printer columns.** `+kubebuilder:subresource:status` plus a few `+kubebuilder:printcolumn` markers makes `kubectl get` useful out of the box.

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
```

**Conditions on Status, not phases.** Use `[]metav1.Condition` with `Ready` / `Available` / `Progressing`. A free-form `phase: Provisioning` string is cheap to ship and expensive to live with.

## Patterns to Avoid

**Don't add fields "just in case."** Removing a CRD field is a migration. Add when there's a real use case.

**Don't use `interface{}` / `runtime.RawExtension` to defer schema decisions.** Users will fill it with anything; you'll spend release N+2 trying to constrain it.

**Don't put implementation details in field names** (`rgwUser`, `keycloakRealm`). Even if the only backend today bakes that in, the next backend won't.

**Don't reuse upstream types as the CRD field type when you only support a subset.** Define your own struct that mirrors only what you'll honor. Otherwise users supply fields you silently ignore.

**Don't break compatibility within a stable API version.** If the CRD is `v1`, you can add fields and you can deprecate, but you can't remove or rename. Bump to `v1beta2` / `v2` for breaking changes.

## Mandatory Checks Before Shipping

1. **Every list field has `+listType` and (for object lists) `+listMapKey`.**
2. **Validation markers cover the obvious cases** — required, enums, patterns, length bounds.
3. **No vendor/backend names in JSON tags.** Grep the API package for `rgw`, `keycloak`, etc.
4. **Kind name doesn't shadow upstream Kubernetes terminology.**
5. **`+kubebuilder:subresource:status` is set** if Status is mutated.
6. **Status uses `[]metav1.Condition`**, not free-form phase strings.
7. **Pod-level fields you'll plausibly want** (`command`, `args`, resources) are at least named in the type, even if currently TODO.
