---
name: react-nextjs
description: "Next.js v13+ App Router — server components/actions, route handlers, middleware, next/image, next/link, generateMetadata, loading.tsx, error.tsx, revalidate, 'use server', app/ files. Not for plain React or Pages Router."
paths:
  - "**/app/**/*.tsx"
  - "**/app/**/*.ts"
  - "**/next.config.*"
  - "**/middleware.ts"
  - "**/middleware.tsx"
---

# Next.js (App Router)

App Router only, React 19+, Next 15+. Server Components are the default.

Composes with: `component-api` (file shape, props), `component-logic` (hooks, control flow), `state-management` (client-state layers), `tanstack-query-reads`/`tanstack-query-mutations` (client-side server state), `typescript-strictness` (types). This skill only covers Next.js-specific decisions — don't restate the others here.

## Server vs client boundary

| Rule | Why it matters |
| --- | --- |
| Default to a Server Component | Smaller client bundle, direct DB/secret access |
| `"use client"` only on leaves that need state, effects, browser APIs, or event handlers | A client parent forces every imported child into the client bundle |
| Server Components pass through `children` / props into Client Components — never the reverse import | Client files cannot import Server Components |
| `import "server-only"` at the top of any module that touches secrets or DB | Accidental client import fails at build time |
| Never read `process.env.SECRET_*` from a Client Component | Leaks secret to the browser |

## Data fetching

| Where | Tool |
| --- | --- |
| First-paint data on a route | Server Component + `await fetch(...)` or direct DB call |
| Mutations from a form or button | Server Action (`"use server"`) |
| Client-side server state (interactive lists, infinite scroll, polling) | TanStack Query — see `tanstack-query`. Server-render the first page, hydrate, hand off to Query |
| Webhooks, third-party callbacks, non-Next consumers | Route handler in `app/api/.../route.ts` |

Do not call your own route handler from a Client Component just to wrap a DB call — that's a Server Action.

Cache control lives on the `fetch` call: `{ next: { revalidate: 60 } }`, `{ cache: "no-store" }`, or `{ next: { tags: ["thing"] } }`. Don't reach for `export const dynamic = "force-dynamic"` — prefer `cookies()` / `headers()` so the *reason* the route is dynamic is in the code.

After a Server Action: `revalidateTag()` or `revalidatePath()`. Don't reconcile by hand-returning fresh data.

## Server Actions

- Treat input as untrusted — validate at the boundary (Zod or equivalent).
- Re-check auth inside the action; the rendering page is not a trust signal.
- Call from `<form action={action}>` or via `useActionState` / `useFormStatus`. Never wrap an action in a client `fetch`.
- Return plain serializable values; throw to surface to the nearest `error.tsx`.

## Routing & files

- Co-locate `loading.tsx`, `error.tsx`, `not-found.tsx`, `layout.tsx` per segment.
- `error.tsx` must be `"use client"` and accept `{ error, reset }`.
- Next 15 async params: `params` and `searchParams` are Promises — `const { id } = await params`.
- Static generation: `generateStaticParams` + `dynamicParams = false` to 404 unknown ids.
- Internal nav: `next/link`, never `<a href>`. Router hooks come from `next/navigation`, never `next/router`.

## Metadata

- Static: `export const metadata: Metadata = {...}`. Dynamic: `generateMetadata({ params })`.
- Set `metadataBase` once in the root layout for absolute OG URLs.
- `next/head` does not exist in App Router.

## Streaming

- Wrap slow segments in `<Suspense fallback={...}>` so faster siblings render immediately.
- `loading.tsx` is the segment-level Suspense boundary; finer Suspense for in-page sections.
- Independent fetches: kick off at the top, `await` at point of use. Don't `await` sequentially when the calls don't depend on each other.

## Images, fonts, scripts

- `next/image` for known-dimension images. `priority` on the LCP image only.
- `next/font` — never a `<link>` to a font CDN.
- `next/script` with `strategy="afterInteractive"` by default; `beforeInteractive` only when truly required.

## Middleware

- One `middleware.ts` at project root. Keep it tiny — runs on every matched request.
- Always set a `matcher`. Never let it run on `_next/*` or static assets.
- Edge runtime: no Node APIs, no DB drivers, no large deps. Heavy auth belongs in a layout or Server Action.

## Caching mental model

Four caches, in order of proximity to the request:

| Cache | Scope | How to break it |
| --- | --- | --- |
| Request memoization | per request | automatic — nothing to do |
| Data cache | persistent, keyed by `fetch` URL+options | `revalidate` / `tags` / `cache: "no-store"` |
| Full route cache | static HTML for a route | `cookies()`, `headers()`, dynamic `searchParams`, `cache: "no-store"` |
| Router cache | client-side, per session | soft-invalidated on navigation |

If a route must be dynamic, express *why* (`cookies()` / `headers()`) rather than `force-dynamic`.

## Anti-patterns

- `useEffect` + `fetch` for first-paint data on a page that could be a Server Component.
- Route handler called via client `fetch` that just wraps a DB call — make it a Server Action.
- `"use client"` at the top of a layout or page so a single child can use a hook — push the boundary down.
- Importing a Server Component into a Client Component file.
- `next/head`, `next/router`, `getServerSideProps`, `getStaticProps` — Pages Router, not App Router.
- `export const dynamic = "force-dynamic"` used as a hammer for a caching surprise without diagnosing the cause.
