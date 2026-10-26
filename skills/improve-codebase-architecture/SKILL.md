---
name: improve-codebase-architecture
description: Scan codebase for deepening opportunities, present markdown report, and grill through candidate.
disable-model-invocation: true
---

# Improve Codebase Architecture

Surface architectural friction and propose **deepening opportunities**: refactoring shallow modules into deep ones to improve locality, leverage, and testability.

Design rules and vocabulary derive from the `coding-standards` skill (**deep modules**, **information hiding**, **leakage**, **cohesion**, **seams**). Domain terms derive from `GLOSSARY.md`. Prior decisions derive from ADRs in `docs/adr/`.

## Process

### 1. Explore

**Scope before scan**: Deepening pays off on code that changes.

- If user specified a subsystem or pain point, start there.
- Otherwise, inspect git history (`git log --oneline`) to identify hot spots.

Consult `GLOSSARY.md` and relevant ADRs before inspecting code.

Walk the codebase (dispatch a research subagent if broad) to identify friction signals defined in the `coding-standards` skill and the `refactoring` skill:

- **Shallow modules & pass-through layers** (`Middle Man` smell): interfaces nearly as complex as implementation; layers adding no abstraction.
- **Information leakage**: module design decisions split across callers.
- **Lost cohesion & change amplification** (`Divergent Change`, `Shotgun Surgery` smells): single changes touching many modules, or one module changing for unrelated reasons.
- **Seam leaks & Demeter violations**: callers navigating internals rather than telling.
- **Test friction**: tests testing past the interface, or mocking non-boundary collaborators instead of using real logic or fakes.

Apply the **deletion test**: would deleting the module concentrate complexity (earning its keep) or vanish/move it (shallow pass-through)?

**Completion criterion**: 2–4 candidate clusters identified with file paths, friction diagnosis, and preliminary deepening direction.

### 2. Present candidates as Markdown report

Write a self-contained markdown report to the OS temp directory (`$TMPDIR` or `/tmp`), saved to `<tmpdir>/architecture-review-<timestamp>.md`. Notify the user of the file path.

Structure the report for high signal and low token count:

````markdown
# Architecture Review: <repo-name>

**Date**: YYYY-MM-DD | **Scope**: <analyzed paths or hot spots>

## Candidates

### 1. <Title naming the deepening>

- **Strength**: Strong | Worth exploring | Speculative
- **Files**: `path/to/file1`, `path/to/file2`
- **Friction**: <1-2 sentences citing `coding-standards` terms: shallow module, leakage, change amplification>
- **Deepened Design**: <1-2 sentences on consolidated interface and seam placement>
- **Wins**:
  - Locality: <bugs/changes concentrate in one place>
  - Leverage: <one interface, N callers>
  - Tests: <public interface becomes test surface>
- **ADR Callout** (if applicable): <conflict note, e.g. "Revisits ADR-0007 because...">
- **Diagram**:
  ```mermaid
  flowchart LR
    subgraph Before
      ...
    end
    subgraph After
      ...
    end
  ```
````

## Top Recommendation

<Candidate name and 1-sentence rationale>

Keep diagrams concise (`flowchart LR`). Keep win bullets <= 6 words. Avoid boilerplate prose. Do NOT propose full interfaces yet.

**Completion criterion**: Markdown report written to disk, path reported, and user prompted: _"Which of these would you like to explore?"_

### 3. Grilling loop & execution handoff

Once the user selects a candidate:

1. **Grill the design**: Call the Skill tool with "grilling" to walk constraints, dependencies (classify via `DEEPENING.md` in the `codebase-design` skill), target interface, internal vs external seams, and surviving test surfaces.
2. **Explore alternatives**: Apply **Design it twice** (Writing section of the `coding-standards` skill) if the interface shape has non-obvious trade-offs.
3. **Capture side effects**:
   - New or refined domain terms: update `GLOSSARY.md`.
   - Load-bearing rejections: offer to record an ADR in `docs/adr/`.
4. **Execution handoff**: Once design is approved, hand off implementation to the `refactoring` skill (characterization tests, small moves, 2-hat commits) and the Tidying section of the `coding-standards` skill (tidying first).
