---
name: general-engineering-practice
description: "Cross-cutting engineering practice — when to ask clarifying questions vs. proceed, reviewing code and PR diffs, writing commit messages, diagnosing bugs and flaky tests, structuring PRs. Triggers on intent (review/debug/commit/PR), not on file context."
---

# Engineering practice

Cross-cutting habits that shape day-to-day work — asking the right question at the right time, reviewing diffs without drowning the author, writing commits that age well, debugging without flailing, and shipping PRs reviewers can actually absorb.

## Asking clarifying questions

Symmetric failure modes: plowing ahead on an underspecified request and producing the wrong thing, or asking three questions before doing any work and burning patience. Calibrate.

**Ask when all of these hold:**

1. The ambiguity affects an outcome that is hard to reverse.
2. A short investigation (grep, `git log`, read a file) wouldn't resolve it.
3. The user has the answer readily; you can't infer it from context.

Otherwise proceed, but state your assumption in one sentence first. Redirection is cheap.

**Ask, high confidence:** destructive or irreversible actions (deletes, force-pushes, external messages, shared infra); two interpretations with materially different work; missing required inputs you can't infer (endpoint, env, account ID); conflicting signals between code, user, and docs.

**Proceed with stated assumptions:** clear primary read with minor ambiguity; easily reversible work; investigation answers it faster than the user can; stylistic details where the codebase already has a convention.

**When you do ask:** lead with what you've already figured out ("I see `prod-east` and `prod-west` — which?"). Batch one round. Offer likely options. No procedural questions ("should I proceed?" — just start). No permission-laundering.

**Anti-patterns:** asking before reading; three questions to start a 10-minute reversible task; asking the same thing twice across turns; disguising a recommendation as a question.

## Code review

Flag liberally, label precisely. A `nit:` costs seconds to triage; a missed real bug costs hours. The job is accurate labels, not minimal comments.

**Compose with language and framework skills.** This section describes cross-cutting review discipline — what to flag, how to label, how to read a diff. *What counts as a bug-prone pattern in a specific language* lives in the language skills. When reviewing code, pull in the relevant ones based on file extensions actually present: `golang-*` for `.go`, `react-*` for `.tsx`/`.jsx`, `typescript-strictness` for `.ts`, `kubernetes-*` for operator manifests/controllers, `cross-language-api-contract-go-react` for any Go↔React boundary. The same rule applies when the user invokes `/review` — that loads its own playbook, but doesn't replace the per-language rule set; consult both.

**What to comment on, roughly in priority:**

1. **Bugs** — logic errors, off-by-one, broken invariants, wrong conditions, mishandled errors.
2. **Bug-prone patterns** — mutable shared state without clear ownership, public APIs inviting misuse, error paths only superficially handled.
3. **Antipatterns** for the language/framework, even if they happen to work here (`useEffect` for derived state, goroutines with no exit path, swallowed exceptions).
4. **Breaking changes** to API contracts, behavior, silent defaults.
5. **Security** — injection, missing auth checks, secrets in code, unsafe deserialization.
6. **Missing tests** for non-trivial logic, especially the categories above.
7. **Maintainability** — duplication that will diverge, leaking abstractions, functions that genuinely can't be understood.
8. **Naming that misleads or breaks consistency** with the surrounding file.
9. **Better alternatives connecting to existing code** (stdlib, internal libs, codebase conventions) — usually `consider:`.

**Don't comment on:** anything a linter catches; naming you'd choose differently where the existing one is clear; style alternatives that are a wash; theoretical edge cases that won't occur; refactors outside the change's scope.

**Severity labels, every comment:**

- *(unprefixed, blocking)* — bugs, security, broken contracts.
- `consider:` — antipatterns, alternatives. Not blocking.
- `nit:` — minor preferences, naming. Definitely not blocking.
- `question:` — you need context, not a change.

Mislabeling severity is the most common review failure: blocking on preferences, or hiding real bugs in tentative phrasing. Tone scales with severity — bugs blunt and specific, nits terse and no justification needed.

**Approve** when correct and any open comments are `nit:`/`consider:`. **Request changes** for bugs, security, broken contracts, significant antipatterns — say what would unblock. **Ask questions** when you genuinely can't tell — don't request changes on speculation.

**Reading the diff:** in context, not isolation — open the file when in doubt. Check the tests. Notice what's *missing* (removed validation, removed error handling, removed tests). Compare PR description against the code — if they disagree, ask which is right.

**Repo conventions worth flagging:** English in all code/comments/errors; no `"failed to ..."` prefix in Go error wraps; no `fmt.Printf`/`fmt.Println` in operator code paths; magic strings that look like states; CRD/API field names containing vendor terms (`rgwUser`, `keycloakRealm`).

## Commit messages

Read by people who don't have your context — sometimes years later during a `git blame` for an unrelated bug. Write for that reader.

**Subject:**

- Imperative present tense — "Fix race in worker shutdown," not "Fixed."
- Under 72 chars; no trailing period.
- Specific, not literal — says *what was accomplished*, not "Update worker.go."
- Match the repo's existing style — check `git log --oneline` first. Don't unilaterally introduce a new convention.

**Body** — skip for trivial changes. Add one when the *why* isn't obvious from the diff, when `git blame` would need context the code doesn't carry, or when there's a non-obvious tradeoff. Wrap at ~72 chars, blank line after subject, explain *why* not *what*, references as trailers (`Fixes #123`). One commit, one logical change — if the body needs "Also..." sections, split.

**Anti-patterns:** "Update X" / "Fix bug" / "Misc changes"; restating the diff; `wip`/`squash-me`/`fixup` messages in shared history; bundling unrelated changes; future tense or first person; ending with "as discussed."

**Pre-commit check:** subject reads as a complete imperative; subject says what was accomplished, not what file changed; body explains why; no WIP messages reaching shared history; issue refs in trailers, not prose; one logical change per commit.

## Debugging

Reproduce first, narrow systematically, separate known from assumed. The most common failure is jumping to a fix before the problem is understood.

**The discipline:**

1. **Reproduce it.** A bug you can't reproduce is one you can't confirm you fixed. If intermittent, find conditions that make it more frequent.
2. **State known vs. assumed** in writing. Every assumption is a hypothesis to verify.
3. **Narrow.** Bisect — toggle one variable at a time. Don't change three things and hope one was it.
4. **Find the cause, not a cause.** A change that hides the symptom isn't necessarily the fix. If you can't explain *why* it works, you're not done.
5. **Confirm the fix reproduces nothing.** Re-run the original repro, then the broader suite.

**Before changing any code:** can you reproduce it? What's the smallest input that triggers it? What recently changed (`git log`, `git bisect`, recent deploys)? What does the error literally say, word by word? Is the error from where you think — stack frames lie when inlined, async, or wrapped.

**Common failure modes:** fixing the symptom (catching an exception that shouldn't have been thrown; null-checking a value that shouldn't be null — ask *why* it's missing); believing the first plausible story; changing code to see what happens (guessing, not debugging); trusting cached state across rebuilds; reading the diff instead of the file; skipping the logs; stopping at the first green test.

**Tools, cheapest first:** read the error and stack trace; read the failing-path code; logging/prints at boundaries; debugger; `git bisect` for regression-shaped bugs; tracing/profiling for timing-shaped ones.

**Flaky bugs are real bugs with hidden inputs** — time, ordering, concurrency, RNG seeds, test pollution. Suspects: shared mutable state between tests, timeout races, order-dependent tests, time/timezone dependence, external services. Don't retry-loop them; document the suspected cause if you can't fix now.

**Stop and ask** after 30+ minutes with no narrowing, when the bug crosses a system boundary you don't own, when repro needs production data you can't get, or when two equally plausible hypotheses fit all evidence. Ask with what you've ruled out, not just "it's broken."

**Before declaring it fixed:** original repro no longer fails; you can articulate the cause in one sentence; you know *why* the fix works; a test now exists that would have caught it (or you've explicitly decided one isn't worth it); no new failures in the broader suite.

## PR hygiene

A PR is a unit of review. Reviewer attention is finite and usually the team bottleneck — right-size accordingly.

**Break large PRs into a stack against a feature branch.** Typical layered shape: define the CRD with unit tests → implement reconciler logic → cross-resource wiring → CI/RBAC/infra → e2e tests. Each step reviewed and merged into the feature branch independently; only the final merge into `main` is the big one.

**Squash when noisy.** Twenty `wip`/`fix lint`/`address review` commits drown the actual work. Heuristic: fewer than ~5 commits, each one a coherent change `git log --oneline` would respect → keep them. Otherwise squash.

**Don't let IDE refactor tools cause collateral.** After "rename across project," scan the file list, not just patches. Anything outside the intended scope is a smell — revert before pushing.

**One PR, one reason to change.** If you'd describe it with "and," consider splitting. Exception: a small mechanical refactor enabling a fix is fine together — say so in the description. Don't bundle "while I'm here" cleanups into a feature PR.

**Other patterns:** draft PRs early — runnable code with rough edges beats polished and late. Description orients the reviewer (*why* and *where to focus*; the diff already shows *what*). Self-review the diff before requesting review.

**Avoid:** merging straight to `main` for multi-layer work; force-pushing after review starts without saying so; leaving `fix lint`/`address comments` commits in shared history.

**Pre-review checklist:** full self-review; description names the why and focus areas; no accidental files (generated output, IDE configs, scratch); no mass-rename collateral outside scope; tests included or explicitly justified; commit history squash-ready or already meaningful.
