---
name: golang-test-integration
description: "Go integration tests — //go:build integration tags, FuzzX, golden files with -update, envtest, operator/CRD/reconciler coverage. Not for unit-test style (→ test-style) or test doubles (→ test-doubles)."
---

# Go integration & specialized tests

Slow tests behind build tags. Fuzz parsers. Golden files for big outputs. Operators have non-optional reconciler coverage.

## Build tags for slow/integration tests

`//go:build integration` keeps `go test ./...` fast in normal use. CI runs `go test -tags=integration` for the full suite.

```go
//go:build integration

package thing_test

func TestPipeline_EndToEnd(t *testing.T) { /* ... */ }
```

Run a single tagged test with `go test -tags=integration -run TestPipeline_EndToEnd ./...`.

## Fuzz tests for parsers and encoders

`func FuzzX(f *testing.F)` finds inputs you wouldn't think of. Seed with known cases and let the fuzzer explore.

```go
func FuzzParse(f *testing.F) {
    f.Add("x=1")
    f.Add("")
    f.Fuzz(func(t *testing.T, in string) {
        got, err := Parse(in)
        if err != nil {
            return
        }
        round, err := Parse(formatBack(got))
        if err != nil || round != got {
            t.Errorf("round-trip failed for %q", in)
        }
    })
}
```

Run locally with `go test -fuzz=FuzzParse -fuzztime=30s`.

## Golden files for large/structured outputs

Write a `-update` flag that rewrites the golden file when the test runs with it; the test reads it otherwise. Diff is reviewable in PR.

```go
var update = flag.Bool("update", false, "update golden files")

func TestRender(t *testing.T) {
    got := Render(input)
    golden := filepath.Join("testdata", "render.golden")
    if *update {
        _ = os.WriteFile(golden, got, 0644)
    }
    want, _ := os.ReadFile(golden)
    if !bytes.Equal(got, want) {
        t.Errorf("output differs from %s (rerun with -update to refresh)", golden)
    }
}
```

## Kubernetes operator coverage

For controller-runtime / operator code, tests aren't optional. Areas that must have coverage:

- **CRD types.** Validation, defaulting, conversions. CRD bugs ship to every cluster that installs the operator.
- **Reconciler logic.** Use `envtest` or a fake client to drive `Reconcile` through its branches. Cover at minimum:
  - happy path,
  - `NotFound` (resource deleted between Get and Update),
  - permanent-failure path (controller sets a status condition and stops).
- **Exported utility/helper functions.** Namespace metadata parsers, validation helpers, anything other packages import.

`envtest` boots a real API server + etcd against the manifests, so reconciler behavior is exercised end-to-end without a kind cluster.

## Pre-ship checks

- Slow / network / k8s-API tests are behind a build tag — `go test ./...` stays fast.
- Parsers and encoders have at least one `FuzzX` with seeded inputs.
- Golden files for any output > ~10 lines; updates land via `-update` and are reviewable in the PR diff.
- Operator code has reconciler tests for happy / NotFound / permanent-failure branches.
