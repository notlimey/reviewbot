---
name: react-tanstack-query-mutations
description: "Write-side TanStack Query v5 — useMutation, optimistic updates with onMutate/onError rollback, setQueryData, invalidateQueries. Not for reads (→ tanstack-query-reads) or QueryClient defaults (→ tanstack-query-cache)."
paths:
  - "**/*.tsx"
  - "**/*.ts"
---

# TanStack Query — mutations

`@tanstack/react-query` v5. Mutations don't double-write. Optimistic updates are required for low-risk single-field writes; forbidden for destructive ones.

## Naming

`use<Action><Slice>Mutation` — e.g., `useUpdateThingMutation`, `useDeleteCommentMutation`. Live in the same `hooks/` folder as the read they invalidate.

## Defaults

- `retry: 0`. Mutations must not double-write — set this once on `QueryClient.defaultOptions.mutations` (see tanstack-query-cache).
- `onSuccess`-invalidate by default. `onSettled` only when paired with an `onMutate` optimistic write.
- Invalidate via key factories — never literal arrays.
- Errors → toast/notification from `onError`.

## Optimistic updates — required for low-risk single-field writes

Toggles, resolve/unresolve, status flips, reorder, rename, feedback.

```ts
onMutate: async (next) => {
  await qc.cancelQueries({ queryKey });
  const prev = qc.getQueryData(queryKey);
  qc.setQueryData(queryKey, applyChange(prev, next));
  return { prev };
},
onError: (_e, _v, ctx) => qc.setQueryData(queryKey, ctx?.prev),
onSettled: () => qc.invalidateQueries({ queryKey }),
```

Forbidden for destructive operations and writes with frequent server-side rejection (uniqueness checks, permission denials).

## `setQueryData` is restricted

Allowed only for:

1. Optimistic updates (above).
2. Server-pushed stream updates from SSE / WebSocket mutations.

Anything else → invalidate. When used, leave a one-line comment with the reason.

## SSE / streaming mutations

Use the dedicated SSE-mutation primitive. Don't roll a new pattern. Every streaming `mutationFn` accepts and forwards `signal` so the consumer can cancel.

## Pre-merge checklist (mutations)

- Default `retry: 0` configured in `QueryClient.defaultOptions`
- Mutations invalidate via factory — no literal arrays in `invalidateQueries`
- Low-risk single-field writes have an optimistic `onMutate` + rollback in `onError`
- Destructive writes do NOT have optimistic updates
- `setQueryData` only for optimistic updates or stream-pushed writes, with a one-line reason comment
- File ≤ 150 lines (≤ 100 preferred)
