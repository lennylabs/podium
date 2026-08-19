# Spec-driven and test-driven development

The governing rule for how change happens in this repository. Podium is built spec-first and test-first: the technical specification under `spec/` is the single source of truth, code implements the spec, and tests verify the spec. It frames the companion rules [`code-best-practices.md`](code-best-practices.md), [`test-coverage.md`](test-coverage.md), and [`doc-style.md`](doc-style.md).

## Top-level principle

All new code aligns to the spec. Every behavior in `pkg/`, `cmd/`, `internal/`, and `sdks/` traces to a spec section, and a test pins that behavior to the section. Code with no spec basis is not written, and a test that encodes no spec requirement is not the test to write.

## The spec is the source of truth

- Implement what the spec says. When the code and the spec disagree, the spec is right and the code is the defect, unless the spec itself is wrong (see below).
- Cite the spec on spec-derived logic with `// Spec: §X.Y`, and cite a matrix cell with `// Matrix: §X.Y (<cell>)`. Both are machine-checked: `speccov-drift` maps tests to the sections they verify through the first, and `matrix-audit` maps them to matrix cells through the second. Cite by section number and never by line number, because line numbers shift.
- Do not edit `spec/` to match code you want to write. Spec changes go through the pipeline below.

## When the spec is silent, wrong, or contradictory

Change the spec first, through the proposal pipeline, then write the code:

1. `change-proposal` writes and adversarially converges a proposal under `proposals/` that stages the spec edits.
2. A human approves it by setting the proposal's status to `Approved`.
3. `implement-proposal` lands the staged edits in `spec/`, verifies them, commits them on their own, and then implements the code against the now-current spec.

Never let code lead the spec. A spec change lands and is verified before the code that depends on it is written.

## Test-driven

- Tests encode the spec's required behavior, including the empty, error, concurrent, boundary, and spec-named-failure paths, at every level the change reaches (see [`test-coverage.md`](test-coverage.md)). Happy-path coverage alone does not satisfy this rule.
- A behavior is not done until a test pins it to its spec section and that test passes. Run the tests; writing them is half the work.
- Tests are first-class spec artifacts. A test that verifies a spec section carries the `// Spec: §X.Y` annotation, and every behavioral spec section has at least one test that cites it. `speccov-drift` fails when a section loses its last citation.

## The consistency gate

`make coverage-gate` runs `lint`, `speccov-drift`, `matrix-audit`, `doccov-check`, and `coverage-budget`. It is the mechanical check that the spec, the code, the matrices, and the documentation still agree, and a change is not done until it passes. Three of its parts are obligations a change creates rather than checks it merely satisfies:

- A new behavioral spec section needs a test citing it.
- A new matrix cell (§6.7.1 harness capabilities, §6.10 error codes, §4.6 visibility, §4.3 and §4.3.5 types) needs a `// Matrix:` annotated test.
- A new runnable documentation example needs its `tools/doccov/manifest.yaml` entry and the end-to-end test that executes it.

## Mirrored surfaces

Some spec content is mirrored in code, and the mirror is authoritative for what runs. A change to one side is incomplete without the other:

- The §6.7.1 capability matrix lives in `spec/06` and in `pkg/adapter/capability.go`.
- The §6.7 per-type target paths live in `spec/06` and in the adapters, pinned by the golden files in `test/materialization/testdata/golden/`.
- The §6.10 error codes live in `spec/06` and in the code that returns them, checked by `matrix-audit`.

Verify direction in the code rather than from memory, and change both sides in the same commit.

## Where this rule applies

- All Go under `pkg/`, `cmd/`, and `internal/`, the SDKs under `sdks/`, and the tests under `test/`.
- It governs the proposal pipeline skills, `change-proposal` and `implement-proposal`, which exist to keep the code and the spec in lockstep.

## How to apply when implementing a change

1. Find the spec section the change implements. When none exists, or the existing one is wrong, stop and take it through the proposal pipeline before writing code.
2. Land and verify any spec change first, then implement the code against the committed spec.
3. Write the tests that encode the spec's behavior at every level the change reaches, with the `// Spec:` annotation, and run them to green.
4. Cite the spec section in the code and in the tests.
5. Run `make coverage-gate` and resolve what it reports.

## Escape hatches

- Build tooling, test harnesses, and developer utilities (`tools/`, `internal/testharness/`, `internal/testenv/`) implement the testing and operational model rather than a spec behavior. They cite the spec where one applies and are otherwise governed by [`code-best-practices.md`](code-best-practices.md).
- A pure refactor that changes no behavior creates no new spec obligation. The existing citations and tests must still hold.
- The documentation site generator under `site/` implements no spec behavior and is governed by its own README and by [`doc-style.md`](doc-style.md).

## Maintenance

When a change surfaces a spec-to-code divergence this file does not cover, add the surface to the mirrored-surfaces list. Keep the list to mirrors that are actually enforced, so it stays a checklist rather than a description.
