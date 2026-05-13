---
name: react-component-state
description: "Where React component state belongs — local vs lifted vs global vs URL vs server; placement of React Query/Zustand/XState. Not for state-mgmt patterns (→ state-management) or component logic (→ component-logic)."
---

# React component state placement

## State placement (informs where logic lives in a component)

1. Server data → a query library (React Query / SWR / equivalent) — never local state
2. Shareable / bookmarkable UI state (filters, pagination) → URL params
3. Ephemeral cross-component UI state → a small store (Zustand or equivalent)
4. Multi-step flows with 3+ states → a state machine (XState or equivalent)
5. Component-local throwaway state → `useState`

Rule: server data never goes in a client store; reload-surviving state never goes in `useState`.
