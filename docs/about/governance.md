---
title: Governance
nav_order: 3
description: "How decisions are made: the maintainer model, transition criteria, and the proposal process."
---

# Governance

This page describes how decisions about Podium are made: the current maintainer model, the criteria for moving to a steering committee, and the proposal process for spec changes. It also lists the license terms and the contact points.

## Current model: Benevolent Dictator for Now (BDfN)

During the design and early build phases, a single maintainer has final decision authority over architecture, merges, releases, and community policy. The model is intentionally lightweight and is not intended to be permanent.

## Transition to a steering committee

The BDfN model transitions to a multi-maintainer steering committee when the project has **three or more regular contributors**. A regular contributor is someone who has done all of the following:

- Landed substantive changes beyond typos or docs in `main`.
- Participated in architectural discussions or proposal reviews.
- Sustained that activity for at least three months.

When the criteria are met, the transition follows these steps:

1. The maintainer proposes an initial committee.
2. The committee drafts a charter that covers decision rules, voting, membership, and rotation.
3. This page is updated to describe the new model.

## Decision-making

Spec changes follow a proposal process. A proposal is a markdown file in [`proposals/`](https://github.com/lennylabs/podium/tree/main/proposals) named `NNNN-<slug>.md`, where `NNNN` is the next free four-digit number. A proposal states the problem, the decisions that constrain the design and their rationale, and the changes it stages. Those changes are spec edits, code, tests, and documentation, or any combination of them. A proposal does not edit `spec/` itself.

The proposal's `Status` bullet records its state, which normally moves through these stages:

1. **Draft.** The proposal is written.
2. **Verified.** Adversarial review rounds check the proposal against the spec and the code until a complete review pass finds no confirmed defect.
3. **Approved.** A maintainer records sign-off by setting the status to `Approved`.
4. **Applied to spec.** The staged spec edits land in `spec/`, are verified against the proposal, and are committed on their own before any code that depends on them. A proposal that stages no spec edit skips this stage.
5. **Implemented.** The code, tests, and documentation are implemented against the updated spec.

A proposal that is not adopted is set to `Withdrawn`, with the reason in its status, and stays in `proposals/`. A proposal file normally lands on the same branch as its spec and implementation commits and merges to `main` through a pull request.

- **Proposals** are written as numbered files in [`proposals/`](https://github.com/lennylabs/podium/tree/main/proposals). A problem can be raised first as an issue or a GitHub Discussion, and a proposal's `Issue` bullet links the issue when one exists.
- **Discussion** happens in the open, and anyone can participate.
- **Decisions** are recorded with their rationale in the proposal's Decisions section. The `Status` bullet records whether the proposal was approved, implemented, or withdrawn, and gives the reason for a withdrawal.
- After the steering committee forms, consensus is the preferred decision mode, and a simple-majority vote is the fallback.

## License and DCO

- **License.** Podium is licensed under [MIT](https://github.com/lennylabs/podium/blob/main/LICENSE). Contributions are accepted under the same license.
- **Developer Certificate of Origin.** Every commit must be signed off with `git commit -s`. Podium requires no separate CLA.

## Contact

- **Issues and discussions:** [github.com/lennylabs/podium](https://github.com/lennylabs/podium)
- **Security:** the [security policy](https://github.com/lennylabs/podium/blob/main/SECURITY.md)
- **Conduct:** the [Contributor Covenant](https://github.com/lennylabs/podium/blob/main/CODE_OF_CONDUCT.md)
