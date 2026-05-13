---
name: react-tanstack-query-reads
description: "Read-side TanStack Query v5 — useQuery, useSuspenseQuery, useInfiniteQuery, queryOptions, key factories, select, Suspense loading. Not for mutations (→ tanstack-query-mutations) or cache config (→ tanstack-query-cache)."
---

# TanStack Query — reads

`@tanstack/react-query` v5. Server-state reads go through `queryOptions` + a key factory. Loading state is Suspense, not `data | undefined`.

## File layout

Colocate by feature, one read hook per file. Mutations live next to their reads in the same `hooks/` folder. Cap files at 150 lines; aim under 100.

```
<feature>/hooks/
  useThing.ts
  useUpdateThingMutation.ts
  thingKeys.ts
```

Shared `lib/` is reserved for cross-feature primitives.

## Hierarchical key factories — no literals, ever

Always go through `queryOptions()`. Factories nest from a root:

```ts
export const rootKeys = {
  all: () => ["root"] as const,
  detail: (id: string) => [...rootKeys.all(), id] as const,
  section: (id: string) => [...rootKeys.detail(id), "section"] as const,
};

export const thingOptions = (id: string) =>
  queryOptions({
    queryKey: [...rootKeys.section(id), "thing"] as const,
    queryFn: ({ signal }) => api.getThing(id, { signal }),
  });
```

A literal array as a `queryKey` is a bug. Nesting exists so you can invalidate at any level. Keys must be JSON-serializable (no `Date`, `Map`, class instances).

## Hooks read scope, take no args

```ts
export const useThing = () => {
  const { id } = useScope();
  return useSuspenseQuery(thingOptions(id));
};
```

Args only for components that need a non-active scope (rare). Naming: reads `useX`.

Inside a scope-gated subtree, scope hooks return non-nullable values — never write `if (!x)` or `enabled: !!x` for things the gate already guarantees. `enabled` is reserved for UI state, dependent data, or user input.

## `select` for derived data

Reach for `select` when a component needs a subset or derivation. The component re-renders only when its slice changes.

```ts
export const useThingTitle = () => {
  const { id } = useScope();
  return useSuspenseQuery({
    ...thingOptions(id),
    select: (data) => data.title,
  });
};
```

Keep `select` referentially stable — top-level function or `useCallback`. Inline `select` re-derives every render and defeats the optimization.

## Loading & errors

- Route-level data → `useSuspenseQuery`. Wrap routes with `<Suspense fallback={<Skeleton />}>` and `<ErrorBoundary>`. Components read data directly, never `data | undefined`.
- In-component lazy data → `useQuery`. Popovers, command palette, hover-prefetched widgets, modals.
- Skeletons for first paint. Spinners only over already-rendered content.

## Infinite queries

Pagination uses `useInfiniteQuery` + `infiniteQueryOptions()`. Page param goes through the key factory.

```ts
export const thingListOptions = (filter: Filter) =>
  infiniteQueryOptions({
    queryKey: [...rootKeys.list(), filter] as const,
    queryFn: ({ pageParam, signal }) => api.listThings(filter, pageParam, { signal }),
    initialPageParam: 0,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  });
```

Render flat data via `data.pages.flatMap(...)`. Components don't read `.pages[i]` directly — page boundaries are a cache implementation detail.

## Migrate, don't preserve

When you touch a file using `useEffect` + `useState` + `fetch`, port it to TanStack Query in the same change. No legacy left behind.

## Pre-merge checklist (reads)

- No literal arrays as query keys — every key reaches a factory
- Every `queryFn` accepts and forwards `signal`
- Route-level data uses `useSuspenseQuery`; route has Suspense + ErrorBoundary
- No `if (!x)` / `enabled: !!x` below a scope gate
- Pagination uses `useInfiniteQuery` + `infiniteQueryOptions`
- `select` is a stable reference (top-level fn or `useCallback`)
- File ≤ 150 lines (≤ 100 preferred)
