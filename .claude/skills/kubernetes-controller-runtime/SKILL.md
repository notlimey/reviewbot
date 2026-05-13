---
name: kubernetes-controller-runtime
description: "controller-runtime operators — Reconcile loop, ctrl.Result, requeue, Status().Update, manager setup, signal handling. Not for CRD type design (→ crd-design) or RBAC markers (→ rbac)."
---

# Controller-Runtime Patterns

Reconcilers are easy to write wrong: a returned error means "retry soon," a `Status().Update` always writes, and a misnamed context defeats graceful shutdown. These rules keep operators from quietly DOSing their own API server.

## Core Rules

**Return `nil` error for permanent / user-caused failures.** A bad manifest, a validation error, an immutable field that was changed — none of these will self-resolve, and returning a non-nil error puts the controller into a tight retry loop. Communicate the problem via a status condition and return `ctrl.Result{}, nil`.

```go
// Good — permanent failure surfaced via status, no retry storm
if !validBackend(spec.Backend) {
    meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
        Type:    conditionReady,
        Status:  metav1.ConditionFalse,
        Reason:  "InvalidBackend",
        Message: fmt.Sprintf("backend %q is not supported", spec.Backend),
    })
    return ctrl.Result{}, r.Status().Update(ctx, &obj)
}

// Bad — retry every few ms forever
if !validBackend(spec.Backend) {
    return ctrl.Result{}, fmt.Errorf("invalid backend %q", spec.Backend)
}
```

Reserve non-nil errors for transient failures (API timeouts, throttling, dependent resource not yet present) where a retry makes sense.

**Guard `Status().Update()` against no-op writes.** `meta.SetStatusCondition` is idempotent on content, but `Update()` fires unconditionally — and that fires a watch event, which re-enqueues the object, which calls Reconcile, which may set the same condition, which calls Update… Compare before writing.

```go
before := obj.Status.DeepCopy()
meta.SetStatusCondition(&obj.Status.Conditions, cond)
// other status mutations
if !equality.Semantic.DeepEqual(before, &obj.Status) {
    if err := r.Status().Update(ctx, &obj); err != nil {
        return ctrl.Result{}, fmt.Errorf("status update: %w", err)
    }
}
```

**Preserve conditions; don't delete them.** Kubernetes API conventions say conditions are append-and-update, not garbage-collect. A `False` condition is signal — clearing it on the next reconcile destroys the user's debugging trail. Update the condition's `Status`/`Reason`/`Message` instead.

**Justify every requeue interval.** `ctrl.Result{RequeueAfter: 10 * time.Minute}` with no comment is a magic number. Either it's wrong (event-driven reconciliation would suffice) or it's right (an external system has no watch and needs polling) — and in the second case, say so in a comment.

```go
// External IdP has no watch API; poll every 5min for revoked sessions.
return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
```

Default to event-driven: watch the dependent resources via `Owns()` / `Watches()` and let the reconciler fire on real changes.

**Use `ctrl.SetupSignalHandler()` for the manager's root context.** Not `context.TODO()`, not `context.Background()`. The signal handler returns a context that's canceled on SIGTERM/SIGINT, which is what gives the manager time to drain.

```go
ctx := ctrl.SetupSignalHandler()
if err := mgr.Start(ctx); err != nil {
    setupLog.Error(err, "manager exited with error")
    os.Exit(1)
}
```

**Distinguish `IsNotFound` from real errors on every Get.** A deleted object hitting Reconcile is the normal "object was deleted" path — ignore it and return. Anything else is a real failure.

```go
if err := r.Get(ctx, req.NamespacedName, &obj); err != nil {
    if apierrors.IsNotFound(err) {
        return ctrl.Result{}, nil
    }
    return ctrl.Result{}, fmt.Errorf("get %s: %w", req.NamespacedName, err)
}
```

## Patterns to Reach For

**`Owns()` for child resources, `Watches()` for cross-resource dependencies.** Both make Reconcile event-driven. If you're tempted to add a `RequeueAfter` to "wait for the child," you probably want an `Owns()` instead.

**Finalizers for ordered teardown only.** If the resource has nothing to clean up outside Kubernetes, skip the finalizer. When you do use one, remove it as the *last* step of cleanup.

**Status subresource enabled (`+kubebuilder:subresource:status`).** Lets you `Status().Update()` without bumping `metadata.generation`, which prevents your own writes from triggering reconciles.

## Patterns to Avoid

**Don't reconcile based on `metadata.generation` alone.** Status changes by other actors don't bump generation, and you'll miss them. Watch what you care about.

**Don't sleep in Reconcile.** Return `RequeueAfter` instead. Sleeping holds a worker slot.

**Don't share state between Reconcile invocations via reconciler fields.** Each call must be independent — Reconcile may run in parallel for different objects, and may be re-driven at any time.

**Don't return `ctrl.Result{Requeue: true}` and a non-nil error.** Pick one. The error already requeues with backoff.

## Mandatory Checks Before Shipping

1. **Every `Get` distinguishes `IsNotFound`.** Grep for `r.Get(` and verify each call.
2. **Every `Status().Update` is guarded** against no-op writes (DeepEqual on the previous status, or explicit dirty-tracking).
3. **No `RequeueAfter` without a comment** explaining why polling is needed.
4. **Permanent-failure paths return `nil` error and set a condition.** No tight retry loops on user-caused problems.
5. **Manager started with `ctrl.SetupSignalHandler()`**, not `context.Background()`.
6. **Conditions are updated, not deleted.** Search the diff for `Conditions = nil` or condition removal.
