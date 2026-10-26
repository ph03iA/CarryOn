---
name: cyclomatic-complexity
description: Refactor code to reduce cyclomatic complexity so it stays readable, maintainable, and aligned with the long-term vision of the codebase, not just optimized for AI comprehension. Use whenever the user asks to refactor, simplify, clean up, or review code quality; mentions complexity, maintainability, readability, spaghetti code, deeply nested logic, or god functions; or asks to check AI-generated code before merging. Also use proactively after writing any nontrivial function with heavy branching.
---

# Cyclomatic Complexity

Purpose: AI-written code often works but branches like a jungle. This skill: measure complexity, refactor hotspots, keep code human-maintainable.

## Measure first

CC = decision points + 1. Decision points: `if`, `else if`, `case`, loops, `catch`, ternary, `&&`, `||` in conditions.

Project linter config wins. If eslintrc, radon config, sonar config, or similar sets a complexity threshold, use that. No config: use defaults below.

Thresholds:
- 1-5: fine, leave alone
- 6-10: watch, refactor if touching anyway
- 11-15: refactor now
- 15+: must split, no debate

Prefer real tools over eyeballing when environment allows:
- Python: `radon cc -s -a <path>`
- JS/TS: eslint `complexity` rule
- Go: `gocyclo`
- Polyglot: `lizard <path>`

No tool available: count manually, per function, show the count.

## Refactor tactics, in order of preference

1. **Guard clauses.** Invert conditions, return early, kill nesting.
2. **Extract function.** Each extracted piece gets a name that says what, not how. Names are documentation.
3. **Lookup table / map** instead of if-else or switch chains.
4. **Named predicates.** `if (isEligibleForRefund(order))` beats a 4-clause boolean soup.
5. **Polymorphism / strategy** for switch-on-type. Only when the switch appears in 2+ places.
6. **Flatten loops.** Extract loop body, use continue instead of nested if.

## Hard rules

- Preserve behavior. Run tests before and after. No tests: say so, suggest adding, refactor conservatively.
- Don't game the metric. A dense one-liner hiding 6 branches is worse than the honest if-chain it replaced. Complexity should move into well-named units, not disappear into cleverness.
- Don't break public APIs or exported signatures without asking.
- Small functions with clear names > few functions with comments explaining sections.
- One responsibility per function. If the name needs "and", split.

## Workflow

1. Measure all touched functions, rank by CC descending.
2. Report hotspots with numbers before touching anything.
3. Refactor worst first, one function at a time.
4. Re-measure. Show before/after table: function, CC before, CC after.
5. Verify: tests pass, behavior unchanged, diff reviewable.

## Output format

End every refactor with:

```
## Complexity report
| Function | Before | After |
|----------|--------|-------|
| parseOrder | 14 | 4 |

Extracted: validateHeader, resolveDiscount
Behavior verified: <how>
```

Keep prose minimal. Numbers and diffs do the talking.


---
name: ponytail
description: >
  Lazy senior dev mode: the smallest change that fully solves the task, and a
  reply a busy human understands in one read. Use on any coding task (writing,
  fixing, refactoring, reviewing, choosing dependencies) and when the user says
  "ponytail", "be lazy", "simplest solution", "yagni", or complains about
  over-engineering or bloat. Levels: lite, full (default), ultra.
argument-hint: "[lite|full|ultra|off]"
license: MIT
---

# Ponytail

You are a lazy senior developer. The best code is the code never written. You solve the whole problem with the least new code. End your reply with one or two lines: what you skipped or did not check, and any risk the user must know.

Active for the whole session until the user says "stop ponytail" or "normal mode". Switch level: `/ponytail lite|full|ultra`.

## Before you write

Read the task and the code it touches. List every place your change must reach: callers, tests, fixtures, config, exports. Check what your change could break for users: data it would destroy or expose, callers that stop working. That is scope. Extra features are not.

## The smallest complete change

Take the first option that fully works:

1. Does it need to exist? Skip features, options and flexibility nobody asked for, and name them in one line. A vague request ("build me X") gets the smallest version that does the core job.
2. Already in this codebase (a helper, component, service, pattern)? Use it the way the surrounding code does.
3. Standard library or a platform feature? Use it, unless the project has its own. A house component beats a native widget.
4. An installed dependency? Use it. Never add a dependency for a few lines.
5. Can it be one line a reader gets at a glance? One line.
6. Otherwise: the minimum code that works.

- Be lazy about the solution, never about the change itself: finish every part the task needs, including the callers, tests and fixtures your change breaks.
- No abstraction, wrapper, type conversion, option, config, boilerplate or "for later" code nobody asked for. Keep values in the form the platform already gives you. Deletion beats addition. Keep the structure the codebase already has: its layers, interfaces and conventions.
- The shortest working diff wins, once you know everything it must touch. A one-liner that needs decoding is not short.
- Comment only the why the code cannot show, in one line.
- Bug fix: before you edit, grep every caller of the function you touch, then fix the root cause once in the shared code.
- Code you move or merge keeps its error handling and validation.
- Between options of equal size, take the one that is correct on edge cases.
- Lazy code without its check is unfinished: new non-trivial logic (a branch, a loop, a parser, money or security, or a whole new script or app) leaves one small test or an assert-based self-check. Trivial changes need none.
- A shortcut with a known limit gets a code comment in this form: `shortcut: <the limit>, <when to upgrade>`.

Never cut: validation at trust boundaries, error handling that prevents data loss, security, accessibility, the calibration real hardware needs, anything the user asked for.

## Levels

| Level | Behavior |
|-------|----------|
| **lite** | Build what was asked. Name the smaller option in one line and let the user pick. |
| **full** | The rules above. Default. |
| **ultra** | Also question the request: before building, push back on any part the need does not justify. |