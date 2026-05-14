---
name: golang-test-doubles
description: "Go test doubles — hand-written fakes vs gomock/mockery, httptest servers, clock injection, time.Now in tests. Not for general test discipline (→ test-style) or integration tests (→ test-integration)."
paths:
  - "**/*_test.go"
---

# Go test doubles

Hand-written fakes by default. `httptest` for HTTP. Inject the clock for time. Generated mocks are a last resort.

## Interfaces for seams, fakes for behavior

When a dependency needs to be substituted, define a **small interface at the consumer** (not the provider) and write a **fake that implements it**.

Generated mocks (`gomock`, `mockery`) lock you into "expected call" assertions — those test implementation, not behavior. Hand-written fakes record state you can assert on:

```go
type emailer interface {
    Send(ctx context.Context, to, subject, body string) error
}

type fakeEmailer struct {
    sent []sentMail
    err  error
}

func (f *fakeEmailer) Send(_ context.Context, to, subject, body string) error {
    if f.err != nil {
        return f.err
    }
    f.sent = append(f.sent, sentMail{to, subject, body})
    return nil
}
```

The test then asserts on `fake.sent` — concrete state — rather than on call counts.

## `httptest` for HTTP, not hand-rolled mocks

`httptest.NewServer` and `httptest.NewRecorder` give a real `http.Handler` round-trip — closer to production than a mocked client:

```go
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // assert on r; write the response your code expects
}))
t.Cleanup(srv.Close)
client := apiClient{baseURL: srv.URL}
```

## Inject the clock — never test through `time.Now` directly

```go
type clock interface{ Now() time.Time }

type service struct{ clk clock }

// In production: clk = realClock{}
// In tests:      clk = &fakeClock{now: time.Date(...)}
```

Tests can then advance time deterministically. Real time in tests means flakes.

## Don't `time.Sleep` to wait for async work

It's flaky by construction. Use a channel, `sync.WaitGroup`, or poll with a deadline. If you must wait for a real clock, you're back to the previous rule — inject it.

## Don't over-mock

If a test has more setup-of-mocks than exercise-of-code, the test is testing the mocks. Use a fake, an in-memory implementation, or a real dependency (`sqlite`, `httptest`).

## Pre-ship checks

- Every substitution is a **consumer-side** interface, not a provider-imposed one.
- Substitution implementations are **hand-written fakes** unless there's a documented reason for a generated mock.
- No `time.Sleep` for synchronization — grep the diff.
- No direct `time.Now` calls in code under test — clock is injected.
- No race-only failures (and no `-race`-only-passing tests either).
