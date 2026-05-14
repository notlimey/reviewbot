---
name: golang-concurrency
description: "Go concurrency — goroutines, channels, sync primitives, context, cancellation, races, worker pools, fan-out/fan-in. Not for general Go style (→ code-style) or non-Go languages."
paths:
  - "**/*.go"
---

# Go Concurrency

Go's concurrency primitives are powerful but easy to misuse. Follow these rules unless there's a specific reason not to.

## Core Rules

**Every goroutine must have a clear exit path.** Before writing `go funcName()`, answer: how does this goroutine terminate? If the answer is unclear, you have a leak. Acceptable exits: the function returns naturally, a context is canceled, a channel is closed, or a quit signal is received.

**Every goroutine must have a clear owner.** The owner is responsible for starting it, signaling it to stop, and waiting for it to finish. Goroutines spawned without an owner are bugs waiting to happen.

**Pass `context.Context` as the first parameter to any function that may block, do I/O, or spawn goroutines.** Name it `ctx`. Check `ctx.Done()` in any loop or blocking operation. Never store contexts in structs; pass them through call chains.

**Channels communicate; mutexes protect.** If you're sharing state between goroutines, ask first whether you can pass it through a channel instead. If you must share, use `sync.Mutex` (or `sync.RWMutex` for read-heavy workloads) and keep critical sections small.

**The sender closes the channel, never the receiver.** Closing from the receive side, or closing twice, panics. If multiple senders exist, coordinate closure through a separate signal (often `context.Context` or a `sync.Once`).

## Patterns to Reach For

**`errgroup.Group` for concurrent tasks that can fail.** Prefer it over raw `sync.WaitGroup` whenever any goroutine can return an error. It handles error propagation and context cancellation cleanly:

```go
g, ctx := errgroup.WithContext(ctx)
for _, url := range urls {
    url := url // capture
    g.Go(func() error {
        return fetch(ctx, url)
    })
}
if err := g.Wait(); err != nil {
    return err
}
```

**Worker pool for bounded concurrency.** Don't spawn one goroutine per item for large workloads — bound it.

```go
sem := make(chan struct{}, maxConcurrent)
for _, item := range items {
    sem <- struct{}{}
    go func(item Item) {
        defer func() { <-sem }()
        process(item)
    }(item)
}
```

Or use `errgroup.SetLimit(n)` if using errgroup.

**`select` with `ctx.Done()` for cancellable waits.** Any `<-ch` that could block indefinitely should be a `select` that also watches the context:

```go
select {
case result := <-ch:
    return result, nil
case <-ctx.Done():
    return zero, ctx.Err()
}
```

## Patterns to Avoid

**Don't use `time.Sleep` for synchronization.** It's a code smell that hides race conditions. Use channels, `sync.WaitGroup`, or `sync.Cond`.

**Don't use bare `sync.WaitGroup` when goroutines can error.** You'll either ignore errors or build error-collection scaffolding that `errgroup` already provides.

**Don't capture loop variables in goroutines without explicit copying.** Before Go 1.22 this was a constant bug source; even in 1.22+, being explicit (`item := item` or passing as parameter) makes intent clear.

**Don't use buffered channels to "improve performance."** Buffer size should reflect actual semantic need (e.g., decoupling producer/consumer rates, signaling). A buffered channel chosen for speed usually masks a design problem.

**Don't communicate via shared memory when channels work.** "Don't communicate by sharing memory; share memory by communicating" is the Go proverb for a reason. Mutexes are correct sometimes, but channels should be the first instinct for goroutine coordination.

## Mandatory Checks Before Shipping

When writing or reviewing concurrent Go code, verify:

1. **Run with `-race`.** Always. `go test -race ./...` and `go run -race`. The race detector catches real bugs that pass review.
2. **Every goroutine has a termination story.** Trace each `go` statement to its exit condition.
3. **Every channel has a clear closer.** Document who closes it if it's non-obvious.
4. **Contexts propagate.** No function that does I/O or spawns goroutines should be missing a `ctx` parameter.
5. **No goroutine leaks under cancellation.** If the parent context is canceled, all spawned work should wind down.

## When Not to Use Concurrency

Concurrency adds complexity. Don't reach for it unless:
- The work is genuinely I/O-bound and parallelism reduces wall time, or
- The work is CPU-bound and you have multiple cores to use, or
- The structure of the problem is naturally concurrent (servers, pipelines).

Sequential code that's fast enough is better than concurrent code that's subtly wrong.
