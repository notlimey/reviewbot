---
name: react-state-management
description: "React state-management patterns and library choice — useState, useReducer, context API, Redux, Zustand, nuqs, XState. Not for state placement (→ component-state) or TanStack Query (→ tanstack-query-reads)."
---

# React state management

Pick the layer that matches the data, not the one that's easiest to reach for.

## Layers

| Layer | Tool | Use for |
| --- | --- | --- |
| Server state | TanStack Query | Anything from the backend. Default for most state. |
| Persistent client state | nuqs (URL params) | Filters, pagination, sorting, tabs — anything bookmarkable or shareable. |
| Ephemeral client state | Zustand | Cross-component client-only state: UI toggles, sidebar open/closed, temp drafts. |
| Complex workflows | XState | Multi-step forms, onboarding, state machines with 3+ states and non-trivial transitions. |
| Component-local state | `useState` | Truly local, throwaway state: controlled inputs, hover, accordion open/closed. |
| Static global config | React Context | Theme, locale, auth scope — values that are static or change rarely. |

## Rules

- Never store server data in Zustand or `useState`. It belongs in TanStack Query.
- If a value should be bookmarkable or shareable via URL, it goes in nuqs, not Zustand.
- Don't reach for XState until you have 3+ states with non-trivial transitions. Boolean toggles stay in `useState` or Zustand.
- Don't put frequently-changing values in Context — every consumer re-renders. Use Zustand instead.
- When Context is the right call (static value), leave a one-line comment stating why.
- Don't use `useReducer` as a poor man's Zustand. If state is shared across components, lift it to Zustand.

## Anti-patterns

- `useEffect` + `useState` + `fetch` to load server data — use TanStack Query.
- Syncing URL params to Zustand via `useEffect` — use nuqs directly.
- A Redux/Zustand slice that mirrors a server response — delete it, read from Query.
- Prop-drilling a value 4+ levels deep that isn't server data — lift to Zustand or Context per the rules above.
