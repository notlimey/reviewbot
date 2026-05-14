---
name: react-component-styling
description: "React component styling and folder layout — Tailwind, vertical slices, file organization, UI primitives. Not for component API (→ component-api), logic (→ component-logic), state, or a11y."
paths:
  - "**/*.tsx"
  - "**/*.jsx"
---

# React component styling

## Styling

- Tailwind with a `cn()` helper for conditional classes
- Sort class names (Biome / equivalent linter rule)
- Don't build parallel UI primitives — pick one component library and stick to it

## Folder layout (vertical slice)

```
features/{name}/
  components/
  queries/      # read hooks
  mutations/    # write hooks
  hooks/
  types.ts
  index.ts      # only if multiple consumers
```
