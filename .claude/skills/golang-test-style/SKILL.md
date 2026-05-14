---
name: golang-test-style
description: "Go unit-test discipline — table-driven subtests, t.Parallel, t.Helper, t.Cleanup, require.Equal, cmp.Diff, black-box tests, testdata, naming. Not for test doubles (→ test-doubles) or integration tests (→ test-integration)."
paths:
  - "**/*_test.go"
  - "**/testdata/**"
---

# Go test style

Test behavior, not implementation. One scenario per test. Table-driven for variations.

## Core rules

- **Test behavior, not implementation.** Drive tests through the package's exported API and assert on observable outcomes (return values, side effects, errors). A test that breaks when you rename an unexported function is a tax on refactoring.
- **One scenario per test.** A test can check several things about one behavior, but each test should describe one scenario. If the test name needs an "and" to read true, split it.
- **`t.Helper()` in every test helper.** Without it, failures point at the helper instead of the call site.
- **`t.Cleanup` over `defer` for setup teardown.** It survives `t.Fatal`, runs in LIFO order, and works the same in helpers.
- **Tests must be independent and parallel-safe.** No shared mutable state, no order dependencies, no reliance on the working directory. Use `t.TempDir()` for files, in-memory or per-test schemas for databases.
- **`testdata/` is for fixtures.** The Go toolchain ignores it. Put golden files, sample inputs, and large fixtures there — not inline string literals.
- **Don't share state via package-level variables.** Tests that pass alone but fail under `-count=10` are this bug.
- **Don't use `init()` for test setup.** Use `TestMain` if you genuinely need package-wide setup.

## Table-driven subtests with `t.Parallel()`

```go
func TestParse(t *testing.T) {
    t.Parallel()
    cases := []struct {
        name    string
        in      string
        want    Result
        wantErr error
    }{
        {"empty", "", Result{}, ErrEmpty},
        {"valid", "x=1", Result{X: 1}, nil},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            t.Parallel()
            got, err := Parse(tc.in)
            if !errors.Is(err, tc.wantErr) {
                t.Fatalf("err = %v, want %v", err, tc.wantErr)
            }
            if got != tc.want {
                t.Errorf("got %+v, want %+v", got, tc.want)
            }
        })
    }
}
```

`t.Fatalf` for setup failures (continuing would crash or produce noise); `t.Errorf` for assertion failures (let the test report multiple failures in one run).

## Assertions

- **`require.Equal` over hand-rolled `reflect.DeepEqual`.** `require.Equal` already uses `reflect.DeepEqual` and reports a readable diff on failure.
- **`cmp.Diff` for structs and slices.** It tells you *how* values differ. Use `cmpopts.IgnoreFields` for timestamps and IDs that aren't part of the contract.
- **`errors.Is` / `errors.As` for error comparison.** Same rule as production code — never `==` or string match.

```go
if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(User{}, "CreatedAt")); diff != "" {
    t.Errorf("User mismatch (-want +got):\n%s", diff)
}
```

## Black-box where possible

Test in `package foo_test`. Forces tests through the public API and catches accidental tight coupling to internals. Use white-box (`package foo`) only when an internal genuinely needs direct testing.

## Naming

`TestParse_RejectsTrailingComma` beats `TestParse2`. Names describe the scenario, not the function.

## When not to test

Skip tests for trivial getters/setters, `main()` glue that wires already-tested components, and generated code (test the generator, not the output). The bar is "no logic to verify," not "I'm in a hurry." If there's a branch, a loop, or an error path, write the test.

## Anti-patterns

- Asserting on log output as a behavior — logs are for humans; expose a hook or counter.
- Measuring coverage as a goal. 100% coverage with assertion-free tests is worthless.

## Pre-ship checks

1. `go test -race ./...` passes.
2. Tests pass under `-count=10` and `-shuffle=on`.
3. Every test that can run in parallel calls `t.Parallel()`. Subtests too.
4. Every helper calls `t.Helper()`.
5. Test names describe the scenario, not the function.
6. Fixtures live in `testdata/`, not in giant string literals.
