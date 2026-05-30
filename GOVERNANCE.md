# Governance

## Current model: Benevolent Dictator for Now (BDfN)

During the design and early build phases, a single maintainer has final decision authority over architecture, merges, releases, and community policy. This model is intentionally lightweight and is not intended to be permanent.

## Transition to a steering committee

The BDfN model transitions to a multi-maintainer steering committee when the project reaches **three or more regular contributors**, where "regular" means someone who has:

- Landed substantive changes (not just typos or docs) in `main`,
- Participated in architectural discussions or RFC reviews, and
- Sustained that activity for at least three months.

When the criteria are met, the maintainer will propose an initial committee, the committee will draft a charter (decision rules, voting, membership, rotation), and this file will be updated to reflect the new model.

## Decision-making

Spec changes follow an RFC process. A Request for Comments document records the proposed change, its motivation, and the alternatives considered. RFCs live in [`docs/rfc/`](docs/rfc/); see that directory's index for the document format and numbering.

- **Proposals** are filed as GitHub Discussions or draft RFCs in [`docs/rfc/`](docs/rfc/).
- **Discussion** happens in the open, and anyone can participate.
- **Decisions** are documented in the relevant RFC or issue with rationale, including the rationale for rejected proposals.
- After the steering committee is formed, consensus is the preferred mode, and simple-majority voting is the fallback.

## License and DCO

- **License:** [MIT](LICENSE). Contributions are accepted under the same license.
- **Developer Certificate of Origin:** every commit must be signed off with `git commit -s`. No separate CLA.

## Contact

- **Issues and discussions:** <https://github.com/lennylabs/podium>
- **Security:** see [`SECURITY.md`](SECURITY.md)
- **Conduct:** see [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md)
