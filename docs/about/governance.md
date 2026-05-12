---
layout: default
title: Governance
parent: About
nav_order: 3
description: "How decisions are made: the maintainer model, transition criteria, and the proposal process."
---

# Governance

## Current model: Benevolent Dictator for Now (BDfN)

During the design and early build phases, a single maintainer has final decision authority over architecture, merges, releases, and community policy. This model is intentionally lightweight and is not intended to be permanent.

## Transition to a steering committee

The BDfN model transitions to a multi-maintainer steering committee when the project reaches **three or more regular contributors**, where "regular" means someone who has:

- Landed substantive changes beyond typos or docs in `main`,
- Participated in architectural discussions or ADR reviews, and
- Sustained that activity for at least three months.

When the criteria are met, the maintainer will propose an initial committee, the committee will draft a charter (decision rules, voting, membership, rotation), and this page will be updated to reflect the new model.

## Decision-making

- **Proposals** are filed as GitHub Discussions or draft ADRs in `docs/adr/`.
- **Discussion** happens in the open; anyone can participate.
- **Decisions** are documented in the relevant ADR or issue with rationale, including rationale for rejected proposals.
- After the steering committee is formed, consensus is the preferred mode; simple-majority voting is the fallback.

## License and DCO

- **License.** [MIT](https://github.com/lennylabs/podium/blob/main/LICENSE). Contributions are accepted under the same license.
- **Developer Certificate of Origin.** Every commit must be signed off with `git commit -s`. No separate CLA.

## Contact

- **Issues and discussions:** [github.com/lennylabs/podium](https://github.com/lennylabs/podium)
- **Security:** see the [security policy](https://github.com/lennylabs/podium/blob/main/SECURITY.md)
- **Conduct:** see the [Contributor Covenant](https://github.com/lennylabs/podium/blob/main/CODE_OF_CONDUCT.md)
