---
name: react-component-api
description: "React component API — props, type definitions, file structure, naming, export shape. Not for control flow/hooks (→ component-logic), state placement (→ component-state), or styling (→ component-styling)."
paths:
  - "**/*.tsx"
  - "**/*.jsx"
---

# React component API

## File & component shape

- Use `export function` for components — never arrow functions assigned to a const
- Cap component files at ~200 lines; extract once they grow past that
- Small helpers may live in the same file as the component that uses them
- One component per file (plus its small helpers)

## Props

- ≤2 props → destructure inline: `function Foo({ a, b })`
- 3+ props → use a named props parameter and a `FooProps` type
- Use `PropsWithChildren<T>` when accepting children
- No `forwardRef` (React 19+) — pass `ref` as a regular prop
- Type props with `type FooProps = {...}`, suffix `Props`

## Naming

- Files: `kebab-case.tsx` → `export function PascalCase()`
- Hooks files: `use-kebab-case.ts`
- Types: `PascalCase`, no `I` prefix; suffix by role (`Props`, `State`, `FormValues`, `DTO`)
- Booleans: `is` / `has` / `should` / `can` prefix
- Handlers: `onX` for props, `handleX` for implementations
- Constants: `UPPER_SNAKE_CASE`
- Directories: `kebab-case`

## TypeScript posture

- `strict: true`
- Ban `any` (use `unknown` + narrowing)
- Ban `as` assertions except at verified external boundaries, with a comment
- Non-null `!` allowed only with a comment explaining why
- `// @ts-expect-error` with reason — never `// @ts-ignore`
- Explicit return types on exported functions; inferred is fine internally
- Prefer `type` over `interface`; `as const` for complex sets, `enum` for simple ones
