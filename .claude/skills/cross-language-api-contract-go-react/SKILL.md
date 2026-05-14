---
name: cross-language-api-contract-go-react
description: "Go↔React wire contract — JSON shapes, error envelopes, time/money encoding, nullability, pagination, OpenAPI/protobuf/tygo codegen. Not for Go-only API design or React data fetching (→ tanstack-query-reads)."
paths:
  - "**/*.go"
  - "**/*.ts"
  - "**/*.tsx"
---

# Go ↔ React API Contracts

The failure mode this targets: Go server and React client drift on types, error shapes, date formats, and nullability — bugs that only surface in integration, hours after merge.

## Single Source of Truth

**Generate, don't hand-write.** Pick one source and codegen both sides:

- **REST:** OpenAPI 3 spec → `oapi-codegen` for Go, `openapi-typescript` or `@hey-api/openapi-ts` for TS.
- **gRPC / gRPC-Web:** `.proto` files → `protoc-gen-go-grpc` for Go, `@bufbuild/protoc-gen-es` for TS.
- **Internal services, low schema churn:** `tygo` to generate TS types directly from Go structs.

Hand-rolled TS types that "mirror" Go structs will drift. The first symptom is usually a 500 nobody can reproduce locally.

**Spec lives next to the Go server and is authoritative.** Generated TS ships into the frontend as a versioned package or git submodule. If the Go handler disagrees with the spec, the spec wins — fix the handler.

## JSON Conventions

**camelCase on the wire.** Pick once, hold the line. `json:"firstName"` tags on every Go field — never let Go's default PascalCase escape.

**Time is RFC 3339 strings, always.** `2025-11-05T14:30:00Z`. Never Unix seconds, never millis, never local time. Go: `time.Time` with default JSON marshalling. TS: parse to `Date` at the boundary, treat as string everywhere else.

**Money is integer minor units.** Cents, øre, satoshi. Never `float64` — JSON numbers lose precision and currency arithmetic in floats is a bug factory. Brand the TS type so it can't accidentally be added to a regular `number`.

**Enums are string unions, not magic numbers.** Go: `type Status string` with `const StatusActive Status = "active"`. TS: `type Status = "active" | "pending" | "closed"`. Reviewable in logs, stable across reorderings, safe to remove or rename.

**IDs are strings, not numbers.** JSON numbers are float64 — anything above 2^53 silently corrupts. Use strings even when the database uses `bigint`. Brand on the TS side (`type UserId = string & { __brand: "UserId" }`) so a `UserId` can't be passed where an `OrgId` is expected.

## Nullability

**Pick one representation per field, document it in the spec.** A field is exactly one of: required, optional-missing, or nullable. Never let "missing" and "null" both mean "absent" — the client will guess wrong.

| Meaning | Go | TS | JSON |
| --- | --- | --- | --- |
| Required | `string` | `string` | always present |
| Optional (absent when unset) | `*string` + `omitempty` | `string \| undefined` | field omitted |
| Nullable (present, null when unset) | `*string` (no `omitempty`) | `string \| null` | `"x": null` |

Generators that emit `string | null | undefined` everywhere mean the spec didn't decide. Fix the spec.

## Error Envelope

**One error shape across every endpoint.** Pick it once, enforce in middleware.

```json
{
  "error": {
    "code": "validation_failed",
    "message": "email is required",
    "details": { "field": "email" }
  }
}
```

- `code` is a stable machine-readable string. The frontend switches on it; renaming is a breaking change.
- `message` is human-readable, English, never localized server-side.
- `details` is endpoint-specific and optional.

HTTP status carries the category (400/401/403/404/409/422/500). `code` carries the specific reason. Never return `{"message": "..."}` from one endpoint and `{"error": "..."}` from another.

The React client translates `code` to a localized string. **Never display the server's `message` verbatim to end users** — that's why `code` exists.

## Pagination

**Cursor-based for any list that can change while paginating.** Offset pagination duplicates and skips rows when underlying data shifts. Response shape:

```json
{ "items": [...], "nextCursor": "opaque-string-or-null" }
```

Cursors are opaque — the client never parses them. Server is free to change the encoding.

## Validation

**Server is canonical; client validates for UX only.** Server must validate every field — never trust the client. The client validates the same rules for fast feedback, but treats the server's response as truth on submit. Both sides reading the rules from the same schema (OpenAPI `format`/`pattern`, or a shared zod-from-spec generator) beats two hand-written `if` blocks that drift.

## Versioning

**URL-prefix versions: `/v1/users`, `/v2/users`.** Breaking changes get a new version, never a silent reshape. Old version stays live until clients migrate; deprecated endpoints are marked `deprecated: true` in the spec first, removed later.

A field rename, a type change, or a new required field is breaking. Adding an optional field is not.

## Patterns to Avoid

**Don't `interface{}` / `any` on either side.** Each one is a contract you didn't write down. Reach for `oneOf`/discriminated unions in the spec.

**Don't ship endpoints not in the spec.** "Just one quick endpoint" becomes the undocumented field nobody remembers. Spec or it doesn't exist.

**Don't let one endpoint return the data field as `null` and another return `[]` for the same "empty list" case.** Pick one — `[]` is usually right.

**Don't return raw database errors.** Map at the boundary. `pq: duplicate key value` is a leak; `{"code": "already_exists"}` is a contract.

## Pre-merge Checks

1. **Spec regenerated and committed.** Diff between Go handler signatures and the spec is empty.
2. **TS client regenerated.** Frontend build picks up new types without hand edits.
3. **New error responses use the envelope** — both shape and the `code` taxonomy.
4. **New fields have explicit nullability in the spec**, not implicit.
5. **No new `time.Time` field encodes as Unix seconds or millis.**
6. **New list endpoints use cursor pagination if the underlying data is mutable.**
7. **No new IDs are typed as `number` on either side.**
