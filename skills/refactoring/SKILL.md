---
name: refactoring
description: Code smells and named refactorings. Use when restructuring code beyond a tidying, or naming smells in review.
---

# Refactoring

Restructure code without changing behaviour, drawn from _Refactoring_ (Fowler). Builds on the `coding-standards` skill: where a smell has a standards term, that term is the source of truth. Smells and refactorings are vocabulary: name them in plans, commits, and review findings.

- **Two hats**: refactoring and adding behaviour are separate steps, never mixed in one commit.
- **Rule of three**: tolerate a second copy; extract on the third.

## Steps

1. **Name the smell** from the [catalog](#smells), with its location.
2. **Get to green.** Run the tests. If the code under change lacks coverage, add characterization tests first, pinning current behaviour.
3. **Apply the named refactoring** in small moves, running tests after each move. A red test means revert the move, not patch forward.
4. **Commit structure apart from behaviour**, one refactoring per commit where practical.

Done when: every named smell is fixed or explicitly deferred with a reason, tests stayed green after every move, and the diff changes no behaviour.

## Smells

| Smell | Signal | Refactorings |
| --- | --- | --- |
| **Mysterious name** | Name hides purpose (see **precise names**) | Rename Function / Variable / Field |
| **Duplicated code** | Same knowledge in two places (see **DRY**) | Extract Function, Slide Statements, Pull Up Method |
| **Long function** | Body needs comments to navigate | Extract Function, Decompose Conditional, Split Loop |
| **Long parameter list** | Many args, often flags | Introduce Parameter Object, Preserve Whole Object, Remove Flag Argument |
| **Global data** | Shared state anyone can write | Encapsulate Variable, narrow scope |
| **Mutable data** | Updates in one place break another | Encapsulate Variable, Separate Query from Modifier, Change Reference to Value |
| **Divergent change** | One module changes for unrelated reasons (lost **cohesion**) | Split Phase, Extract Class, Move Function |
| **Shotgun surgery** | One change edits many modules (see **change amplification**) | Move Function / Field, Combine Functions into Class, Inline Class |
| **Feature envy** | Function uses another module's data more than its own | Move Function, Extract Function |
| **Data clumps** | Fields that always travel together | Extract Class, Introduce Parameter Object |
| **Primitive obsession** | Raw strings/ints standing in for domain concepts | Replace Primitive with Object, Replace Type Code with Subclasses |
| **Repeated switches** | Same branching on type in several places | Replace Conditional with Polymorphism |
| **Speculative generality** | Hooks and params for futures that never came (see **good-enough software**) | Collapse Hierarchy, Inline Function / Class, Remove Dead Code |
| **Middle man** | Module only delegates (see **pass-through methods**) | Remove Middle Man, Inline Function |
| **Message chains** | `a.b().c().d()` (see **Law of Demeter**) | Hide Delegate, Extract Function, Move Function |
