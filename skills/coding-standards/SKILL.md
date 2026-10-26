---
name: coding-standards
description: Coding standards from APOSD, The Pragmatic Programmer and Tidy First. Use when writing, reviewing, or planning code changes.
---

# Coding Standards

Language-and-repo-agnostic principles for writing and reviewing code, drawn from _A Philosophy of Software Design_
(APOSD, Ousterhout), _The Pragmatic Programmer_ (PP, Hunt & Thomas), and _Tidy First?_ (TF, Beck). Bolded terms are the
vocabulary: use them by name in plans, commits, and review findings.

Repo-local conventions override this doc where they conflict.

## Writing

1. **Read first.** Learn the surrounding code's idioms, naming, and existing helpers. Reuse before adding.
2. **Design it twice** (APOSD). Draft the interface and its interface comment before the body. Sketch 2–3 alternatives;
   pick the deepest.
3. **Tidy first** (TF): make the change easy, then make the easy change. See [Tidying](#tidying).
4. **Implement in small steps** against the interface, keeping the build and tests green after each. Use
   **tracer bullets** (PP) when the path is uncertain: a thin end-to-end slice first, then fill in.
5. **Test** once the interface stabilises (or alongside). See [Tests](#tests).

Done when: tests green, tidyings and behaviour changes sit in separate commits, and the diff contains only what the task
needs.

## Reviewing

Apply every rule under [Rules](#rules) to every changed file. Each finding names the rule's term, the location, and a
concrete fix. Priorities:

1. Correctness
2. Architecture and design
3. Tests
4. Performance
5. Naming and comments

Done when: every changed file is checked against every rule, and findings are ranked by priority.

## Rules

### Complexity

**Complexity** (APOSD) is anything that makes code hard to understand or change. Its symptoms: **change amplification**
(one change touches many places), **cognitive load** (much to know before changing anything), **unknown unknowns**
(unclear what must change). Unknown unknowns are the worst. Every rule here reduces one of the three.

- Program **strategically** (APOSD): spend a little extra now on good design; every change leaves structure as good or
  better than found. Tactical "just make it work" accretes complexity one shortcut at a time.
- **Broken windows** (PP): fix bad code you touch, or flag it. Unchecked rot spreads.
- **Good-enough software** (PP): match quality to need. Build what the task requires; generality waits for a real second
  use case.

### Modules

- **Deep modules** (APOSD): small interface, large implementation (e.g. Unix file I/O: `open/read/write/close` hides
  disks, caches, permissions). A **shallow module**'s interface costs nearly as much to learn as its body; merge or
  deepen it.
- **Information hiding** (APOSD): each module owns its design decisions (formats, algorithms, storage).
  **Information leakage**, where one decision is known by several modules, couples them; consolidate it in one place.
- **Pull complexity downward** (APOSD): the module absorbs hard cases so callers stay simple. Sensible defaults over
  required config.
- **Different layer, different abstraction** (APOSD): each layer adds meaning. **Pass-through methods** and
  **pass-through variables** signal a layer adding nothing.
- **Somewhat general-purpose** (APOSD): interfaces general enough for nearby uses, implementations specific to today's
  need.
- **Define errors out of existence** (APOSD): shape semantics so the error case can't arise (e.g. delete-if-present is
  idempotent) instead of making every caller handle it.

### Coupling

**Coupling** (TF, after Constantine): two elements are coupled when changing one forces a change to the other. The cost
of software is the cost of changing it, and coupling drives that cost. **Cohesion** is the remedy: elements that change
together live together, so a change stays in one place.

- **DRY** (PP): every piece of _knowledge_ has one authoritative representation. Code that looks alike but encodes
  different knowledge is not duplication; leave it apart.
- **Rule of three**: tolerate a second copy; extract on the third.
- **Extend before adding** (house rule): grow an existing function/struct when the change fits its abstraction. Create a
  new one when extending would make it change for unrelated reasons (lost **cohesion**).
- **Orthogonality** (PP): changing one module leaves others unaffected. Test: how many modules does a typical change
  touch?
- **Tell, don't ask** / **Law of Demeter** (PP): ask an object to do work instead of reaching through its internals.
  Violations show up as chains like `a.b().c().d()`, coupling the caller to a navigation path.
- **Reversibility** (PP): keep vendors, storage, and protocols behind seams so decisions stay changeable.

### Correctness

- **Design by contract** (PP): state preconditions, postconditions, and invariants at interfaces; validate at trust
  boundaries.
- **Crash early** (PP): assert what "can't happen", and fail loudly at the first impossible state. A dead program does
  less damage than a corrupted one.
- **Program deliberately** (PP): know why code works. Code that passes by coincidence (untested assumptions, magic
  sleeps, order luck) fails later.
- **Handle errors once** (house rule): handle each error at the layer that can act on it. Wrap with context when
  propagating.

### Performance

- **Measure before optimising** (PP): profile first; optimise only the measured hot path. Clean code is easier to tune
  than clever code.
- **Estimate algorithm cost** (PP): know the Big-O of changed code. Flag work that grows with input in a hot path:
  nested loops over unbounded collections, a query or remote call per item (N+1), repeated recomputation of the same
  result.
- **Design around the critical path** (APOSD): the common case runs the fewest steps; special cases branch off it.

### Naming and comments

- **Precise, consistent names** (APOSD): one concept, one name, everywhere; length sized to scope. If naming is hard,
  the design is muddy; fix the design.
- **Comments describe what the code can't** (APOSD): intent, the why, invariants, units, non-obvious constraints.
- **Interface comments** (APOSD) state everything a caller needs and nothing about internals.
  **Implementation comments** explain a non-obvious how. Both sit at a different level of detail than the code they
  annotate.
- Update comments in the same change as the code they describe.

### Tidying

A **tidying** (TF) is a tiny structure change, behaviour unchanged: minutes of work, a handful of lines.

- **Structure vs behaviour** (TF): tidyings and behaviour changes go in separate commits. Structure changes are
  reversible and quick to review; behaviour changes need full scrutiny.
- **First, after, later, never** (TF): tidy first when it makes this change cheaper; after when it eases the next change
  here; later (note it) when it's big; never when the code won't change again.
- **Stop when the change is easy** (TF): tidy only until the behaviour change is easy. Keep batches small enough to
  review in minutes.
- Tidyings to apply by name:
  - **Guard clauses**: return early on preconditions; the main path stays un-nested.
  - **Dead code**: delete it; version control remembers.
  - **Normalize symmetries**: one idiom for one job.
  - **New interface, old implementation**: add the interface you wish existed, implemented by calling the old one;
    migrate callers.
  - **Reading order**, **cohesion order**: order code the way a reader needs it; place elements that change together
    side by side.
  - **Move declaration and initialization together.**
  - **Explaining variable**, **explaining constant**: name a subexpression or magic literal.
  - **Explicit parameters**: pass inputs in rather than reading globals, env, or maps deep inside.
  - **Chunk statements**: a blank line between the steps of a block.
  - **Extract helper**: pull a block with one clear purpose into a named function.
  - **One pile**: when code is fragmented past understanding, inline it into one place, then re-split.
  - **Explaining comment**, **delete redundant comment**: add the why a reader missed; remove what the code already
    says.
- Restructuring beyond a tidying (smells, moving responsibility across modules): call the `refactoring` skill.

### Tests

- Unit tests almost always, integration tests often, E2E tests rarely.
- Test the code under change. Trust imported packages to work as documented.
- Assert on behaviour and contracts through public interfaces. Tests survive tidyings, internal restructuring, and
  wording changes to messages; exact output strings are asserted only when the format is itself the contract.
- Mock only what crosses a boundary: external APIs, payment rails, cloud SDKs, slow hardware. Everything else runs real
  logic.
- Prefer in-memory **fakes** (a map-backed store implementing the interface) over dynamic mocks with call expectations.
- Tests are deterministic and isolated: runnable in parallel, in any order.
- Cover edge cases, error paths, and state transitions; skip trivial accessors and boilerplate.
