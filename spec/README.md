# Podium: Technical Specification

The specification is split across one file per top-level section. Read in order for the full technical reference, or jump to the section that addresses your question.

## Sections

0. [Quickstart](00-quickstart.md): minimal artifact, ingest, agent session.
1. [Overview](01-overview.md): what Podium is, problem statement, design principles, where Podium fits.
2. [Architecture](02-architecture.md): high-level component map, registry layout, consumer surfaces.
3. [Disclosure Surface](03-disclosure-surface.md): three-layer disclosure, scope preview, discovery flow.
4. [Artifact Model](04-artifact-model.md): artifacts, layout, manifest schema, bundled resources, domains, layers, visibility.
5. [Meta-Tools](05-meta-tools.md): `load_domain`, `search_artifacts`, `load_artifact`; tool descriptions and prompting guidance.
6. [MCP Server](06-mcp-server.md): bridge configuration, identity providers, workspace overlay, cache, materialization, harness adapters, host recipes.
7. [External Integration](07-external-integration.md): registry as external HTTP or local filesystem, host integration, `podium sync`, language SDKs, onboarding (`podium init`, `podium config show`, `podium login`).
8. [Audit and Observability](08-audit-and-observability.md): what gets logged, sinks, retention, erasure, integrity.
9. [Extensibility](09-extensibility.md): pluggable interfaces, plugin distribution, building on Podium externally.
10. [MVP Build Sequence](10-mvp-build-sequence.md): phased rollout from filesystem source to enterprise capabilities.
11. [Verification](11-verification.md): tests and conformance criteria.
12. [Operational Risks and Mitigations](12-operational-risks-and-mitigations.md): known risks and how the spec addresses them.
13. [Deployment](13-deployment.md): reference topology, runbook, backup/restore, multi-region, sizing, standalone, filesystem registry, backend configuration.
14. [Common Scenarios](14-common-scenarios.md): end-to-end walkthroughs.
15. [Glossary](glossary.md): terminology.

## Cross-references

Section references in the prose use `§N` or `§N.M` notation (e.g., `§4.6` for Layers and Visibility, `§13.11` for Filesystem Registry). To resolve one, open the file matching that top-level section number.
