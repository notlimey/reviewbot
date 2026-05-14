---
name: react-component-logic
description: "React component internal logic — control flow, hooks usage, memoization, early returns, comments. Not for component API (→ component-api), state placement (→ component-state), or styling (→ component-styling)."
paths:
  - "**/*.tsx"
  - "**/*.jsx"
---

# React component logic

## Control flow inside components

- Early returns for loading / error / empty / unauthorized states
- No `if`/`else` chains — use early returns or `switch`
- No `else` blocks at all
- No nested ternaries — extract to a variable, helper, or early return
- No `forEach` — use `for...of` or `.map()`
- Render-prop or small sub-component patterns for complex conditional rendering

## Hooks & memoization

- Import hooks named from `react` (`import { useState }`), never `React.useState`
- Proactive `memo` / `useMemo` / `useCallback` on list items and callbacks passed down
- Custom hooks live in `hooks/`, named `use-kebab-case.ts`

## Comments

- Default to none
- Only write a comment when the *why* is non-obvious (constraint, workaround, surprising behavior)
- Never describe what the code does — names should do that
