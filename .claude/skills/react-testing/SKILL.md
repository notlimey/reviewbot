---
name: react-testing
description: "React component tests with React Testing Library/Vitest/Jest — render, screen.getByRole, userEvent, MSW handlers, act() warnings, QueryClient in tests. Not for Playwright/Cypress E2E or non-React testing."
paths:
  - "**/*.test.tsx"
  - "**/*.test.ts"
  - "**/*.spec.tsx"
  - "**/*.spec.ts"
  - "**/__tests__/**"
---

# React Testing

React Testing Library + Vitest (or Jest). The failure mode this targets: tests that pass while the component is broken, or that break on every refactor without catching real bugs.

## Core Rules

**Test from the user's perspective.** Query by role, label, or text — what a user can see or do. `getByRole('button', { name: /save/i })` survives refactors; `getByTestId('save-btn')` is implementation detail with extra steps.

**Query priority order:** role → label → placeholder → text → display value → alt → title → testid. Reach lower only when higher options genuinely don't exist (e.g. an icon-only button with no accessible name — fix the a11y bug first, then test).

**`userEvent` over `fireEvent`.** `userEvent.click()` runs the full event sequence (pointerdown, focus, click); `fireEvent.click` is a single synthetic event that misses bugs real clicks surface. Set up with `const user = userEvent.setup()` per test, not at module scope — it carries pointer state.

**`findBy*` for async, never `waitFor` + `queryBy`.** `findByText(...)` retries until the element appears or times out. `waitFor(() => expect(queryByText(...)).toBeInTheDocument())` is the same thing with extra lines and worse failure output.

**One render per test.** No `rerender` to swap props mid-test except when verifying a prop-change effect. A second render usually means the test is checking two things.

**Reset per test, not globally.** Fresh `QueryClient`, fresh router, fresh MSW handlers per test. State leaking between tests is the single biggest source of flakes.

## Setup Patterns

**`renderWithProviders` helper that matches production wrappers.** Tests should mount components in the same provider tree as the app — `QueryClient`, router, theme, i18n. Hand-mocking what providers expose breeds tests that pass while production crashes.

```ts
function renderWithProviders(ui: ReactNode, { route = "/" } = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return {
    user: userEvent.setup(),
    ...render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={[route]}>{ui}</MemoryRouter>
      </QueryClientProvider>
    ),
  };
}
```

**MSW for HTTP, never `vi.mock('axios')`.** Mock at the network boundary, not the client library. MSW handlers survive a switch from axios to fetch to TanStack Query; module mocks don't.

**One `QueryClient` per test with `retry: false`.** Otherwise a failed mock retries 3× with backoff before the test times out, and you get cryptic `act()` warnings instead of the real failure.

## Patterns to Reach For

**`screen.*` over destructured queries.** Easier to grep, harder to accidentally query an unmounted tree.

**`await screen.findByRole(...)` as the synchronization point.** Use the find query to wait for the UI to settle after an action — then assert.

**`within(region).getByRole(...)` to scope queries.** When the page has multiple "Save" buttons, scope to the section under test instead of disambiguating by index.

**Test the integration, mock at the seam.** Render the feature; mock the network. Don't render `<Form>` in isolation with mocked `<TextField>`s — that tests the test harness.

**Snapshot tests only for stable, structured output.** API response transformers, formatters, generated SVGs. Never snapshot rendered components — the diff is unreadable and reviewers rubber-stamp updates.

## Patterns to Avoid

**Don't assert on `useState` or internal hook state.** If the user can't see it, the test shouldn't either. Re-render with new props and assert on what's now visible.

**Don't `act(() => { ... })` manually.** RTL wraps user events and queries in `act` already. Manual `act` calls usually mask a real async bug — find it instead.

**Don't mock the hooks of the component under test.** Mocking `useQuery` to return fixed data tests nothing. Mock the network with MSW; let the real hook run.

**Don't query by class name or DOM structure.** `container.querySelector('.btn-primary')` breaks on every CSS refactor and tells you nothing about user-visible behavior.

**Don't test third-party libraries.** A test that asserts `<DatePicker>` opens when clicked is testing the library. Test your integration: that selecting a date calls your handler with the right value.

**Don't suppress `act()` warnings.** Each one is a real bug: an update happened after the test thought it was done. Find what's still pending and `await` it.

## Mandatory Checks Before Shipping

1. **Tests pass under `--shuffle` / random order.** Catches shared state.
2. **No `act()` warnings in output.** Each one is a bug.
3. **Every async wait uses `findBy*` or `waitFor` with an assertion** — never bare `setTimeout`.
4. **Network is mocked at MSW, not at the HTTP client library.**
5. **No `getByTestId` unless higher-priority queries genuinely can't reach the element.** When they can't, fix the a11y issue first.
6. **`QueryClient` has `retry: false` for tests using TanStack Query.**
7. **No snapshot tests of rendered components.**

## When Not to Test

Pure presentational components with no logic, no state, no effects (`<Spacer>`, `<Divider>`) — visual review covers them. If there's a branch, an effect, or user interaction, write the test.
