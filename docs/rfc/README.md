# RFCs

This directory holds Request for Comments documents for Podium spec changes. An RFC records a proposed change to the specification, its motivation, and the alternatives considered. The governance model and how RFCs feed decisions are described in [`GOVERNANCE.md`](../../GOVERNANCE.md).

## When to file an RFC

File an RFC for any change to the normative specification under `spec/`, for new first-class artifact types or SPIs, and for changes to wire contracts or storage formats. Routine bug fixes, documentation edits, and internal refactors do not require an RFC; open a pull request or a GitHub Discussion instead.

## Numbering

Each RFC has a four-digit number assigned in filing order, starting at `0001`. The number is permanent once assigned and is never reused, even if the RFC is withdrawn or rejected. The file name is `NNNN-short-title.md`, for example `0001-layer-composition-order.md`.

## Format

Each RFC is a single markdown file with the following sections:

- **Title and metadata.** RFC number, title, author, status (`draft`, `accepted`, `rejected`, or `withdrawn`), and the date of the last status change.
- **Summary.** One paragraph stating the proposed change.
- **Motivation.** The problem the change solves and why the current behavior is insufficient.
- **Specification.** The proposed change in enough detail to implement, including the affected `spec/` sections.
- **Alternatives.** Other approaches considered and the reason each was set aside.
- **Open questions.** Unresolved points that need discussion before acceptance.

## Lifecycle

A draft RFC is filed as a pull request adding the file to this directory. Discussion happens in the open on the pull request or in a linked GitHub Discussion. The maintainer records the decision by setting the RFC status and merging the file, including the rationale for a rejected proposal so the record is complete.
