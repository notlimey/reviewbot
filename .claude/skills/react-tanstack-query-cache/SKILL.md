---
name: react-tanstack-query-cache
description: "TanStack Query v5 cache config — QueryClient defaultOptions, staleTime/gcTime buckets, refetchInterval polling, AbortSignal cancellation, on-disk persistence. Not for hook-level reads (→ tanstack-query-reads) or mutations (→ tanstack-query-mutations)."
paths:
  - "**/*.tsx"
  - "**/*.ts"
---

# TanStack Query — cache configuration

Cache behavior lives on `QueryClient.defaultOptions`. Pick a bucket from the table below; don't hand-tune numbers in individual `queryOptions` calls.

## Default-override buckets

The **Default** row lives on `QueryClient.defaultOptions`. Other buckets are per-`queryOptions` overrides. If you find yourself writing the default values into a `queryOptions` call, delete them.

```ts
new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, gcTime: 30 * 60_000 },
    mutations: { retry: 0 },
  },
});
```

| Data shape | staleTime | gcTime | refetchOnMount |
| --- | --- | --- | --- |
| Default | 30s | 30min | if-stale |
| Static-ish (settings, feature flags, list pages) | 5min | Infinity | never |
| Real-time (jobs, pipelines) | 0 | 30min | always + `refetchInterval` |
| Heavy (analytics, big aggregates) | 5min | 30min | if-stale |
| Typing-driven (search, palette) | 0 | 5min | + `placeholderData: keepPreviousData` |

WAN reads may set `retry: 3` with exponential backoff. Local reads keep `retry: 1`.

## Polling, SSE, cancellation

- Polling = `refetchInterval`. Never manual `setTimeout` loops. Use `refetchIntervalInBackground` only when updates matter while unfocused.
- SSE / streaming mutations → dedicated SSE-mutation primitive (see tanstack-query-mutations).
- Every `queryFn` accepts and forwards `signal`. Every `api.*` method accepts an `AbortSignal`. No exceptions.

## On-disk cache persistence

- Persister keyed per scope.
- Allowlist by key prefix. New keys default to non-persisted.
- Buster key from schema versions — bumping either drops the persisted cache.
- Errored queries are not persisted.

## Pre-merge checklist (cache)

- Bucket-table default values live in `defaultOptions`, not in every `queryOptions`
- Every `queryFn` and `api.*` method forwards `AbortSignal`
- Polling uses `refetchInterval`, not `setTimeout`
- Persisted-cache allowlist is opt-in by key prefix
- Persister buster key bumps when schema changes
- File ≤ 150 lines (≤ 100 preferred)
