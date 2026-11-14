---
title: Governance
nav_order: 3
description: "How decisions are made: the maintainer model, transition criteria, and the proposal process."
---

# Governance

This page describes how decisions about Podium are made: the current maintainer model, the criteria for moving to a steering committee, and the RFC process for spec changes. It also lists the license terms and the contact points.

## Current model: Benevolent Dictator for Now (BDfN)

During the design and early build phases, a single maintainer has final decision authority over architecture, merges, releases, and community policy. The model is intentionally lightweight and is not intended to be permanent.

## Transition to a steering committee

The BDfN model transitions to a multi-maintainer steering committee when the project has **three or more regular contributors**. A regular contributor is someone who has done all of the following:

- Landed substantive changes beyond typos or docs in `main`.
- Participated in architectural discussions or RFC reviews.
- Sustained that activity for at least three months.

When the criteria are met, the transition follows these steps:

1. The maintainer proposes an initial committee.
2. The committee drafts a charter that covers decision rules, voting, membership, and rotation.
3. This page is updated to describe the new model.

## Decision-making

Spec changes follow an RFC process. A Request for Comments (RFC) document records a proposed change, its motivation, and the alternatives considered. RFCs live in `docs/rfc/`, whose index describes the document format and numbering.

- **Proposals** are filed as GitHub Discussions or as draft RFCs in `docs/rfc/`.
- **Discussion** happens in the open, and anyone can participate.
- **Decisions** are recorded with their rationale in the relevant RFC or issue. Rejected proposals carry their rationale as well.
- After the steering committee forms, consensus is the preferred decision mode, and a simple-majority vote is the fallback.

## License and DCO

- **License.** Podium is licensed under [MIT](https://github.com/lennylabs/podium/blob/main/LICENSE). Contributions are accepted under the same license.
- **Developer Certificate of Origin.** Every commit must be signed off with `git commit -s`. Podium requires no separate CLA.

## Contact

- **Issues and discussions:** [github.com/lennylabs/podium](https://github.com/lennylabs/podium)
- **Security:** the [security policy](https://github.com/lennylabs/podium/blob/main/SECURITY.md)
- **Conduct:** the [Contributor Covenant](https://github.com/lennylabs/podium/blob/main/CODE_OF_CONDUCT.md)
