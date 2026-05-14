---
name: typescript-strictness
description: "TypeScript strict-mode discipline — tsconfig strict flags (strictNullChecks, noImplicitAny), narrowing patterns, avoiding any/non-null assertions/type assertions. Not for general TS questions."
paths:
  - "**/*.ts"
  - "**/*.tsx"
  - "**/tsconfig*.json"
---

# TypeScript strictness

`strict: true` in the base `tsconfig.json` — all sub-flags enabled. Every package extends the base config.

## Banned patterns

| Pattern | Rule |
| --- | --- |
| `any` | Banned. Biome error. Use `unknown` and narrow. |
| `as` type assertions | Banned except at API boundaries where the shape is verified at runtime. Must include a comment explaining why. |
| `// @ts-ignore` | Banned. Use `// @ts-expect-error` with an explanation if unavoidable. |
| Non-null assertion `!` | Allowed only with a comment explaining why the value is guaranteed to exist. |

## Preferences

| Topic | Preference |
| --- | --- |
| Type declarations | `type` over `interface`. Use `interface` only when extending or for declaration merging. |
| Enums | `enum` for simple, flat value sets. `as const` object for anything that needs computed values, iteration, or reverse mapping. |
| Catch blocks | Always type `error` as `unknown`. Narrow before accessing properties. |
| Return types | Explicit on exported functions and hooks. Inferred allowed for internal/private helpers. |
| Nullability | `strictNullChecks` on. Both `null` and `undefined` allowed — prefer whichever the data source returns naturally. Don't convert between them without reason. |
| `unknown` / `never` usage | If you can't avoid `unknown` or `never` in a type signature, add a comment explaining the design reason. |

## Examples

```ts
// ✅ Good — type alias for plain shapes
type ApiError = {
  code: number;
  message: string;
};

// ✅ Enum for simple flat sets
enum Status {
  Active,
  Inactive,
  Pending,
}

// ✅ as const for sets needing computed values or iteration
const WIDGET_SIZES = {
  Small: { cols: 1, rows: 1 },
  Medium: { cols: 2, rows: 1 },
  Large: { cols: 2, rows: 2 },
} as const;

// ✅ Catch block — narrow before use
try {
  await api.fetchUser(id);
} catch (error: unknown) {
  if (error instanceof ApiError) {
    handleApiError(error);
  }
  throw error;
}
```

## Anti-patterns

- `catch (error: any)` — type as `unknown` and narrow.
- `value as SomeType` to silence a compiler error — fix the type, or validate at runtime.
- `interface Foo { ... }` for a plain object shape that isn't extended — use `type`.
- `foo!.bar.baz` chained without justification — narrow with a guard or document why the value is guaranteed.
- `// @ts-ignore` — use `// @ts-expect-error: <reason>` so the comment fails when the underlying error is fixed.
